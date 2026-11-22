package service

import (
	"errors"

	"github.com/QuantumNous/new-api/model"
)

// Script facts are keyed by usageSchema names, not provider JSON paths. Copy the
// frozen schema for evaluation only; never mutate the declarative revision or
// replace its expression, member group ratio, or other frozen multipliers.
func scriptUsageSnapshot(snapshot *model.TaskUsageSnapshot) (*model.TaskUsageSnapshot, error) {
	if snapshot == nil {
		return nil, errors.New("script usage requires a bound declarative usage plugin")
	}
	copy := *snapshot
	copy.Schema = make(map[string]model.TaskUsageField, len(snapshot.Schema))
	for name, field := range snapshot.Schema {
		field.Path = name
		field.Required = true
		copy.Schema[name] = field
	}
	return &copy, nil
}

func EvaluateScriptUsage(snapshot *model.TaskUsageSnapshot, facts []byte) (int, error) {
	copy, err := scriptUsageSnapshot(snapshot)
	if err != nil {
		return 0, err
	}
	return EvaluateTaskUsage(copy, facts)
}

// ScriptTaskUsageBillingTarget is only a projection into the existing evaluator.
// The caller still owns terminal status CAS, reservation, settlement and refund.
func ScriptTaskUsageBillingTarget(task *model.Task) (int, string, bool) {
	if task == nil || task.PrivateData.ScriptPluginID == 0 ||
		task.PrivateData.ScriptPluginUsage == "" && task.PrivateData.ScriptPluginUsageError == "" {
		return TaskUsageBillingTarget(task)
	}
	if task.PrivateData.ResultDiscarded {
		return task.BillingQuota, "提交时已冻结脚本用量结算", true
	}
	if task.PrivateData.ScriptPluginUsageError != "" || task.PrivateData.BillingContext == nil {
		return task.Quota, "脚本用量无效，保留预扣额度", true
	}
	snapshot, err := scriptUsageSnapshot(task.PrivateData.BillingContext.UsageSnapshot)
	if err != nil {
		return task.Quota, "脚本用量缺少冻结策略，保留预扣额度", true
	}
	copy := *task
	billing := *task.PrivateData.BillingContext
	billing.UsageSnapshot = snapshot
	copy.PrivateData.BillingContext = &billing
	copy.Data = []byte(task.PrivateData.ScriptPluginUsage)
	return TaskUsageBillingTarget(&copy)
}
