package controller

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupMidjourneyLedgerTest builds an in-memory wallet ledger with an
// invitee/inviter pair so successful consumption can produce exactly one
// rebate record.
func setupMidjourneyLedgerTest(t *testing.T) (*gorm.DB, *model.User, *model.Token) {
	t.Helper()
	env := setupTopUpQuotaTestEnv(t)
	db := env.db
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}, &model.Channel{}, &model.Midjourney{}))

	originalSetting := operation_setting.GetRebateSetting()
	originalBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	require.NoError(t, operation_setting.UpdateRebateSetting(operation_setting.RebateSetting{
		Enabled:            true,
		RateBasisPoints:    250,
		PayoutMode:         operation_setting.RebatePayoutImmediate,
		PayoutHour:         7,
		InviterLimitPeriod: operation_setting.RebateLimitPeriodMonthly,
		SiteTimezone:       "UTC",
	}))
	t.Cleanup(func() {
		common.BatchUpdateEnabled = originalBatch
		require.NoError(t, operation_setting.UpdateRebateSetting(originalSetting))
	})

	require.NoError(t, db.Create(&model.RebateSystemState{
		Key:       "wallet_credit_baseline_v1",
		Value:     "initialized:mj-ledger-test",
		UpdatedAt: time.Now().UTC(),
	}).Error)
	inviter := model.User{Username: "mj-ledger-inviter", AffCode: "mj-ledger-parent", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	user := model.User{Username: "mj-ledger-user", AffCode: "mj-ledger-child", InviterId: inviter.Id, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	credited, err := model.CreditTopUpTx(db, user.Id, "mj-ledger-topup", 10000)
	require.NoError(t, err)
	require.True(t, credited)
	token := model.Token{UserId: user.Id, Key: "mj-ledger-token", RemainQuota: 10000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	return db, &user, &token
}

func midjourneyUserQuota(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var user model.User
	require.NoError(t, db.Select("quota").First(&user, userID).Error)
	return user.Quota
}

func midjourneyLotRemaining(t *testing.T, db *gorm.DB, userID int) int64 {
	t.Helper()
	var lot model.WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", userID).First(&lot).Error)
	return lot.QuotaRemaining
}

func midjourneyLedgerCount(t *testing.T, db *gorm.DB, value interface{}) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(value).Count(&count).Error)
	return count
}

func TestSettleMidjourneyTaskBillingLedgerFailureRefunds(t *testing.T) {
	db, user, _ := setupMidjourneyLedgerTest(t)
	key, err := service.ReservePendingTaskWalletQuota(user.Id, 3000)
	require.NoError(t, err)
	require.Equal(t, 7000, midjourneyUserQuota(t, db, user.Id))

	task := &model.Midjourney{
		UserId:     user.Id,
		Quota:      3000,
		BillingKey: key,
		Action:     constant.MjActionImagine,
		MjId:       "mj-ledger-failure",
		ChannelId:  1,
	}
	settleMidjourneyTaskBilling(context.Background(), task, false)

	require.Equal(t, 10000, midjourneyUserQuota(t, db, user.Id))
	require.EqualValues(t, 10000, midjourneyLotRemaining(t, db, user.Id))
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationRefunded, reservation.State)
	require.Zero(t, midjourneyLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.Zero(t, midjourneyLedgerCount(t, db, &model.RebateRecord{}))
	var held int64
	require.NoError(t, db.Model(&model.WalletLotHold{}).Where("reservation_key = ?", key).
		Select("COALESCE(SUM(quota_held),0)").Scan(&held).Error)
	require.Zero(t, held)

	// A duplicate terminal callback must not refund twice.
	settleMidjourneyTaskBilling(context.Background(), task, false)
	require.Equal(t, 10000, midjourneyUserQuota(t, db, user.Id))
}

func TestSettleMidjourneyTaskBillingLedgerSuccessFinalizes(t *testing.T) {
	db, user, _ := setupMidjourneyLedgerTest(t)
	key, err := service.ReservePendingTaskWalletQuota(user.Id, 3000)
	require.NoError(t, err)

	task := &model.Midjourney{
		UserId:     user.Id,
		Quota:      3000,
		BillingKey: key,
		Action:     constant.MjActionImagine,
		MjId:       "mj-ledger-success",
		ChannelId:  1,
	}
	settleMidjourneyTaskBilling(context.Background(), task, true)

	require.Equal(t, 7000, midjourneyUserQuota(t, db, user.Id))
	require.EqualValues(t, 7000, midjourneyLotRemaining(t, db, user.Id))
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationFinalized, reservation.State)
	require.EqualValues(t, 3000, reservation.ReservedQuota)
	require.EqualValues(t, 3000, reservation.FinalQuota)
	require.EqualValues(t, 1, midjourneyLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, midjourneyLedgerCount(t, db, &model.RebateRecord{}))

	// A duplicate success callback must not consume or reward twice.
	settleMidjourneyTaskBilling(context.Background(), task, true)
	require.Equal(t, 7000, midjourneyUserQuota(t, db, user.Id))
	require.EqualValues(t, 1, midjourneyLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, midjourneyLedgerCount(t, db, &model.RebateRecord{}))
}

func TestSettleMidjourneyTaskBillingLegacyFailureKeepsRefundPath(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	db := env.db
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Midjourney{}))

	originalBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = originalBatch })

	user := model.User{Username: "mj-legacy-user", AffCode: "mj-legacy-aff", Quota: 5000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	task := &model.Midjourney{
		UserId:    user.Id,
		Quota:     3000,
		Action:    constant.MjActionImagine,
		MjId:      "mj-legacy-failure",
		ChannelId: 1,
	}
	settleMidjourneyTaskBilling(context.Background(), task, false)
	require.Equal(t, 8000, midjourneyUserQuota(t, db, user.Id))

	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Find(&logs).Error)
	require.Len(t, logs, 1)
	require.EqualValues(t, 3000, logs[0].Quota)
	require.Contains(t, logs[0].ModelName, "imagine")

	// Legacy success keeps the historical no-op behavior.
	settleMidjourneyTaskBilling(context.Background(), task, true)
	require.Equal(t, 8000, midjourneyUserQuota(t, db, user.Id))
}
