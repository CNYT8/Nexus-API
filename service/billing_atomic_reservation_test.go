package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func stopBillingTestLease(t *testing.T, session *BillingSession) {
	t.Helper()
	t.Cleanup(func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		session.stopLeaseLocked()
	})
}

func billingTestTokenFailure(t *testing.T, db *gorm.DB, tokenID int) {
	t.Helper()
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER fail_billing_token BEFORE UPDATE ON tokens WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'billing token write failed'); END", tokenID)).Error)
}

func TestBillingSessionWalletTokenFailureRecoversAtomically(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			// Accounting reads the persisted configuration inside its transaction;
			// the shared fixture's process-local setting alone cannot enable rebates.
			require.NoError(t, db.Create(&[]model.Option{
				{Key: "rebate_setting.enabled", Value: "true"},
				{Key: "rebate_setting.rate_basis_points", Value: "250"},
				{Key: "rebate_setting.payout_mode", Value: "immediate"},
			}).Error)
			if legacy {
				require.NoError(t, db.Where(map[string]interface{}{"key": "wallet_credit_baseline_v1"}).Delete(&model.RebateSystemState{}).Error)
				require.NoError(t, db.Where("user_id = ?", task.UserId).Delete(&model.WalletCreditLot{}).Error)
			}
			ctx, info := newWalletBillingTestContext(task.UserId, task.PrivateData.TokenId, "task-token", 10000)
			session, apiErr := NewBillingSession(ctx, info, 3000)
			require.Nil(t, apiErr)
			stopBillingTestLease(t, session)
			billingTestTokenFailure(t, db, task.PrivateData.TokenId)
			require.ErrorContains(t, session.Settle(4000), "billing token write failed")
			session.Refund(ctx) // Successful upstream usage must remain non-refundable.
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", session.WalletReservationKey()).First(&reservation).Error)
			require.Equal(t, model.WalletReservationFinalizing, reservation.State)
			require.EqualValues(t, 4000, reservation.FinalQuota)

			require.NoError(t, db.Exec("DROP TRIGGER fail_billing_token").Error)
			processed, err := model.RecoverFinalizingWalletSettlements(db, 10)
			require.NoError(t, err)
			require.Equal(t, 1, processed)
			require.NoError(t, session.Settle(4000))
			require.NoError(t, session.Settle(4000))
			require.Error(t, session.Settle(4001))
			require.Equal(t, 6000, getUserQuota(t, task.UserId))
			require.Equal(t, 6000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			processed, err = model.RecoverFinalizingWalletSettlements(db, 10)
			require.NoError(t, err)
			require.Zero(t, processed)
			expectedSettlements := int64(1)
			if legacy {
				expectedSettlements = 0
			}
			require.Equal(t, expectedSettlements, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			require.Equal(t, expectedSettlements, taskLedgerCount(t, db, &model.RebateRecord{}))
		})
	}
}

func TestBillingSessionTokenReserveFailureRollsBackFunding(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	billingTestTokenFailure(t, db, task.PrivateData.TokenId)
	ctx, info := newWalletBillingTestContext(task.UserId, task.PrivateData.TokenId, "task-token", 10000)
	info.UserSetting.BillingPreference = "wallet_first"
	session, apiErr := NewBillingSession(ctx, info, 3000)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodePreConsumeTokenQuotaFailed, apiErr.GetErrorCode())
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletReservation{}))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletLotHold{}))
}

func TestBillingSessionSubscriptionTokenFailureRecoversAtomically(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}))
	plan := model.SubscriptionPlan{Title: "atomic-billing", TotalAmount: 10000, QuotaResetPeriod: "never"}
	require.NoError(t, db.Create(&plan).Error)
	now := time.Now().Unix()
	sub := model.UserSubscription{UserId: task.UserId, PlanId: plan.Id, AmountTotal: 10000, Status: "active", StartTime: now - 60, EndTime: now + 3600, QuotaPeriodStart: now - 60}
	require.NoError(t, db.Create(&sub).Error)
	ctx, info := newWalletBillingTestContext(task.UserId, task.PrivateData.TokenId, "task-token", 10000)
	info.UserSetting.BillingPreference = "subscription_only"
	session, apiErr := NewBillingSession(ctx, info, 3000)
	require.Nil(t, apiErr)
	stopBillingTestLease(t, session)
	billingTestTokenFailure(t, db, task.PrivateData.TokenId)
	require.ErrorContains(t, session.Settle(4000), "billing token write failed")
	require.EqualValues(t, 3000, getSubscriptionUsed(t, sub.Id))
	require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.NoError(t, db.Exec("DROP TRIGGER fail_billing_token").Error)
	processed, err := model.RecoverFinalizingWalletSettlements(db, 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.NoError(t, session.Settle(4000))
	require.EqualValues(t, 4000, getSubscriptionUsed(t, sub.Id))
	require.Equal(t, 6000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestPendingBillingReservationExpiryRefundsWalletAndTokenOnce(t *testing.T) {
	for _, unlimited := range []bool{false, true} {
		t.Run(fmt.Sprintf("unlimited=%v", unlimited), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", task.PrivateData.TokenId).UpdateColumn("unlimited_quota", unlimited).Error)
			key := reservationTestKey(t, task.UserId, "crash-before-task-insert")
			require.NoError(t, model.ReserveBillingWalletQuota(db, key, task.UserId, task.PrivateData.TokenId, 3000))
			require.NoError(t, db.Model(&model.WalletReservation{}).Where("reservation_key = ?", key).UpdateColumn("updated_at", time.Now().Add(-96*time.Hour)).Error)
			billingTestTokenFailure(t, db, task.PrivateData.TokenId)
			count, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
			require.ErrorContains(t, err, "billing token write failed")
			require.Zero(t, count)
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			require.NoError(t, db.Exec("DROP TRIGGER fail_billing_token").Error)
			count, err = model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			require.NoError(t, model.RefundBillingReservation(db, key, task.UserId))
			require.Equal(t, 10000, getUserQuota(t, task.UserId))
			require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			var token model.Token
			require.NoError(t, db.First(&token, task.PrivateData.TokenId).Error)
			require.Zero(t, token.UsedQuota)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				if err := model.LockTaskBillingUserTx(tx, task.UserId); err != nil {
					return err
				}
				return model.BindWalletReservationTaskTx(tx, key, task.UserId, "task", task.ID)
			}), model.ErrWalletReservationConflict)
		})
	}
}

func TestBillingReservationLeasePreventsActiveRequestExpiry(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	key := reservationTestKey(t, task.UserId, "long-active-request")
	require.NoError(t, model.ReserveBillingWalletQuota(db, key, task.UserId, task.PrivateData.TokenId, 3000))
	old := time.Now().Add(-96 * time.Hour)
	require.NoError(t, db.Model(&model.WalletReservation{}).Where("reservation_key = ?", key).Updates(map[string]interface{}{"created_at": old, "updated_at": old}).Error)
	live, err := model.RenewBillingReservation(db, key, task.UserId)
	require.NoError(t, err)
	require.True(t, live)
	count, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, 7000, getUserQuota(t, task.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.NoError(t, model.RefundBillingReservation(db, key, task.UserId))
	live, err = model.RenewBillingReservation(db, key, task.UserId)
	require.NoError(t, err)
	require.False(t, live)
}

func TestWalletFundingLegacyReservationAcrossBaseline(t *testing.T) {
	for _, action := range []string{"settle", "grow", "refund"} {
		t.Run(action, func(t *testing.T) {
			db := setupWalletSettlementTestDB(t)
			user := model.User{Username: "legacy-cutover", Quota: 100}
			require.NoError(t, db.Create(&user).Error)
			funding := &WalletFunding{userId: user.Id, settlementKey: reservationTestKey(t, user.Id, action)}
			require.NoError(t, funding.PreConsume(30))
			require.Equal(t, 70, userQuota(t, user.Id))
			// Reconstruct an old request resuming after a historical cutover.
			// Current initialization rejects live legacy reservations.
			lotKey, err := model.NewWalletCreditEventKey(user.Id, model.WalletCreditSourceLegacy, "baseline", "cutover", "legacy_baseline")
			require.NoError(t, err)
			require.NoError(t, db.Create(&model.WalletCreditLot{CreditEventKey: lotKey, UserID: user.Id, Source: model.WalletCreditSourceLegacy, Component: "legacy_baseline", ReferenceType: "baseline", ReferenceID: "cutover", QuotaTotal: 70, QuotaRemaining: 70}).Error)
			require.NoError(t, db.Create(&model.RebateSystemState{Key: "wallet_credit_baseline_v1", Value: "initialized:cutover", UpdatedAt: time.Now().UTC()}).Error)
			expected := 70
			switch action {
			case "settle":
				require.NoError(t, funding.SettleUsage(30, 30))
				require.NoError(t, funding.SettleUsage(30, 30))
			case "grow":
				require.NoError(t, funding.ReserveMore(20))
				require.Equal(t, 50, userQuota(t, user.Id))
				require.NoError(t, funding.SettleUsage(50, 40))
				expected = 60
			case "refund":
				require.NoError(t, funding.Refund())
				require.NoError(t, funding.Refund())
				expected = 100
			}
			require.Equal(t, expected, userQuota(t, user.Id))
			var remaining int64
			require.NoError(t, db.Model(&model.WalletCreditLot{}).Where("user_id = ?", user.Id).Select("COALESCE(SUM(quota_remaining), 0)").Scan(&remaining).Error)
			require.EqualValues(t, expected, remaining)
			require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
			mismatches, _, _, err := model.ReconcileWalletCreditBatch(db, 0, 100)
			require.NoError(t, err)
			require.Empty(t, mismatches)
		})
	}
}

func TestTaskBillingUsesPersistedNoTokenIdentity(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	key := reservationTestKey(t, task.UserId, "playground-task")
	require.NoError(t, model.ReserveBillingWalletQuota(db, key, task.UserId, 0, 3000))
	task.PrivateData.BillingKey = key
	task.Quota = 3000
	require.NoError(t, db.Model(task).Updates(map[string]interface{}{"private_data": task.PrivateData, "quota": task.Quota}).Error)
	require.NoError(t, ReserveTaskWalletQuota(task, 3500))
	markTaskSuccess(t, db, task)
	applied, _, err := settleTaskWalletQuota(task, 4000, false)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 6000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
}
