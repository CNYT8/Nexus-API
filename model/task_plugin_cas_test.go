package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateWithStatus_MergesScriptPollFields(t *testing.T) {
	for _, status := range []TaskStatus{TaskStatusInProgress, TaskStatusSuccess, TaskStatusFailure} {
		for _, fields := range []struct {
			name       string
			state      string
			usage      string
			usageError string
		}{
			{name: "zero_usage_and_cleared_error", state: `{"cursor":2}`, usage: `{"seconds":0}`},
			{name: "clear_all"},
			{name: "null_state_and_invalid_usage", state: "null", usage: "{}", usageError: "completion_usage_invalid"},
		} {
			t.Run(string(status)+"/"+fields.name, func(t *testing.T) {
				truncateTables(t)
				task := &Task{
					TaskID: "script-poll-merge", Status: TaskStatusInProgress, Progress: "20%", Quota: 3000,
					PrivateData: TaskPrivateData{
						Key: "provider-key", UpstreamTaskID: "upstream-task",
						ScriptPluginID: 7, ScriptPluginDigest: "frozen-revision",
						ScriptPluginState: `{"cursor":1}`, ScriptPluginUsage: `{"seconds":3}`,
						ScriptPluginUsageError: "old_error", PollFailures: 2, LastPollClass: "transport",
						BillingKey: "old-key", BillingSource: "wallet", TokenId: 12,
						BillingContext: &TaskBillingContext{ModelPrice: 1},
					},
					Data: json.RawMessage(`{"poll":1}`),
				}
				insertTask(t, task)
				poll := *task

				// Billing writes after the poll snapshot was read. Include a cleared
				// token identity: neither zero nor nonzero fields may be resurrected.
				current := *task
				current.Quota, current.BillingState, current.BillingQuota = 4500, TaskBillingPending, 4500
				current.PrivateData.BillingKey = "durable-key"
				current.PrivateData.BillingSource = "subscription"
				current.PrivateData.SubscriptionId = 9
				current.PrivateData.SubscriptionPeriodStart = 1700000000
				current.PrivateData.TokenId = 0
				current.PrivateData.BillingContext = &TaskBillingContext{
					ModelPrice: 2, GroupRatio: 0.8, OtherRatios: map[string]float64{"resolution": 2},
					UsageSnapshot: &TaskUsageSnapshot{PluginID: 11, Expression: "seconds * 0.01"},
				}
				current.PrivateData.PollFailures = 4
				require.NoError(t, DB.Model(task).Updates(map[string]any{
					"quota": current.Quota, "billing_state": current.BillingState,
					"billing_quota": current.BillingQuota, "private_data": current.PrivateData,
				}).Error)

				// Generic callers have no adapter write intent. Their zero-value
				// script fields must not clear the current row's private metadata.
				generic := *task
				generic.Progress = "30%"
				generic.PrivateData.ScriptPluginState = ""
				generic.PrivateData.ScriptPluginUsage = ""
				generic.PrivateData.ScriptPluginUsageError = ""
				won, err := generic.UpdateWithStatus(TaskStatusInProgress)
				require.NoError(t, err)
				require.True(t, won)
				require.Equal(t, current.PrivateData, generic.PrivateData)

				poll.Status, poll.Progress = status, "100%"
				poll.BillingState, poll.BillingQuota = TaskBillingPending, 1200
				poll.SetScriptPollMetadata(&fields.state, fields.usage, fields.usageError)
				poll.PrivateData.ResultURL = "https://example.com/final.mp4"
				poll.PrivateData.ResultMetadata = &TaskResultMetadata{Text: "final output"}
				poll.PrivateData.PollFailures, poll.PrivateData.LastPollClass = 0, "ok"
				poll.Data = json.RawMessage(`{"poll":2}`)
				won, err = poll.UpdateWithStatus(TaskStatusInProgress)
				require.NoError(t, err)
				require.True(t, won)

				var stored Task
				require.NoError(t, DB.First(&stored, task.ID).Error)
				expected := current.PrivateData
				expected.ScriptPluginState, expected.ScriptPluginUsage, expected.ScriptPluginUsageError = fields.state, fields.usage, fields.usageError
				expected.ResultURL, expected.ResultMetadata = poll.PrivateData.ResultURL, poll.PrivateData.ResultMetadata
				expected.PollFailures, expected.LastPollClass = 0, "ok"
				require.Equal(t, expected, stored.PrivateData, "only poll-owned private fields may change")
				require.Equal(t, expected, poll.PrivateData, "subsequent settlement must also see current billing identities")
				require.Equal(t, 4500, stored.Quota, "the stale reserved quota must not be restored")
				require.Equal(t, TaskBillingPending, stored.BillingState)
				wantTarget := 1200
				if status == TaskStatusInProgress {
					wantTarget = 4500
				} else if status == TaskStatusFailure {
					wantTarget = 0
				}
				require.Equal(t, wantTarget, stored.BillingQuota)
				require.Equal(t, status, stored.Status)
				require.JSONEq(t, `{"poll":2}`, string(stored.Data))
			})
		}
	}
}

func TestUpdateWithStatus_ScriptPollCannotOverwriteSettledTerminalTask(t *testing.T) {
	for _, terminal := range []TaskStatus{TaskStatusSuccess, TaskStatusFailure} {
		t.Run(string(terminal), func(t *testing.T) {
			truncateTables(t)
			task := &Task{
				TaskID: "script-terminal-race", Status: TaskStatusInProgress, Quota: 3000,
				PrivateData: TaskPrivateData{
					BillingKey: "reservation", TokenId: 12,
					ScriptPluginID: 7, ScriptPluginDigest: "revision",
					ScriptPluginState: `{"cursor":1}`, ScriptPluginUsage: `{"seconds":3}`,
				},
			}
			insertTask(t, task)
			stale := *task
			task.Status, task.Progress = terminal, "100%"
			task.BillingState, task.BillingQuota = TaskBillingPending, 1500
			state := "null"
			task.SetScriptPollMetadata(&state, `{"seconds":1.5}`, "")
			won, err := task.UpdateWithStatus(TaskStatusInProgress)
			require.NoError(t, err)
			require.True(t, won)

			// Reproduce settlement winning before a late poll attempts its CAS.
			task.Quota, task.BillingState = task.BillingQuota, TaskBillingDone
			task.PrivateData.TokenId = 0
			require.NoError(t, DB.Model(task).Updates(map[string]any{
				"quota": task.Quota, "billing_state": task.BillingState, "private_data": task.PrivateData,
			}).Error)
			var settled Task
			require.NoError(t, DB.First(&settled, task.ID).Error)
			for _, fromStatus := range []TaskStatus{TaskStatusInProgress, terminal} {
				for _, billingState := range []string{TaskBillingPending, TaskBillingSkipped} {
					late := stale
					late.Status, late.Progress = TaskStatusSuccess, "100%"
					late.BillingState, late.BillingQuota = billingState, 9000
					lateState := `{"late":true}`
					late.SetScriptPollMetadata(&lateState, `{"seconds":9}`, "late_error")
					won, err = late.UpdateWithStatus(fromStatus)
					require.NoError(t, err)
					require.False(t, won, "terminal tasks remain immutable even with a matching fromStatus")
					var stored Task
					require.NoError(t, DB.First(&stored, task.ID).Error)
					require.Equal(t, settled, stored, "late polls must not reset settlement or plugin facts")
				}
			}
		})
	}
}
