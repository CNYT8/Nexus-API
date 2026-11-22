package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPersistSubmittedTaskRetainResult(t *testing.T) {
	retain, discard := true, false
	for _, tc := range []struct {
		name          string
		policy        *bool
		status        model.TaskStatus
		invalidUsage  bool
		wantDiscarded bool
		wantQuota     int
		wantState     string
	}{
		{name: "success_discards_after_usage", policy: &discard, status: model.TaskStatusSuccess, wantDiscarded: true, wantQuota: 1500, wantState: model.WalletReservationFinalized},
		{name: "failure_discards_and_refunds", policy: &discard, status: model.TaskStatusFailure, wantDiscarded: true, wantQuota: 0, wantState: model.WalletReservationRefunded},
		{name: "invalid_usage_keeps_reserved_quota", policy: &discard, status: model.TaskStatusSuccess, invalidUsage: true, wantDiscarded: true, wantQuota: 3000, wantState: model.WalletReservationFinalized},
		{name: "async_retains", policy: &discard, wantQuota: 3000, wantState: model.WalletReservationReserved},
		{name: "default_retains", status: model.TaskStatusSuccess, wantQuota: 1500, wantState: model.WalletReservationFinalized},
		{name: "explicit_true_retains", policy: &retain, status: model.TaskStatusSuccess, wantQuota: 1500, wantState: model.WalletReservationFinalized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, token := setupSubmittedTaskWalletTest(t)
			key, err := service.ReservePendingTaskWalletQuota(user.Id, 3000)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{
				UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
				BillingSource: service.BillingSourceWallet, OriginModelName: "retention-model", UsingGroup: "default",
				ChannelMeta:   &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeGemini, ApiKey: "private-provider-key"},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{},
			}
			require.NoError(t, service.PreConsumeTokenQuota(info, 3000))
			plugin := &model.TaskPlugin{
				Name: "retention-test", Version: "1",
				Schema: `{"seconds":{"path":"usage.seconds","required":true,"max":3600}}`, Expression: "seconds * 0.001",
			}
			if tc.policy != nil {
				value := *tc.policy
				plugin.RetainResult = &value
			}
			snapshot, err := service.NewTaskUsageSnapshot(plugin, 1, nil)
			require.NoError(t, err)
			snapshot.QuotaPerUnit = 500000
			if plugin.RetainResult != nil {
				*plugin.RetainResult = !*plugin.RetainResult
				require.Equal(t, *tc.policy, *snapshot.RetainResult, "submit policy must be an independent frozen copy")
			}
			raw := []byte(`{"usage":{"seconds":3},"result_text":"immediate-output","artifacts":[{"kind":"video","url":"https://example.com/output.mp4"}]}`)
			if tc.invalidUsage {
				raw = []byte(`{"result_text":"immediate-output"}`)
			}
			result := &relay.TaskSubmitResult{
				Platform: constant.TaskPlatform("test"), Quota: 3000, TaskData: raw,
				UpstreamTaskID: "private-upstream-id", UsageSnapshot: snapshot, InstantStatus: tc.status,
				ResultMetadata: service.CollectTaskResultMetadata(raw, "https://example.com/output.mp4"),
			}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			ctx.Set(service.ContextKeyTaskWalletBillingKey, key)
			ctx.Set(service.ContextKeyTaskWalletBillingQuota, 3000)
			// Static adapters write the immediate response before persistence.
			ctx.Data(http.StatusOK, "application/json", raw)
			task := persistSubmittedTask(ctx, info, result)
			require.NotNil(t, task)
			require.Equal(t, string(raw), response.Body.String())
			require.Equal(t, raw, result.TaskData)
			require.Equal(t, "immediate-output", result.ResultMetadata.Text)

			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			require.Equal(t, tc.wantQuota, stored.Quota)
			require.Equal(t, key, stored.PrivateData.BillingKey)
			require.Equal(t, token.Id, stored.PrivateData.TokenId)
			require.Equal(t, service.BillingSourceWallet, stored.PrivateData.BillingSource)
			if tc.status != "" {
				require.Equal(t, model.TaskBillingDone, stored.BillingState)
				require.Equal(t, tc.wantQuota, stored.BillingQuota)
				// Retrying settlement cannot charge or refund a second time.
				settleTerminalSubmittedTask(ctx, task)
				settleTerminalSubmittedTask(ctx, &stored)
			}
			require.Equal(t, 10000-tc.wantQuota, submittedTaskUserQuota(t, db, user.Id))
			require.Equal(t, 10000-tc.wantQuota, submittedTaskTokenRemain(t, db, token.Id))
			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
			require.Equal(t, tc.wantState, reservation.State)

			found, exists, err := model.GetByTaskId(user.Id, stored.TaskID)
			require.NoError(t, err)
			require.Equal(t, !tc.wantDiscarded, exists)
			_, exists, err = model.GetByOnlyTaskId(stored.TaskID)
			require.NoError(t, err)
			require.Equal(t, !tc.wantDiscarded, exists)
			batch, err := model.GetByTaskIds(user.Id, []any{stored.TaskID})
			require.NoError(t, err)
			if !tc.wantDiscarded {
				require.NotNil(t, found)
				require.Len(t, batch, 1)
				require.JSONEq(t, string(raw), string(stored.Data))
				require.NotNil(t, stored.PrivateData.ResultMetadata)
				if tc.status == "" {
					// Later asynchronous completion is never a submit-time discard.
					stored.Status, stored.BillingState, stored.BillingQuota = model.TaskStatusSuccess, model.TaskBillingPending, 1500
					require.False(t, service.ApplyImmediateTaskResultRetention(&stored))
					require.JSONEq(t, string(raw), string(stored.Data))
				}
				return
			}

			require.Nil(t, found)
			require.Empty(t, batch)
			require.Empty(t, stored.Data)
			require.Empty(t, stored.FailReason)
			require.Empty(t, stored.PrivateData.ResultURL)
			require.Nil(t, stored.PrivateData.ResultMetadata)
			require.Empty(t, stored.PrivateData.Key)
			require.Empty(t, stored.PrivateData.UpstreamTaskID)
			quota, _, handled := service.TaskUsageBillingTarget(&stored)
			require.True(t, handled)
			require.Equal(t, tc.wantQuota, quota, "discarded raw usage must not cause repricing")
			var decoded any
			require.ErrorIs(t, stored.GetData(&decoded), model.ErrTaskResultDiscarded)
			require.Empty(t, stored.GetResultURL())
			require.Empty(t, stored.GetUpstreamTaskID())

			for _, handler := range []gin.HandlerFunc{GetTaskArtifacts, VideoProxy} {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+stored.TaskID+"/content", nil)
				c.Set("id", user.Id)
				c.Params = gin.Params{{Key: "task_id", Value: stored.TaskID}}
				handler(c)
				require.Equal(t, http.StatusNotFound, w.Code, "result read must stop before provider access")
			}
			// History remains visible, with no result or private billing data.
			history := model.TaskGetAllUserTask(user.Id, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, history, 1)
			public := relay.TaskModel2Dto(history[0])
			require.True(t, public.ResultDiscarded)
			require.Nil(t, public.Data)
			require.Empty(t, public.ResultURL)
			require.Empty(t, public.FailReason)
			require.NotNil(t, public.UsageSnapshot)
			encoded, err := json.Marshal(public)
			require.NoError(t, err)
			for _, secret := range []string{"immediate-output", "output.mp4", key, "private-provider-key", "private-upstream-id", "billing_quota", "retain_result"} {
				require.NotContains(t, string(encoded), secret)
			}
		})
	}
}
