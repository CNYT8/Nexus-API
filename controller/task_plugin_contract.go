package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const taskUsageBindingsOption = "task_setting.usage_plugins"

var taskPluginBindingsMu sync.Mutex

func decodeTaskPluginRequest(c *gin.Context, limit int64, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func GetTaskPluginSchema(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	fieldSchema := gin.H{
		"type": "object", "additionalProperties": false, "required": []string{"path", "max"},
		"properties": gin.H{
			"path":     gin.H{"type": "string", "maxLength": 256, "pattern": `^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+){0,7}$`},
			"required": gin.H{"type": "boolean", "default": false},
			"default":  gin.H{"type": "number", "minimum": 0, "maximum": 1e12, "default": 0},
			"max":      gin.H{"type": "number", "exclusiveMinimum": 0, "maximum": 1e12},
		},
	}
	example := model.TaskPlugin{
		Name: "video-seconds", Version: "1",
		Schema:     `{"seconds":{"path":"usage.seconds","required":true,"default":0,"max":3600}}`,
		Expression: "seconds * 0.01",
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{
		"kind": "declarative_usage_billing", "api_version": 1, "schema_encoding": "json_string",
		"currency": "USD", "quota_per_unit": common.QuotaPerUnit, "immutable_revisions": true,
		"binding_option": taskUsageBindingsOption, "binding_match": "exact_model_name",
		"invalid_usage_policy": "keep_reserved_quota",
		"result_retention": gin.H{
			"field": "retainResult", "type": "boolean", "default": true, "nullable": true,
			"scope": "root_declarative_revision", "local_extension": true, "frozen_at_submit": true,
			"discard_when": "submit_terminal_only", "asynchronous_results": "retained",
			"billing_before_discard": true, "immediate_response": "preserved", "request_parameter": false,
		},
		"limits": gin.H{"name_bytes": 96, "version_bytes": 64, "schema_bytes": 16384, "expression_bytes": 4096, "expression_nodes": 256, "response_bytes": 4 * 1024 * 1024, "multipliers": 64, "bindings": 256},
		"usage_schema": gin.H{
			"type": "object", "minProperties": 1, "maxProperties": 32,
			"propertyNames":        gin.H{"pattern": `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`},
			"additionalProperties": fieldSchema,
		},
		"expression_operators": []string{"+", "-", "*", "/", "%", "<", "<=", ">", ">=", "==", "!=", "&&", "||", "and", "or", "!", "not", "?:"},
		"capabilities":         gin.H{"preview": true, "usage_billing": true, "provider_scripts": false, "custom_protocol_routes": false},
		"example_create":       example,
		"example_response":     gin.H{"usage": gin.H{"seconds": 3}},
		"example_group_ratio":  0.8, "example_other_ratios": gin.H{"resolution": 2},
	})
}

// PreviewTaskPlugin evaluates the same snapshot as submit, with no dispatch or
// financial writes. Exactly one of plugin_id and plugin is accepted.
func PreviewTaskPlugin(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	var request struct {
		PluginID    int64              `json:"plugin_id"`
		Plugin      *model.TaskPlugin  `json:"plugin"`
		Response    json.RawMessage    `json:"response"`
		GroupRatio  *float64           `json:"group_ratio"`
		OtherRatios map[string]float64 `json:"other_ratios"`
	}
	if err := decodeTaskPluginRequest(c, 4*1024*1024+64*1024, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.PluginID < 0 || (request.PluginID > 0) == (request.Plugin != nil) {
		common.ApiErrorMsg(c, "provide exactly one of plugin_id or plugin")
		return
	}
	plugin := request.Plugin
	if request.PluginID > 0 {
		var err error
		plugin, err = model.GetTaskPlugin(request.PluginID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	} else if plugin.ID != 0 {
		common.ApiErrorMsg(c, "draft plugin id must be zero")
		return
	}
	groupRatio := 1.0
	if request.GroupRatio != nil {
		groupRatio = *request.GroupRatio
	}
	snapshot, err := service.NewTaskUsageSnapshot(plugin, groupRatio, request.OtherRatios)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	quota, err := service.EvaluateTaskUsage(snapshot, request.Response)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{
		"dry_run": true, "quota": quota, "currency": "USD", "quota_per_unit": snapshot.QuotaPerUnit,
		"plugin_id": snapshot.PluginID, "group_ratio": snapshot.GroupRatio, "other_ratios": snapshot.OtherRatios,
	})
}

func GetTaskPluginBindings(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{"option_key": taskUsageBindingsOption, "bindings": operation_setting.GetTaskUsagePluginBindings()})
}

// UpdateTaskPluginBindings replaces the whole mapping. Existing task snapshots
// are independent of the mapping and remain valid after unbinding a model.
func UpdateTaskPluginBindings(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	var request struct {
		Bindings map[string]int64 `json:"bindings"`
	}
	if err := decodeTaskPluginRequest(c, 128*1024, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	encoded, err := json.Marshal(request.Bindings)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ValidateTaskPluginBindingsJSON(string(encoded)); err != nil {
		common.ApiError(c, err)
		return
	}
	checked := make(map[int64]bool)
	for _, id := range request.Bindings {
		if id == 0 || checked[id] {
			continue
		}
		plugin, err := model.GetTaskPlugin(id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if err := service.ValidateTaskPlugin(plugin); err != nil {
			common.ApiError(c, err)
			return
		}
		checked[id] = true
	}
	taskPluginBindingsMu.Lock()
	err = model.UpdateOption(taskUsageBindingsOption, string(encoded))
	taskPluginBindingsMu.Unlock()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{"option_key": taskUsageBindingsOption, "bindings": request.Bindings})
}
