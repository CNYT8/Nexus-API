package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogExportOptInCompletionAndMidstreamFailure(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	now := time.Now().Unix()
	logs := make([]model.Log, 201)
	for i := range logs {
		logs[i] = model.Log{UserId: 42, CreatedAt: now - 1, Type: model.LogTypeConsume, ChannelId: 999, Other: `{"root_info":{"secret":"hidden"}}`}
	}
	require.NoError(t, model.LOG_DB.CreateInBatches(&logs, 50).Error)
	export := func(query string) (*httptest.ResponseRecorder, []map[string]json.RawMessage) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 42)
		c.Set("role", common.RoleCommonUser)
		c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		ExportUserLogs(c)
		require.Equal(t, http.StatusOK, w.Code)
		var lines []map[string]json.RawMessage
		for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
			var item map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(line), &item))
			lines = append(lines, item)
		}
		return w, lines
	}
	_, legacy := export("")
	require.Len(t, legacy, 201)
	for _, line := range legacy {
		require.NotContains(t, line, "_export")
		require.Equal(t, "0", string(line["channel"]))
		require.NotContains(t, string(line["other"]), "hidden")
	}
	w, complete := export("include_summary=true")
	require.Equal(t, "1", w.Header().Get("X-Export-Summary"))
	require.Len(t, complete, 202)
	require.JSONEq(t, `{"version":1,"status":"complete","count":201,"max_rows":100000}`, string(complete[201]["_export"]))

	queries := 0
	const callback = "test:log_export_midstream_failure"
	require.NoError(t, model.LOG_DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "logs" {
			queries++
			if queries == 2 {
				tx.AddError(errors.New("injected log query failure"))
			}
		}
	}))
	t.Cleanup(func() { _ = model.LOG_DB.Callback().Query().Remove(callback) })
	failedResponse, incomplete := export("include_summary=true")
	require.Len(t, incomplete, 201)
	require.JSONEq(t, `{"version":1,"status":"failed","count":200,"max_rows":100000}`, string(incomplete[200]["_export"]))
	require.NotContains(t, failedResponse.Body.String(), "injected log query failure")
	require.NoError(t, model.LOG_DB.Callback().Query().Remove(callback))
	require.NoError(t, model.LOG_DB.Where("user_id = ?", 42).Delete(&model.Log{}).Error)
	_, empty := export("include_summary=true")
	require.Len(t, empty, 1)
	require.JSONEq(t, `{"version":1,"status":"complete","count":0,"max_rows":100000}`, string(empty[0]["_export"]))
}
