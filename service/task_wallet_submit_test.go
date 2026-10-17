package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupPendingTaskWalletSubmitTest builds the wallet-ledger environment for the
// async task submit path: an inviter/invitee pair, paid wallet lots and a token
// with a finite quota. No task row is pre-inserted because the pending
// reservation must exist before the row.
func setupPendingTaskWalletSubmitTest(t *testing.T) (*gorm.DB, *model.User, *model.Token) {
	t.Helper()
	db := setupWalletSettlementTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.UserSubscription{}))
	originalSetting := operation_setting.GetRebateSetting()
	originalUnit, originalBatch := common.QuotaPerUnit, common.BatchUpdateEnabled
	common.QuotaPerUnit, common.BatchUpdateEnabled = 500000, false
	require.NoError(t, operation_setting.UpdateRebateSetting(operation_setting.RebateSetting{
		Enabled:            true,
		RateBasisPoints:    250,
		PayoutMode:         operation_setting.RebatePayoutImmediate,
		PayoutHour:         7,
		InviterLimitPeriod: operation_setting.RebateLimitPeriodMonthly,
		SiteTimezone:       "UTC",
	}))
	t.Cleanup(func() {
		common.QuotaPerUnit, common.BatchUpdateEnabled = originalUnit, originalBatch
		require.NoError(t, operation_setting.UpdateRebateSetting(originalSetting))
	})
	require.NoError(t, db.Create(&model.RebateSystemState{Key: "wallet_credit_baseline_v1", Value: "initialized:pending-submit-test", UpdatedAt: time.Now().UTC()}).Error)
	inviter := model.User{Username: "pending-submit-inviter", AffCode: "pending-submit-parent", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	user := model.User{Username: "pending-submit-user", AffCode: "pending-submit-child", InviterId: inviter.Id, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	credited, err := model.CreditTopUpTx(db, user.Id, "pending-submit-topup", 10000)
	require.NoError(t, err)
	require.True(t, credited)
	token := model.Token{UserId: user.Id, Key: "pending-submit-token", RemainQuota: 10000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	return db, &user, &token
}

func pendingTaskWalletHoldSum(t *testing.T, db *gorm.DB, key string) int64 {
	t.Helper()
	var held int64
	require.NoError(t, db.Model(&model.WalletLotHold{}).Where("reservation_key = ?", key).Select("COALESCE(SUM(quota_held),0)").Scan(&held).Error)
	return held
}

func insertPendingTaskRow(t *testing.T, db *gorm.DB, userID, tokenID, quota int, key string, status model.TaskStatus) *model.Task {
	t.Helper()
	task := makeTask(userID, 0, quota, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingKey = key
	task.Status = status
	require.NoError(t, db.Create(task).Error)
	return task
}

func TestTaskWalletBillingKeyUsesPersistedKeyFirst(t *testing.T) {
	persisted, err := model.NewWalletFinalSettlementKey(7, "task-submit", "durable-id")
	require.NoError(t, err)

	withPersisted := &model.Task{ID: 42, UserId: 7, PrivateData: model.TaskPrivateData{BillingKey: persisted}}
	key, err := TaskWalletBillingKey(withPersisted)
	require.NoError(t, err)
	require.Equal(t, persisted, key)

	legacy := &model.Task{ID: 42, UserId: 7}
	legacyKey, err := TaskWalletBillingKey(legacy)
	require.NoError(t, err)
	expected, err := model.NewWalletFinalSettlementKey(7, "task", "42")
	require.NoError(t, err)
	require.Equal(t, expected, legacyKey)
	require.NotEqual(t, persisted, legacyKey)

	// A persisted key is a durable identity even before a task row exists.
	noRow := &model.Task{UserId: 7, PrivateData: model.TaskPrivateData{BillingKey: persisted}}
	key, err = TaskWalletBillingKey(noRow)
	require.NoError(t, err)
	require.Equal(t, persisted, key)

	missing := &model.Task{UserId: 7}
	_, err = TaskWalletBillingKey(missing)
	require.ErrorIs(t, err, ErrWalletBillingIdentityRequired)
	_, err = TaskWalletBillingKey(nil)
	require.ErrorIs(t, err, ErrWalletBillingIdentityRequired)
}

func TestTaskWalletBillingKeyRejectsMalformedPersistedKey(t *testing.T) {
	valid, err := model.NewWalletFinalSettlementKey(7, "task-submit", "malformed-check")
	require.NoError(t, err)
	require.True(t, model.IsCanonicalWalletBillingKey(valid))
	malformed := []string{
		"not-a-key",
		"deadbeef",
		valid[:len(valid)-1],
		valid + "a",
		"ABCDEF" + valid[6:],
	}
	for _, candidate := range malformed {
		_, err := TaskWalletBillingKey(&model.Task{ID: 42, UserId: 7, PrivateData: model.TaskPrivateData{BillingKey: candidate}})
		require.ErrorIsf(t, err, ErrWalletBillingIdentityRequired, "key %q", candidate)
	}
}

func TestReservePendingTaskWalletQuotaLifecycle(t *testing.T) {
	db, user, _ := setupPendingTaskWalletSubmitTest(t)
	key, err := ReservePendingTaskWalletQuota(user.Id, 3000)
	require.NoError(t, err)
	require.True(t, model.IsCanonicalWalletBillingKey(key))
	require.Equal(t, 7000, getUserQuota(t, user.Id))
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ? AND user_id = ?", key, user.Id).First(&reservation).Error)
	require.Equal(t, model.WalletReservationReserved, reservation.State)
	require.EqualValues(t, 3000, reservation.ReservedQuota)
	require.EqualValues(t, 3000, pendingTaskWalletHoldSum(t, db, key))

	require.NoError(t, RefundPendingTaskWalletQuota(key, user.Id))
	require.Equal(t, 10000, getUserQuota(t, user.Id))
	require.NoError(t, db.First(&reservation, reservation.ID).Error)
	require.Equal(t, model.WalletReservationRefunded, reservation.State)
	require.EqualValues(t, 0, pendingTaskWalletHoldSum(t, db, key))
	// Repeated refund is a no-op.
	require.NoError(t, RefundPendingTaskWalletQuota(key, user.Id))
	require.Equal(t, 10000, getUserQuota(t, user.Id))

	// A finalizing or finalized reservation must conflict instead of refunding.
	finalKey, err := ReservePendingTaskWalletQuota(user.Id, 500)
	require.NoError(t, err)
	require.NoError(t, model.PrepareWalletFinalization(db, finalKey, user.Id, 500, 400))
	require.ErrorIs(t, RefundPendingTaskWalletQuota(finalKey, user.Id), model.ErrWalletReservationConflict)
	_, err = model.FinalizeWalletConsumption(db, finalKey, user.Id, 500, 400)
	require.NoError(t, err)
	require.ErrorIs(t, RefundPendingTaskWalletQuota(finalKey, user.Id), model.ErrWalletReservationConflict)
	require.Equal(t, 9600, getUserQuota(t, user.Id))

	require.ErrorIs(t, RefundPendingTaskWalletQuota("bad-key", user.Id), ErrWalletBillingIdentityRequired)
	require.ErrorIs(t, RefundPendingTaskWalletQuota(key, 0), ErrWalletBillingIdentityRequired)
}

func TestReserveTaskWalletQuotaShrinkAndGrow(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	key, err := TaskWalletBillingKey(task)
	require.NoError(t, err)

	require.NoError(t, ReserveTaskWalletQuota(task, 5000))
	require.Equal(t, 5000, getUserQuota(t, task.UserId))
	require.Equal(t, 5000, getTokenRemainQuota(t, task.PrivateData.TokenId))

	require.NoError(t, ReserveTaskWalletQuota(task, 2000))
	require.Equal(t, 8000, getUserQuota(t, task.UserId))
	require.Equal(t, 8000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Equal(t, 2000, task.Quota)
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.EqualValues(t, 2000, reservation.ReservedQuota)
	require.EqualValues(t, 2000, pendingTaskWalletHoldSum(t, db, key))

	require.NoError(t, ReserveTaskWalletQuota(task, 4500))
	require.Equal(t, 5500, getUserQuota(t, task.UserId))
	require.Equal(t, 5500, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Equal(t, 4500, task.Quota)

	// Shrinking to zero releases every hold and the whole token reserve.
	require.NoError(t, ReserveTaskWalletQuota(task, 0))
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Equal(t, 0, task.Quota)
	require.EqualValues(t, 0, pendingTaskWalletHoldSum(t, db, key))

	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	markTaskSuccess(t, db, task)
	RecalculateTaskQuota(context.Background(), task, 1800, "shrink settle")
	require.Equal(t, 8200, getUserQuota(t, task.UserId))
	require.Equal(t, 8200, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
	require.NoError(t, db.First(&reservation, reservation.ID).Error)
	require.Equal(t, model.WalletReservationFinalized, reservation.State)
	require.EqualValues(t, 3000, reservation.ReservedQuota)
	require.EqualValues(t, 1800, reservation.FinalQuota)
}

func TestPendingTaskWalletSubmitEndToEnd(t *testing.T) {
	db, user, token := setupPendingTaskWalletSubmitTest(t)
	key, err := ReservePendingTaskWalletQuota(user.Id, 3000)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key}
	require.NoError(t, PreConsumeTokenQuota(info, 3000))
	task := insertPendingTaskRow(t, db, user.Id, token.Id, 3000, key, model.TaskStatusInProgress)
	require.Equal(t, 7000, getUserQuota(t, user.Id))
	require.Equal(t, 7000, getTokenRemainQuota(t, token.Id))

	markTaskSuccess(t, db, task)
	RecalculateTaskQuota(context.Background(), task, 2500, "轮询完成结算")
	require.Equal(t, 7500, getUserQuota(t, user.Id))
	require.Equal(t, 7500, getTokenRemainQuota(t, token.Id))
	require.Equal(t, 2500, task.Quota)

	keyAfter, err := TaskWalletBillingKey(task)
	require.NoError(t, err)
	require.Equal(t, key, keyAfter)
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationFinalized, reservation.State)
	require.EqualValues(t, 2500, reservation.FinalQuota)
	var lot model.WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&lot).Error)
	require.EqualValues(t, 7500, lot.QuotaRemaining)
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))

	// Duplicate completion callbacks and stale snapshots must be idempotent.
	stale := *task
	RecalculateTaskQuota(context.Background(), &stale, 2500, "重复回调")
	require.Equal(t, 7500, getUserQuota(t, user.Id))
	require.Equal(t, 7500, getTokenRemainQuota(t, token.Id))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestPendingTaskWalletSubmitFailureRefund(t *testing.T) {
	db, user, token := setupPendingTaskWalletSubmitTest(t)
	key, err := ReservePendingTaskWalletQuota(user.Id, 3000)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key}
	require.NoError(t, PreConsumeTokenQuota(info, 3000))
	task := insertPendingTaskRow(t, db, user.Id, token.Id, 3000, key, model.TaskStatusInProgress)

	RefundTaskQuota(context.Background(), task, "提交失败退款")
	require.Equal(t, 10000, getUserQuota(t, user.Id))
	require.Equal(t, 10000, getTokenRemainQuota(t, token.Id))
	require.Equal(t, 0, task.Quota)
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationRefunded, reservation.State)
	require.EqualValues(t, 0, pendingTaskWalletHoldSum(t, db, key))
	var lot model.WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&lot).Error)
	require.EqualValues(t, 10000, lot.QuotaRemaining)
	require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))

	stale := *task
	RefundTaskQuota(context.Background(), &stale, "重复退款")
	require.NoError(t, RefundPendingTaskWalletQuota(key, user.Id))
	require.Equal(t, 10000, getUserQuota(t, user.Id))
	require.Equal(t, 10000, getTokenRemainQuota(t, token.Id))
}

func TestTaskWalletLedgerUsesSubscriptionMirrorsSession(t *testing.T) {
	db, user, _ := setupPendingTaskWalletSubmitTest(t)
	for pref, want := range map[string]bool{
		"wallet_only":  false,
		"wallet_first": false,
	} {
		info := &relaycommon.RelayInfo{UserId: user.Id, UserSetting: dto.UserSetting{BillingPreference: pref}}
		got, err := TaskWalletLedgerUsesSubscription(info)
		require.NoError(t, err)
		require.Equalf(t, want, got, "preference %s", pref)
	}
	info := &relaycommon.RelayInfo{UserId: user.Id, UserSetting: dto.UserSetting{BillingPreference: "subscription_only"}}
	got, err := TaskWalletLedgerUsesSubscription(info)
	require.NoError(t, err)
	require.True(t, got)

	// subscription_first/default depends on an active subscription.
	info.UserSetting.BillingPreference = "subscription_first"
	got, err = TaskWalletLedgerUsesSubscription(info)
	require.NoError(t, err)
	require.False(t, got)
	info.UserSetting.BillingPreference = ""
	got, err = TaskWalletLedgerUsesSubscription(info)
	require.NoError(t, err)
	require.False(t, got)

	require.NoError(t, db.Create(&model.UserSubscription{UserId: user.Id, AmountTotal: 100000, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Add(time.Hour).Unix()}).Error)
	info.UserSetting.BillingPreference = "subscription_first"
	got, err = TaskWalletLedgerUsesSubscription(info)
	require.NoError(t, err)
	require.True(t, got)
	info.UserSetting.BillingPreference = ""
	got, err = TaskWalletLedgerUsesSubscription(info)
	require.NoError(t, err)
	require.True(t, got)

	_, err = TaskWalletLedgerUsesSubscription(nil)
	require.Error(t, err)
}

func TestInactiveLedgerTaskBillingKeepsLegacyPath(t *testing.T) {
	db := setupWalletSettlementTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.UserSubscription{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	user := model.User{Username: "legacy-task-user", Quota: 10000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "legacy-task-token", RemainQuota: 10000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_legacy"},
	}
	require.Nil(t, PreConsumeBilling(ctx, 3000, info))
	require.NotNil(t, info.Billing)
	require.Equal(t, BillingSourceWallet, info.BillingSource)
	require.Equal(t, 7000, getUserQuota(t, user.Id))
	require.Equal(t, 7000, getTokenRemainQuota(t, token.Id))

	require.NoError(t, SettleBilling(ctx, info, 2500))
	require.Equal(t, 7500, getUserQuota(t, user.Id))
	require.Equal(t, 7500, getTokenRemainQuota(t, token.Id))
}

// TestLedgerGuardOnlyBlocksWalletTaskBilling documents the mismatch fixed by the
// task submit owner: PreConsumeBilling's wallet-ledger guard rejects every
// TaskRelayInfo, including subscriptions, so non-wallet sources must use
// NewBillingSession directly while wallet tasks use the pending reservation.
func TestLedgerGuardOnlyBlocksWalletTaskBilling(t *testing.T) {
	db, user, token := setupPendingTaskWalletSubmitTest(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPreConsumeRecord{}, &model.SubscriptionPlan{}))
	require.NoError(t, db.Create(&model.SubscriptionPlan{Id: 1, Title: "task-submit-plan", DurationUnit: "month", DurationValue: 1, Enabled: true}).Error)
	require.NoError(t, db.Create(&model.UserSubscription{UserId: user.Id, PlanId: 1, AmountTotal: 100000, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Add(time.Hour).Unix()}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       "task-subscription-request",
		OriginModelName: "test-model",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
		UserSetting:     dto.UserSetting{BillingPreference: "subscription_only"},
	}
	guardErr := PreConsumeBilling(ctx, 1000, info)
	require.NotNil(t, guardErr)
	require.ErrorIs(t, guardErr.Err, ErrWalletBillingIdentityRequired)
	require.Nil(t, info.Billing)

	session, apiErr := NewBillingSession(ctx, info, 1000)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, BillingSourceSubscription, info.BillingSource)
	require.Greater(t, info.SubscriptionId, 0)
	require.Equal(t, int64(1000), getSubscriptionUsed(t, info.SubscriptionId))
	// The subscription path never touches wallet quota.
	require.Equal(t, 10000, getUserQuota(t, user.Id))
}

func TestTaskWalletQuotaClampErrorMatchesPreConsumeBilling(t *testing.T) {
	require.Nil(t, TaskWalletQuotaClampError(nil))
	apiErr := TaskWalletQuotaClampError(&common.QuotaClamp{Op: "task", Kind: common.QuotaClampOverflow, Original: 1e18, Clamped: 2147483647})
	require.NotNil(t, apiErr)
	require.Equal(t, 400, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())
}
