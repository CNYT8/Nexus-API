package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/error_mask_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setControllerLogPolicy(t *testing.T, days int, enabled bool) {
	t.Helper()
	settings := config.GlobalConfig.Get("log_setting").(*operation_setting.LogSetting)
	common.OptionMapRWMutex.Lock()
	original := *settings
	*settings = operation_setting.LogSetting{MaxRangeDays: days, UserExportEnabled: enabled}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*settings = original
		common.OptionMapRWMutex.Unlock()
	})
}

func invokeLogPolicyHandler(handler gin.HandlerFunc, id, role int, query string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set("id", id)
	c.Set("role", role)
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	handler(c)
	return response
}

func TestLogSettingsContractAndExportDisabled(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	setControllerLogPolicy(t, 365, false)
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		response := invokeLogPolicyHandler(GetLogSettings, 27, role, "")
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				MaxRangeDays      int  `json:"max_range_days"`
				DefaultRangeDays  int  `json:"default_range_days"`
				UserExportEnabled bool `json:"user_export_enabled"`
				ExportEnabled     bool `json:"export_enabled"`
				ExportMaxRows     int  `json:"export_max_rows"`
				ExportBatchSize   int  `json:"export_batch_size"`
				ExportConcurrency int  `json:"export_concurrency_per_user"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		require.Equal(t, 365, result.Data.MaxRangeDays)
		require.Equal(t, 31, result.Data.DefaultRangeDays)
		require.False(t, result.Data.UserExportEnabled)
		require.Equal(t, role >= common.RoleAdminUser, result.Data.ExportEnabled)
		require.Equal(t, 100000, result.Data.ExportMaxRows)
		require.Equal(t, 200, result.Data.ExportBatchSize)
		require.Equal(t, 1, result.Data.ExportConcurrency)
	}
	require.Equal(t, http.StatusForbidden, invokeLogPolicyHandler(GetLogSettings, 0, common.RoleCommonUser, "").Code)
	require.Equal(t, http.StatusForbidden, invokeLogPolicyHandler(ExportUserLogs, 27, common.RoleCommonUser, "").Code)
	require.Equal(t, http.StatusOK, invokeLogPolicyHandler(ExportUserLogs, 27, common.RoleAdminUser, "include_summary=true").Code)
	require.Equal(t, http.StatusOK, invokeLogPolicyHandler(ExportAllLogs, 27, common.RoleRootUser, "include_summary=true").Code)
	require.Equal(t, http.StatusForbidden, invokeLogPolicyHandler(ExportAllLogs, 27, common.RoleCommonUser, "").Code)
}

func TestLogQueryWindowRejectsEmptyDuplicateAndOverLimit(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	setControllerLogPolicy(t, 31, true)
	for _, handler := range []gin.HandlerFunc{GetUserLogs, GetLogsSelfStat, ExportUserLogs, ExportAllLogs} {
		for _, query := range []string{
			"start_timestamp=", "end_timestamp=", "start_timestamp=-1", "end_timestamp=1.2",
			"start_timestamp=2&end_timestamp=1", "end_timestamp=9223372036854775808",
			"start_timestamp=1&start_timestamp=2", "start_timestamp=1&end_timestamp=2678402",
		} {
			response := invokeLogPolicyHandler(handler, 27, common.RoleRootUser, query)
			require.Equal(t, http.StatusBadRequest, response.Code, query)
			require.NotContains(t, response.Header().Get("Content-Type"), "ndjson")
		}
	}
	for _, days := range []int{7, 31, 365} {
		t.Run((time.Duration(days) * 24 * time.Hour).String(), func(t *testing.T) {
			setControllerLogPolicy(t, days, true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/?end_timestamp=2000000000", nil)
			start, end, err := parseLogWindow(c)
			require.NoError(t, err)
			window := days
			if window > 31 {
				window = 31
			}
			require.EqualValues(t, window*86400, end-start)
		})
	}
}

func TestLogExportBusyAndLockUnavailableStatuses(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	setControllerLogPolicy(t, 365, true)
	_, release, err := model.AcquireLogExportLease(context.Background(), 27)
	require.NoError(t, err)
	defer release()
	for _, handler := range []gin.HandlerFunc{ExportUserLogs, ExportAllLogs} {
		require.Equal(t, http.StatusTooManyRequests, invokeLogPolicyHandler(handler, 27, common.RoleRootUser, "").Code)
	}
	require.Equal(t, http.StatusOK, invokeLogPolicyHandler(ExportUserLogs, 28, common.RoleCommonUser, "include_summary=true").Code)
	release()
	require.Equal(t, http.StatusOK, invokeLogPolicyHandler(ExportUserLogs, 27, common.RoleCommonUser, "include_summary=true").Code)
	originalClient := common.RDB
	common.RedisEnabled, common.RDB = true, nil
	t.Cleanup(func() { common.RDB = originalClient })
	require.Equal(t, http.StatusServiceUnavailable, invokeLogPolicyHandler(ExportUserLogs, 27, common.RoleCommonUser, "").Code)
}

func TestLogExportChannelNamesAndUserErrorPrivacy(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	setControllerLogPolicy(t, 365, true)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	mask := config.GlobalConfig.Get("error_mask_setting").(*error_mask_setting.ErrorMaskSetting)
	originalMask := *mask
	*mask = error_mask_setting.ErrorMaskSetting{Enabled: false}
	t.Cleanup(func() { *mask = originalMask })
	originalCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalCache })
	admin := &model.User{Username: "export-admin", AffCode: "export-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	restricted := &model.User{Username: "export-restricted", AffCode: "export-restricted", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(admin).Error)
	require.NoError(t, model.DB.Create(restricted).Error)
	permissions := model.DefaultAdminPermissionConfig()
	permissions[model.AdminPermissionChannel] = false
	require.NoError(t, model.SetAdminPermissionConfig(restricted, permissions))
	channel := &model.Channel{Name: "private-channel", Key: "private-key"}
	require.NoError(t, model.DB.Create(channel).Error)
	rows := []model.Log{
		{UserId: 27, CreatedAt: time.Now().Unix() - 1, Type: model.LogTypeConsume, ChannelId: channel.Id, ModelName: "public-model", Other: `{"number":9007199254740993,"upstream_model_name":"private-model","root_info":{"secret":"root-only"}}`},
		{UserId: 27, CreatedAt: time.Now().Unix() - 1, Type: model.LogTypeError, ChannelId: channel.Id, ModelName: "private-model", Content: "private-channel private-model private-key", Other: `{"raw_error":"private-key"}`},
	}
	require.NoError(t, model.LOG_DB.Create(&rows).Error)
	for _, tc := range []struct {
		name                  string
		id, role              int
		handler               gin.HandlerFunc
		showChannel, showRoot bool
	}{
		{"self", 27, common.RoleCommonUser, ExportUserLogs, false, false},
		{"admin", admin.Id, common.RoleAdminUser, ExportAllLogs, true, false},
		{"restricted", restricted.Id, common.RoleAdminUser, ExportAllLogs, false, false},
		{"root", admin.Id, common.RoleRootUser, ExportAllLogs, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channelQueries := 0
			const callback = "test:log-policy-channel-query"
			require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "channels" {
					channelQueries++
				}
			}))
			t.Cleanup(func() { _ = model.DB.Callback().Query().Remove(callback) })
			response := invokeLogPolicyHandler(tc.handler, tc.id, tc.role, "include_summary=true")
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, "100000", response.Header().Get("X-Export-Max-Rows"))
			require.Equal(t, "complete", response.Result().Trailer.Get("X-Export-Status"))
			lines := strings.Split(strings.TrimSpace(response.Body.String()), "\n")
			require.Len(t, lines, 3)
			for _, line := range lines[:2] {
				var entry model.Log
				require.NoError(t, json.Unmarshal([]byte(line), &entry))
				if tc.showChannel {
					require.Equal(t, "private-channel", entry.ChannelName)
				} else {
					require.Zero(t, entry.ChannelId)
					require.Empty(t, entry.ChannelName)
				}
			}
			if !tc.showChannel {
				require.Zero(t, channelQueries, "user/restricted exports must not load channel names")
			} else {
				require.Positive(t, channelQueries)
			}
			if tc.name == "self" {
				for _, secret := range []string{"private-channel", "private-model", "private-key", "raw_error"} {
					require.NotContains(t, response.Body.String(), secret)
				}
				require.Contains(t, response.Body.String(), "9007199254740993")
			}
			if !tc.showRoot {
				require.NotContains(t, response.Body.String(), "root-only")
			} else {
				require.Contains(t, response.Body.String(), "root-only")
			}
		})
	}
	var stored model.Log
	require.NoError(t, model.LOG_DB.First(&stored, rows[1].Id).Error)
	require.Equal(t, rows[1].Content, stored.Content)
	require.Equal(t, rows[1].Other, stored.Other)
}
