package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/membership_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Only provider I/O is replaced. The real entry point still prices, persists
// the terminal CAS, and settles through the production wallet/token services.
type realtimeBillingAdaptor struct {
	result relaycommon.TaskInfo
	custom int
}

func (*realtimeBillingAdaptor) Init(*relaycommon.RelayInfo) {}
func (*realtimeBillingAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}
func (a *realtimeBillingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	result := a.result
	return &result, nil
}
func (a *realtimeBillingAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return a.custom
}
func (a *realtimeBillingAdaptor) factory(constant.TaskPlatform) service.TaskPollingAdaptor {
	return a
}

func setupRealtimeTaskBillingTest(t *testing.T, unlimited bool) (*gorm.DB, *model.Task, int) {
	t.Helper()
	initialToken := 10000
	if unlimited {
		initialToken = 0
	}
	db, user, token := setupMJPendingLedgerTest(t, true, initialToken)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.UserMembership{}, &model.UserGroupRatioOverride{}))
	originalRebate := operation_setting.GetRebateSetting()
	originalMembership := *membership_setting.GetMembershipSetting()
	originalModels := ratio_setting.ModelRatio2JSONString()
	originalGroups := ratio_setting.GroupRatio2JSONString()
	originalGroupGroups := ratio_setting.GroupGroupRatio2JSONString()
	originalThreshold, originalLog := common.QuotaRemindThreshold, common.LogConsumeEnabled
	t.Cleanup(func() {
		require.NoError(t, model.SetUserMembershipTier(user.Id, "", model.MembershipSourceManual))
		require.NoError(t, model.DeleteUserGroupRatioOverride(user.Id, "realtime-billing"))
		*membership_setting.GetMembershipSetting() = originalMembership
		require.NoError(t, operation_setting.UpdateRebateSetting(originalRebate))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModels))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalGroupGroups))
		common.QuotaRemindThreshold, common.LogConsumeEnabled = originalThreshold, originalLog
	})
	// Positive test balances and threshold zero prevent notification I/O.
	common.QuotaRemindThreshold, common.LogConsumeEnabled = 0, true
	*membership_setting.GetMembershipSetting() = membership_setting.MembershipSetting{
		Enabled: true,
		Tiers:   []membership_setting.Tier{{Id: "realtime-gold", Name: "Gold", Enabled: true, DiscountAllGroups: true, AllGroupDiscount: 0.5}},
	}
	require.NoError(t, model.SetUserMembershipTier(user.Id, "", model.MembershipSourceManual))
	require.NoError(t, model.DeleteUserGroupRatioOverride(user.Id, "realtime-billing"))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"realtime-billing-test":2}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"realtime-billing":1}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	require.NoError(t, operation_setting.UpdateRebateSetting(operation_setting.RebateSetting{
		Enabled: true, RateBasisPoints: 250, PayoutMode: operation_setting.RebatePayoutImmediate,
		PayoutHour: 7, InviterLimitPeriod: operation_setting.RebateLimitPeriodMonthly, SiteTimezone: "UTC",
	}))
	fields, err := config.ConfigToMap(operation_setting.GetRebateSetting())
	require.NoError(t, err)
	for key, value := range fields {
		require.NoError(t, db.Create(&model.Option{Key: "rebate_setting." + key, Value: value}).Error)
	}
	inviter := model.User{Username: "realtime-inviter", AffCode: "realtime-parent", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	require.NoError(t, db.Model(user).Updates(map[string]interface{}{"inviter_id": inviter.Id, "group": "realtime-billing"}).Error)
	require.NoError(t, db.Model(token).UpdateColumn("unlimited_quota", unlimited).Error)
	ch := model.Channel{Type: constant.ChannelTypeGemini, Key: "test-only", Name: "realtime-test"}
	require.NoError(t, db.Create(&ch).Error)
	task := &model.Task{
		TaskID: "realtime-task", UserId: user.Id, ChannelId: ch.Id,
		Status: model.TaskStatusInProgress, Progress: "50%", Group: "realtime-billing",
		PrivateData: model.TaskPrivateData{
			BillingSource: service.BillingSourceWallet, TokenId: token.Id,
			BillingContext: &model.TaskBillingContext{OriginModelName: "realtime-billing-test", OtherRatios: map[string]float64{"seconds": 1.5}},
		},
	}
	require.NoError(t, task.Insert())
	require.NoError(t, service.ReserveTaskWalletQuota(task, 5000))
	return db, task, initialToken
}

func assertRealtimeTaskBilling(t *testing.T, db *gorm.DB, task *model.Task, initialToken, actual int, failure bool) {
	t.Helper()
	var stored model.Task
	var token model.Token
	var reservation model.WalletReservation
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.NoError(t, db.First(&token, task.PrivateData.TokenId).Error)
	require.NoError(t, db.Where("reservation_key = ?", task.PrivateData.BillingKey).First(&reservation).Error)
	require.Equal(t, model.TaskBillingDone, stored.BillingState)
	require.Equal(t, actual, stored.BillingQuota)
	require.Equal(t, actual, stored.Quota)
	require.Equal(t, 10000-actual, mjSubmitUserQuota(t, db, task.UserId))
	require.Equal(t, initialToken-actual, token.RemainQuota)
	require.Equal(t, actual, token.UsedQuota)
	var settlements, rewards int64
	require.NoError(t, db.Model(&model.WalletConsumptionSettlement{}).Count(&settlements).Error)
	require.NoError(t, db.Model(&model.RebateRecord{}).Count(&rewards).Error)
	if failure {
		require.EqualValues(t, model.TaskStatusFailure, stored.Status)
		require.Equal(t, "provider rejected", stored.FailReason)
		require.Equal(t, model.WalletReservationRefunded, reservation.State)
		require.Zero(t, settlements)
		require.Zero(t, rewards)
	} else {
		require.EqualValues(t, model.TaskStatusSuccess, stored.Status)
		require.Equal(t, model.WalletReservationFinalized, reservation.State)
		require.EqualValues(t, actual, reservation.FinalQuota)
		require.EqualValues(t, 1, settlements)
		var settlement model.WalletConsumptionSettlement
		require.NoError(t, db.First(&settlement).Error)
		require.EqualValues(t, actual, settlement.WalletQuota)
		require.EqualValues(t, actual, settlement.EligibleQuotaConsumed)
		if actual == 0 {
			require.Zero(t, rewards)
		} else {
			require.EqualValues(t, 1, rewards)
			var reward model.RebateRecord
			require.NoError(t, db.First(&reward).Error)
			require.EqualValues(t, actual, reward.EligibleQuota)
			// 固定充值比例与 2.5% 返利下，每 40 额度产生 1 额度返利。
			require.EqualValues(t, actual/20, reward.RewardMoneyMicros)
			require.EqualValues(t, actual/40, reward.RewardQuota)
			require.Zero(t, reward.DiscardedRewardMoneyMicros)
			var inviter model.User
			require.NoError(t, db.First(&inviter, reward.InviterID).Error)
			if actual%40 != 0 {
				require.Equal(t, model.RebateStatusHeld, reward.Status)
				require.False(t, reward.HeldByClosure)
				require.Nil(t, reward.PaidAt)
				require.Nil(t, reward.PayoutLotID)
				require.Zero(t, inviter.Quota)
			} else {
				require.Equal(t, model.RebateStatusPaid, reward.Status)
				require.NotNil(t, reward.PaidAt)
				require.NotNil(t, reward.PayoutLotID)
				require.EqualValues(t, actual/40, inviter.Quota)
			}
		}
	}
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Count(&logs).Error)
	if actual == 5000 {
		require.Zero(t, logs)
	} else {
		require.EqualValues(t, 1, logs)
	}
	issues, _, _, err := model.ReconcileWalletCreditBatch(db, 0, 100)
	require.NoError(t, err)
	require.Empty(t, issues)
}

func TestRealtimeTaskBillingTerminalCAS(t *testing.T) {
	cases := []struct {
		name      string
		custom    int
		actual    int
		member    bool
		perCall   bool
		zeroRatio bool
		failure   bool
		openAI    bool
	}{
		{name: "token_difference", actual: 3000},
		{name: "member_token_difference", actual: 1500, member: true, openAI: true},
		{name: "custom_over_tokens", custom: 6500, actual: 6500},
		{name: "per_call_over_custom", custom: 6500, actual: 5000, perCall: true},
		{name: "zero_user_ratio", actual: 0, zeroRatio: true},
		{name: "failure_refunds", actual: 0, failure: true, openAI: true},
	}
	for _, unlimited := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/unlimited_%t", tc.name, unlimited), func(t *testing.T) {
				db, task, initialToken := setupRealtimeTaskBillingTest(t, unlimited)
				if tc.member {
					require.NoError(t, model.SetUserMembershipTier(task.UserId, "realtime-gold", model.MembershipSourceManual))
				}
				if tc.zeroRatio {
					require.NoError(t, model.SetUserGroupRatioOverride(task.UserId, task.Group, 0))
				}
				if tc.perCall {
					task.PrivateData.BillingContext.PerCallBilling = true
					require.NoError(t, db.Model(task).UpdateColumn("private_data", task.PrivateData).Error)
				}
				stale := *task
				adaptor := &realtimeBillingAdaptor{custom: tc.custom, result: relaycommon.TaskInfo{
					Status: model.TaskStatusSuccess, TotalTokens: 1000, Url: "https://example.invalid/result.mp4",
				}}
				if tc.failure {
					adaptor.result.Status, adaptor.result.Reason = model.TaskStatusFailure, "provider rejected"
				}
				body := tryRealtimeFetchWithAdaptor(task, tc.openAI, adaptor.factory)
				if tc.openAI {
					require.Empty(t, body)
				} else {
					require.NotEmpty(t, body)
				}
				assertRealtimeTaskBilling(t, db, task, initialToken, tc.actual, tc.failure)
				// A stale competing fetch must neither reprice nor refund the
				// winner, and its response object must reflect committed state.
				adaptor.result.Status = model.TaskStatusFailure
				if tc.failure {
					adaptor.result.Status = model.TaskStatusSuccess
				}
				require.Empty(t, tryRealtimeFetchWithAdaptor(&stale, false, adaptor.factory))
				require.Equal(t, task.Status, stale.Status)
				require.Equal(t, tc.actual, stale.Quota)
				n, err := service.RecoverTaskBilling(context.Background(), 10)
				require.NoError(t, err)
				require.Zero(t, n)
				assertRealtimeTaskBilling(t, db, task, initialToken, tc.actual, tc.failure)
			})
		}
	}
}

func TestRealtimeTaskBillingPersistsTargetBeforeSettlement(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure_%t", failure), func(t *testing.T) {
			db, task, initialToken := setupRealtimeTaskBillingTest(t, false)
			require.NoError(t, db.Exec("CREATE TRIGGER realtime_token_failure BEFORE UPDATE OF remain_quota ON tokens BEGIN SELECT RAISE(FAIL, 'realtime token failure'); END").Error)
			adaptor := &realtimeBillingAdaptor{result: relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 1000}}
			actual := 3000
			if failure {
				adaptor.result.Status, adaptor.result.Reason = model.TaskStatusFailure, "provider rejected"
				actual = 0
			}
			tryRealtimeFetchWithAdaptor(task, true, adaptor.factory)
			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.EqualValues(t, adaptor.result.Status, stored.Status)
			require.Equal(t, model.TaskBillingPending, stored.BillingState)
			require.Equal(t, actual, stored.BillingQuota)
			require.Equal(t, 5000, stored.Quota)
			require.Equal(t, 5000, mjSubmitUserQuota(t, db, task.UserId))
			require.Equal(t, initialToken-5000, mjSubmitTokenRemain(t, db, task.PrivateData.TokenId))
			var settlements, rewards int64
			require.NoError(t, db.Model(&model.WalletConsumptionSettlement{}).Count(&settlements).Error)
			require.NoError(t, db.Model(&model.RebateRecord{}).Count(&rewards).Error)
			require.Zero(t, settlements)
			require.Zero(t, rewards)
			require.NoError(t, db.Exec("DROP TRIGGER realtime_token_failure").Error)
			// Recovery consumes the durable target even after pricing changes.
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"realtime-billing-test":9}`))
			n, err := service.RecoverTaskBilling(context.Background(), 10)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			assertRealtimeTaskBilling(t, db, task, initialToken, actual, failure)
			n, err = service.RecoverTaskBilling(context.Background(), 10)
			require.NoError(t, err)
			require.Zero(t, n)
			assertRealtimeTaskBilling(t, db, task, initialToken, actual, failure)
		})
	}
}
