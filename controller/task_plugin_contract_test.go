package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTaskPluginContractPreviewBindingAndFrozenUsage(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.TaskPlugin{}, &model.Option{}))
	setting := config.GlobalConfig.Get("task_setting")
	require.NotNil(t, setting)
	common.OptionMapRWMutex.Lock()
	previousConfig, snapshotErr := config.ConfigToMap(setting)
	previousOptions := make(map[string]string, len(common.OptionMap))
	for key, value := range common.OptionMap {
		previousOptions[key] = value
	}
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, snapshotErr)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		common.OptionMapRWMutex.Lock()
		err := config.UpdateConfigFromMap(setting, previousConfig)
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, err)
	})

	type envelope struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	request := func(handler gin.HandlerFunc, role int, method, target string, body []byte) (*httptest.ResponseRecorder, envelope) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("role", role)
		c.Request = httptest.NewRequest(method, target, bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		var response envelope
		if w.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), w.Body.String())
		}
		return w, response
	}
	encode := func(value any) []byte {
		t.Helper()
		body, err := json.Marshal(value)
		require.NoError(t, err)
		return body
	}

	for _, handler := range []gin.HandlerFunc{CreateTaskPlugin, ListTaskPlugins, GetTaskPluginSchema, PreviewTaskPlugin, GetTaskPluginBindings, UpdateTaskPluginBindings} {
		w, _ := request(handler, common.RoleAdminUser, http.MethodPost, "/", []byte(`{}`))
		require.Equal(t, http.StatusForbidden, w.Code)
	}
	_, schema := request(GetTaskPluginSchema, common.RoleRootUser, http.MethodGet, "/", nil)
	require.True(t, schema.Success, schema.Message)
	var contract struct {
		SchemaEncoding  string           `json:"schema_encoding"`
		Example         model.TaskPlugin `json:"example_create"`
		ResultRetention struct {
			Field            string `json:"field"`
			LocalExtension   bool   `json:"local_extension"`
			DiscardWhen      string `json:"discard_when"`
			RequestParameter bool   `json:"request_parameter"`
		} `json:"result_retention"`
	}
	require.NoError(t, json.Unmarshal(schema.Data, &contract))
	require.Equal(t, "json_string", contract.SchemaEncoding)
	require.Equal(t, "retainResult", contract.ResultRetention.Field)
	require.True(t, contract.ResultRetention.LocalExtension)
	require.Equal(t, "submit_terminal_only", contract.ResultRetention.DiscardWhen)
	require.False(t, contract.ResultRetention.RequestParameter)
	retain := false
	contract.Example.RetainResult = &retain

	previewBody := encode(map[string]any{
		"plugin": contract.Example, "response": map[string]any{"usage": map[string]int{"seconds": 3}},
		"group_ratio": 0.8, "other_ratios": map[string]float64{"resolution": 2},
	})
	_, preview := request(PreviewTaskPlugin, common.RoleRootUser, http.MethodPost, "/", previewBody)
	require.True(t, preview.Success, preview.Message)
	var calculation struct {
		DryRun bool `json:"dry_run"`
		Quota  int  `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(preview.Data, &calculation))
	require.True(t, calculation.DryRun)
	require.Equal(t, 24000, calculation.Quota)
	var count int64
	require.NoError(t, model.DB.Model(&model.TaskPlugin{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, model.DB.Model(&model.Option{}).Count(&count).Error)
	require.Zero(t, count)

	_, created := request(CreateTaskPlugin, common.RoleRootUser, http.MethodPost, "/", encode(contract.Example))
	require.True(t, created.Success, created.Message)
	var plugin model.TaskPlugin
	require.NoError(t, json.Unmarshal(created.Data, &plugin))
	require.Positive(t, plugin.ID)
	require.NotNil(t, plugin.RetainResult)
	require.False(t, *plugin.RetainResult)
	bindingBody := []byte(fmt.Sprintf(`{"bindings":{"video-model":%d}}`, plugin.ID))
	_, updated := request(UpdateTaskPluginBindings, common.RoleRootUser, http.MethodPut, "/", bindingBody)
	require.True(t, updated.Success, updated.Message)
	require.Equal(t, plugin.ID, operation_setting.GetTaskUsagePluginID("video-model"))
	var option model.Option
	require.NoError(t, model.DB.Where("key = ?", taskUsageBindingsOption).First(&option).Error)
	require.JSONEq(t, fmt.Sprintf(`{"video-model":%d}`, plugin.ID), option.Value)
	bindingsCopy := operation_setting.GetTaskUsagePluginBindings()
	bindingsCopy["video-model"] = 999999
	require.Equal(t, plugin.ID, operation_setting.GetTaskUsagePluginID("video-model"))

	snapshot, err := service.FreezeTaskUsageBilling("video-model", 0.8, map[string]float64{"resolution": 2})
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.NotNil(t, snapshot.RetainResult)
	require.False(t, *snapshot.RetainResult)
	_, invalidBinding := request(UpdateTaskPluginBindings, common.RoleRootUser, http.MethodPut, "/", []byte(`{"bindings":{"video-model":999999}}`))
	require.False(t, invalidBinding.Success)
	require.Equal(t, plugin.ID, operation_setting.GetTaskUsagePluginID("video-model"))
	_, unbound := request(UpdateTaskPluginBindings, common.RoleRootUser, http.MethodPut, "/", []byte(`{"bindings":{}}`))
	require.True(t, unbound.Success, unbound.Message)
	current, err := service.FreezeTaskUsageBilling("video-model", 1, nil)
	require.NoError(t, err)
	require.Nil(t, current)
	quota, err := service.EvaluateTaskUsage(snapshot, []byte(`{"usage":{"seconds":3}}`))
	require.NoError(t, err)
	require.Equal(t, 24000, quota)

	_, badPreview := request(PreviewTaskPlugin, common.RoleRootUser, http.MethodPost, "/", encode(map[string]any{"plugin_id": plugin.ID, "response": map[string]any{}}))
	require.False(t, badPreview.Success)
	_, extraField := request(CreateTaskPlugin, common.RoleRootUser, http.MethodPost, "/", []byte(`{"name":"x","version":"1","unknown":true}`))
	require.False(t, extraField.Success)
	_, trailing := request(PreviewTaskPlugin, common.RoleRootUser, http.MethodPost, "/", append(previewBody, []byte(` {}`)...))
	require.False(t, trailing.Success)
	_, invalidCursor := request(ListTaskPlugins, common.RoleRootUser, http.MethodGet, "/?before_id=-1", nil)
	require.False(t, invalidCursor.Success)
}
