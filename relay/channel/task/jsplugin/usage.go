package jsplugin

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func (a *TaskAdaptor) hasUsage() bool {
	return a.plugin != nil && (len(a.plugin.Meta.UsageSchema) != 0 || len(a.plugin.Meta.UsageProfiles) != 0 || a.plugin.Engine.HasExport("extractUsage"))
}

func (a *TaskAdaptor) mergeUsage(ctx context.Context, hook string, args ...any) error {
	if !a.plugin.Engine.HasExport(hook) {
		return nil
	}
	value, err := a.plugin.Engine.Call(a.hookContext(ctx), hook, args...)
	if err != nil {
		return errors.New("script usage hook failed")
	}
	facts, err := pluginruntime.ValidateUsageFacts(value, a.plugin.Meta.UsageForModels(a.info.UpstreamModelName, a.info.OriginModelName))
	if err != nil {
		return err
	}
	next := make(map[string]any, len(a.usage)+len(facts))
	for name, value := range a.usage {
		next[name] = value
	}
	for name, value := range facts {
		next[name] = value
	}
	encoded, err := common.Marshal(next)
	if err != nil {
		return err
	}
	if a.usageSnapshot != nil {
		if _, err = service.EvaluateScriptUsage(a.usageSnapshot, encoded); err != nil {
			return errors.New("script usage violates the frozen billing policy")
		}
	}
	a.usage = next
	return nil
}

// The legacy ratios interface must not multiply the same facts twice. The
// host calls FreezeUsageBilling below before preconsumption and dispatch.
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if !a.hasUsage() {
		return nil
	}
	a.submitContext["publicTaskId"] = info.PublicTaskID
	a.submitContext["upstreamModel"] = info.UpstreamModelName
	a.submitContext["usagePurpose"] = "facts"
	if err := a.credentials(a.submitContext, info.ApiKey, info.ChannelSetting.Proxy); err != nil {
		a.usageError = "estimate_auth_failed"
		return nil
	}
	if err := a.mergeUsage(c.Request.Context(), "extractUsage", a.submitContext); err != nil {
		a.usageError = "estimate_usage_invalid"
	}
	delete(a.submitContext, "usagePurpose")
	return nil
}

func (a *TaskAdaptor) FreezeUsageBilling(snapshot *model.TaskUsageSnapshot) (int, bool, error) {
	if !a.hasUsage() {
		return 0, false, nil
	}
	if snapshot == nil || a.usageError != "" {
		return 0, true, errors.New("script usage requires a valid estimate and a bound declarative usage plugin")
	}
	schema := a.plugin.Meta.UsageForModels(a.info.UpstreamModelName, a.info.OriginModelName)
	for name := range snapshot.Schema {
		if _, declared := schema[name]; !declared {
			return 0, true, errors.New("frozen usage schema does not match script fact names")
		}
	}
	a.usageSnapshot = snapshot
	facts, _ := a.UsageSnapshot()
	quota, err := service.EvaluateScriptUsage(snapshot, []byte(facts))
	return quota, true, err
}

func (a *TaskAdaptor) AdjustBillingOnSubmit(_ *relaycommon.RelayInfo, data []byte) map[string]float64 {
	if a.hasUsage() && a.usageError == "" {
		if err := a.mergeUsage(a.ctx, "extractUsageOnSubmit", a.submitContext, jsonValue(data)); err != nil {
			a.usageError = "submit_usage_invalid"
		}
	}
	return nil
}

// Completion facts are extracted in the response parser, with the real
// TaskQueryContext and raw taskData, before the host status CAS. This legacy
// method never supplies a price; TaskUsageBillingTarget remains authoritative.
func (a *TaskAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return 0
}

func (a *TaskAdaptor) completionUsage(query map[string]any, result *relaycommon.TaskInfo, data any) {
	if !a.hasUsage() || a.usageError != "" || result.Status != model.TaskStatusSuccess {
		return
	}
	// Plugin TaskResult uses camelCase, unlike the host polling DTO.
	pluginResult := map[string]any{
		"code": result.Code, "taskId": result.TaskID, "status": result.Status,
		"progress": result.Progress, "reason": result.Reason, "url": result.Url,
		"remoteUrl": result.RemoteUrl, "completionTokens": result.CompletionTokens,
		"totalTokens": result.TotalTokens,
	}
	if err := a.mergeUsage(a.ctx, "extractUsageOnComplete", query, pluginResult, data); err != nil {
		a.usageError = "completion_usage_invalid"
	}
}

func (a *TaskAdaptor) UsageSnapshot() (string, string) {
	if !a.hasUsage() {
		return "", ""
	}
	data, err := common.Marshal(a.usage)
	if err != nil {
		return "", "usage_encoding_invalid"
	}
	return string(data), a.usageError
}

func (a *TaskAdaptor) UsageQuote(fallback int) int {
	if !a.hasUsage() || a.usageError != "" {
		return fallback
	}
	facts, _ := a.UsageSnapshot()
	quota, err := service.EvaluateScriptUsage(a.usageSnapshot, []byte(facts))
	if err != nil {
		a.usageError = "usage_evaluation_invalid"
		return fallback
	}
	return quota
}
