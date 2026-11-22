package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func usageSnapshotForTest() *model.TaskUsageSnapshot {
	return &model.TaskUsageSnapshot{
		Schema: map[string]model.TaskUsageField{
			"seconds": {Path: "usage.seconds", Required: true, Max: 3600},
		},
		Expression: "seconds * 0.01", GroupRatio: 0.8, QuotaPerUnit: 500000,
		OtherRatios: map[string]float64{"resolution": 2},
	}
}

func TestTaskUsageSnapshotAndInvalidUsage(t *testing.T) {
	snapshot := usageSnapshotForTest()
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var persisted model.TaskUsageSnapshot
	require.NoError(t, json.Unmarshal(encoded, &persisted))
	snapshot.Schema["seconds"] = model.TaskUsageField{Path: "changed", Max: 1}
	snapshot.OtherRatios["resolution"] = 99
	snapshot.Expression = "0"
	quota, err := EvaluateTaskUsage(&persisted, []byte(`{"usage":{"seconds":3}}`))
	require.NoError(t, err)
	require.Equal(t, 24000, quota)
	for _, raw := range []string{`{}`, `{"usage":{"seconds":-1}}`, `{"usage":{"seconds":3601}}`, `{"usage":{"seconds":"3"}}`, `{"usage":{"seconds":3}} {}`} {
		_, err := EvaluateTaskUsage(&persisted, []byte(raw))
		require.Error(t, err, raw)
	}
	task := &model.Task{Quota: 3000, Data: json.RawMessage(`{}`), PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{UsageSnapshot: &persisted}}}
	quota, reason, handled := TaskUsageBillingTarget(task)
	require.True(t, handled)
	require.Equal(t, 3000, quota)
	require.Contains(t, reason, "保留预扣")
	persisted.Expression = "0"
	quota, err = EvaluateTaskUsage(&persisted, []byte(`{"usage":{"seconds":3}}`))
	require.NoError(t, err)
	require.Zero(t, quota)
	persisted.Expression, persisted.GroupRatio, persisted.QuotaPerUnit = "0.6", 1, 1
	persisted.OtherRatios = nil
	quota, err = EvaluateTaskUsage(&persisted, []byte(`{"usage":{"seconds":3}}`))
	require.NoError(t, err)
	require.Equal(t, 1, quota)
}

func TestTaskUsageRejectsExecutableExpressions(t *testing.T) {
	for _, expression := range []string{"map([1,2], # * seconds)", "seconds ** 1000", "[seconds][0]", "seconds.foo", "len('abc')"} {
		snapshot := usageSnapshotForTest()
		snapshot.Expression = expression
		_, err := EvaluateTaskUsage(snapshot, []byte(`{"usage":{"seconds":3}}`))
		require.Error(t, err, expression)
	}
}

func TestPollFailureResetAndDurableRefund(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	terminal, err := model.RecordTaskPollFailure(task, "transport", 2)
	require.NoError(t, err)
	require.False(t, terminal)
	require.Equal(t, 1, task.PrivateData.PollFailures)
	previous := task.Status
	task.PrivateData.PollFailures, task.PrivateData.LastPollClass = 0, "ok"
	won, err := task.UpdateWithStatus(previous)
	require.NoError(t, err)
	require.True(t, won)
	terminal, err = model.RecordTaskPollFailure(task, "auth", 2)
	require.NoError(t, err)
	require.False(t, terminal)
	terminal, err = model.RecordTaskPollFailure(task, "not_found", 2)
	require.NoError(t, err)
	require.True(t, terminal)
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusFailure), stored.Status)
	require.Equal(t, model.TaskBillingPending, stored.BillingState)
	require.Zero(t, stored.BillingQuota)
	RefundTaskQuota(context.Background(), &stored, "poll failure")
	RefundTaskQuota(context.Background(), task, "duplicate poll failure")
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	terminal, err = model.RecordTaskPollFailure(task, "transport", 2)
	require.NoError(t, err)
	require.False(t, terminal)
}

func TestTaskResultMetadataFiltersPrivateAndUnsafeFields(t *testing.T) {
	result := CollectTaskResultMetadata([]byte(`{"result_text":"done","api_key":"secret","artifacts":[{"kind":"image","url":"https://example.com/output.png"},{"url":"file:///etc/passwd"},{"url":"https://user:pass@example.com/private"},{"url":"javascript:alert(1)"},{"url":"https://example.com/output.png"}]}`), "")
	require.NotNil(t, result)
	require.Equal(t, "done", result.Text)
	require.Len(t, result.Artifacts, 1)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret")
}
