package relay

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTaskModel2DtoUsageSnapshotExposesOnlyPublicDefinition(t *testing.T) {
	snapshot := &model.TaskUsageSnapshot{
		PluginID: 907,
		Name:     "video-seconds",
		Version:  "1",
		Schema: map[string]model.TaskUsageField{
			"seconds": {Path: "usage.seconds", Required: true, Default: 0, Max: 3600},
		},
		Expression:   "seconds * 0.01",
		GroupRatio:   0.8,
		OtherRatios:  map[string]float64{"private-channel-ratio": 2},
		QuotaPerUnit: 500000,
	}
	task := &model.Task{
		TaskID:    "task-public-id",
		ChannelId: 41,
		Data:      json.RawMessage(`{"result":"public-output"}`),
		PrivateData: model.TaskPrivateData{
			Key:                     "upstream-secret-key",
			UpstreamTaskID:          "upstream-private-job",
			BillingKey:              "wallet-private-reservation",
			BillingSource:           "subscription",
			SubscriptionId:          313,
			SubscriptionPeriodStart: 1800000000,
			TokenId:                 317,
			PollFailures:            3,
			LastPollClass:           "private-poll-class",
			ResultMetadata:          &model.TaskResultMetadata{Text: "private-result-metadata"},
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.1,
				GroupRatio:      0.8,
				ModelRatio:      2,
				OtherRatios:     map[string]float64{"private-billing-ratio": 3},
				OriginModelName: "private-billing-model",
				UsageSnapshot:   snapshot,
			},
		},
	}

	projected := TaskModel2Dto(task)
	require.NotNil(t, projected.UsageSnapshot)
	encoded, err := json.Marshal(projected)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.JSONEq(t, `{
		"name":"video-seconds",
		"version":"1",
		"schema":{"seconds":{"path":"usage.seconds","required":true,"default":0,"max":3600}},
		"expression":"seconds * 0.01"
	}`, string(fields["usage_snapshot"]))
	for _, privateField := range []string{
		"private_data", "billing_context", "key", "upstream_task_id", "billing_key",
		"billing_source", "subscription_id", "subscription_period_start", "token_id",
		"poll_failures", "last_poll_class", "result_metadata", "plugin_id", "group_ratio",
		"model_price", "model_ratio", "other_ratios", "quota_per_unit",
	} {
		require.NotContains(t, fields, privateField)
	}
	for _, privateValue := range []string{
		"upstream-secret-key", "upstream-private-job", "wallet-private-reservation",
		"private-channel-ratio", "private-billing-ratio", "private-billing-model",
		"private-poll-class", "private-result-metadata",
	} {
		require.NotContains(t, string(encoded), privateValue)
	}

	// DTO consumers cannot mutate the frozen definition used by settlement.
	publicField := projected.UsageSnapshot.Schema["seconds"]
	publicField.Max = 1
	projected.UsageSnapshot.Schema["seconds"] = publicField
	require.Equal(t, float64(3600), snapshot.Schema["seconds"].Max)
	delete(projected.UsageSnapshot.Schema, "seconds")
	require.Contains(t, snapshot.Schema, "seconds")
	require.Contains(t, TaskModel2Dto(task).UsageSnapshot.Schema, "seconds")

	// Both forms of legacy tasks keep their previous JSON shape.
	for _, legacy := range []*model.Task{
		{},
		{PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{}}},
	} {
		encoded, err := json.Marshal(TaskModel2Dto(legacy))
		require.NoError(t, err)
		var legacyFields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &legacyFields))
		require.NotContains(t, legacyFields, "usage_snapshot")
	}
}

func TestTaskModel2DtoDiscardedResultHidesResidualPayload(t *testing.T) {
	task := &model.Task{
		TaskID:     "discarded-task",
		Status:     model.TaskStatusSuccess,
		Quota:      1500,
		FailReason: "https://example.com/legacy-result.mp4",
		Data:       json.RawMessage(`{"result":"residual-output"}`),
		PrivateData: model.TaskPrivateData{
			ResultDiscarded: true,
			ResultURL:       "https://example.com/private-result.mp4",
			ResultMetadata:  &model.TaskResultMetadata{Text: "residual-metadata"},
			BillingContext: &model.TaskBillingContext{UsageSnapshot: &model.TaskUsageSnapshot{
				Name: "seconds", Version: "1", Expression: "seconds * 0.001",
				Schema: map[string]model.TaskUsageField{"seconds": {Path: "usage.seconds", Required: true, Max: 3600}},
			}},
		},
	}
	projected := TaskModel2Dto(task)
	require.True(t, projected.ResultDiscarded)
	require.Nil(t, projected.Data)
	require.Empty(t, projected.FailReason)
	require.Empty(t, projected.ResultURL)
	require.Equal(t, 1500, projected.Quota)
	require.NotNil(t, projected.UsageSnapshot)
	encoded, err := json.Marshal(projected)
	require.NoError(t, err)
	for _, residual := range []string{"residual-output", "residual-metadata", "legacy-result.mp4", "private-result.mp4"} {
		require.NotContains(t, string(encoded), residual)
	}
	// Projection cannot alter the record used by settlement or recovery.
	require.JSONEq(t, `{"result":"residual-output"}`, string(task.Data))
	require.Equal(t, "residual-metadata", task.PrivateData.ResultMetadata.Text)
}
