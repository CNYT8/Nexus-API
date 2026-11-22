package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogExportFirstBatchFailureSummaryAndLegacyResponse(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	setControllerLogPolicy(t, 365, true)
	const callback = "test:log-export-first-batch-failure"
	db := model.LOG_DB
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "logs" {
			tx.AddError(errors.New("private database address and credentials"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })

	response := invokeLogPolicyHandler(ExportUserLogs, 42, common.RoleCommonUser, "include_summary=true")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Header().Get("Content-Type"), "application/x-ndjson")
	require.Equal(t, "1", response.Header().Get("X-Export-Summary"))
	require.JSONEq(t, `{"_export":{"version":1,"status":"failed","count":0,"max_rows":100000}}`, response.Body.String())
	require.Equal(t, "failed", response.Result().Trailer.Get("X-Export-Status"))

	legacy := invokeLogPolicyHandler(ExportUserLogs, 42, common.RoleCommonUser, "")
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(legacy.Body.Bytes(), &result))
	require.False(t, result.Success)
	require.Equal(t, "日志导出失败，请稍后重试", result.Message)
	require.NotContains(t, legacy.Body.String(), "private database")
	require.NotContains(t, legacy.Body.String(), "_export")
}

func TestLogExportLimitProbeAndCancellation(t *testing.T) {
	// 使用跨批次且非批大小整数倍的上限，检查游标、剩余额度及探查筛选。
	const maxRows = model.LogExportBatchSize + 1
	const timestamp int64 = 2000000000
	for _, test := range []struct {
		name        string
		matches     int
		failQuery   int
		cancelQuery int
		wantCount   int
		wantStatus  string
	}{
		{name: "empty", wantStatus: "complete"},
		{name: "below_limit", matches: 1, wantCount: 1, wantStatus: "complete"},
		{name: "exact_limit", matches: maxRows, wantCount: maxRows, wantStatus: "complete"},
		{name: "extra_matching_row", matches: maxRows + 1, wantCount: maxRows, wantStatus: "limit_reached"},
		{name: "probe_failure", matches: maxRows, failQuery: 3, wantCount: maxRows, wantStatus: "failed"},
		{name: "cancel_empty_result", cancelQuery: 1, wantStatus: "failed"},
		{name: "cancel_probe_result", matches: maxRows, cancelQuery: 3, wantCount: maxRows, wantStatus: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupOAuthLegacyBindingControllerTestDB(t)
			db := model.LOG_DB
			rows := []model.Log{
				{Id: 1, UserId: 43, CreatedAt: timestamp, Type: model.LogTypeConsume, ModelName: "matched", Group: "public"},
				{Id: 2, UserId: 42, CreatedAt: timestamp, Type: model.LogTypeConsume, ModelName: "other", Group: "public"},
				{Id: 3, UserId: 42, CreatedAt: timestamp, Type: model.LogTypeConsume, ModelName: "matched", Group: "other"},
			}
			for i := 0; i < test.matches; i++ {
				rows = append(rows, model.Log{Id: 1000 + i, UserId: 42, CreatedAt: timestamp, Type: model.LogTypeConsume, ModelName: "matched", Group: "public", ChannelId: 99})
			}
			require.NoError(t, db.CreateInBatches(&rows, 50).Error)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			queries := 0
			const callback = "test:log-export-limit-probe"
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "logs" {
					return
				}
				queries++
				if queries == test.failQuery {
					tx.AddError(errors.New("private probe error"))
				}
				if queries == test.cancelQuery {
					cancel()
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })

			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Set("id", 42)
			c.Set("role", common.RoleCommonUser)
			c.Request = httptest.NewRequest(http.MethodGet, "/?include_summary=true", nil)
			filter := model.LogExportFilter{UserID: 42, Type: model.LogTypeConsume, Start: timestamp - 1, End: timestamp, ModelName: "matched", Group: "public"}
			writeLogExport(c, ctx, filter, true, false, maxRows)

			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, strconv.Itoa(maxRows), response.Header().Get("X-Export-Max-Rows"))
			lines := strings.Split(strings.TrimSpace(response.Body.String()), "\n")
			require.Len(t, lines, test.wantCount+1)
			for i, line := range lines[:test.wantCount] {
				var row model.Log
				require.NoError(t, json.Unmarshal([]byte(line), &row))
				require.Equal(t, i+1, row.Id)
				require.Equal(t, 42, row.UserId)
				require.Equal(t, "matched", row.ModelName)
				require.Zero(t, row.ChannelId)
				require.Empty(t, row.ChannelName)
			}
			var summary struct {
				Export struct {
					Version int    `json:"version"`
					Status  string `json:"status"`
					Count   int    `json:"count"`
					MaxRows int    `json:"max_rows"`
				} `json:"_export"`
			}
			require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &summary))
			require.Equal(t, 1, summary.Export.Version)
			require.Equal(t, test.wantStatus, summary.Export.Status)
			require.Equal(t, test.wantCount, summary.Export.Count)
			require.Equal(t, maxRows, summary.Export.MaxRows)
			require.Equal(t, test.wantStatus, response.Result().Trailer.Get("X-Export-Status"))
			require.True(t, strings.HasSuffix(response.Body.String(), "\n"))
			require.NotContains(t, response.Body.String(), "private probe error")
		})
	}
}
