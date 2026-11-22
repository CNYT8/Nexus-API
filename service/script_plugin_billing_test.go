package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestScriptUsagePreservesFrozenPolicyAndNumericFacts(t *testing.T) {
	snapshot := usageSnapshotForTest()
	for _, test := range []struct {
		facts string
		quota int
	}{
		{`{"seconds":0}`, 0},
		{`{"seconds":1.5}`, 12000},
		{`{"seconds":3}`, 24000},
	} {
		quota, err := EvaluateScriptUsage(snapshot, []byte(test.facts))
		require.NoError(t, err)
		require.Equal(t, test.quota, quota)
	}
	require.Equal(t, "usage.seconds", snapshot.Schema["seconds"].Path)
	require.Equal(t, "seconds * 0.01", snapshot.Expression)
	require.Equal(t, 0.8, snapshot.GroupRatio)
	require.Equal(t, float64(2), snapshot.OtherRatios["resolution"])
	for _, raw := range []string{`{}`, `{"seconds":-1}`, `{"seconds":3601}`, `{"seconds":"3"}`, `{"seconds":3} {}`} {
		_, err := EvaluateScriptUsage(snapshot, []byte(raw))
		require.Error(t, err, raw)
	}
	_, err := EvaluateScriptUsage(nil, []byte(`{"seconds":3}`))
	require.Error(t, err)
}

func TestScriptUsageTargetUsesFactsWithoutReplacingTaskData(t *testing.T) {
	snapshot := usageSnapshotForTest()
	task := &model.Task{
		Quota: 3000, Data: []byte(`{"provider":"preserve"}`),
		PrivateData: model.TaskPrivateData{
			ScriptPluginID: 7, ScriptPluginUsage: `{"seconds":1.5}`,
			BillingContext: &model.TaskBillingContext{UsageSnapshot: snapshot},
		},
	}
	quota, _, handled := ScriptTaskUsageBillingTarget(task)
	require.True(t, handled)
	require.Equal(t, 12000, quota)
	require.Equal(t, `{"provider":"preserve"}`, string(task.Data))
	require.Same(t, snapshot, task.PrivateData.BillingContext.UsageSnapshot)
	require.Equal(t, "usage.seconds", snapshot.Schema["seconds"].Path)
	task.PrivateData.ScriptPluginUsage = `{"seconds":0}`
	quota, _, handled = ScriptTaskUsageBillingTarget(task)
	require.True(t, handled)
	require.Zero(t, quota)

	for _, invalid := range []string{`{}`, `{"seconds":-1}`, `{"seconds":"3"}`} {
		task.PrivateData.ScriptPluginUsage = invalid
		quota, _, handled = ScriptTaskUsageBillingTarget(task)
		require.True(t, handled)
		require.Equal(t, task.Quota, quota, "invalid usage must retain reservation, not fall back to per-call")
	}
	task.PrivateData.ScriptPluginUsageError = "completion_usage_invalid"
	quota, _, handled = ScriptTaskUsageBillingTarget(task)
	require.True(t, handled)
	require.Equal(t, task.Quota, quota)
	task.PrivateData.ScriptPluginUsageError = ""
	task.PrivateData.BillingContext.UsageSnapshot = nil
	quota, _, handled = ScriptTaskUsageBillingTarget(task)
	require.True(t, handled)
	require.Equal(t, task.Quota, quota)
}
