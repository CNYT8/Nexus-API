package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// TestRebateSecurityInviterPrerequisiteBlocksFarmRewards proves a throwaway
// inviter account that never paid cannot turn invitee spending into rewards,
// and that the block is auditable and reversible by a real payment.
func TestRebateSecurityInviterPrerequisiteBlocksFarmRewards(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	setting := rebateRewardSetting()
	setting.InviterMinPaidMoneyMicros = 1000 // 0.001 site money
	useRebateRewardSetting(t, setting)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-farm-blocked", 200, 100_000_000)

	var blocked RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&blocked).Error)
	require.Equal(t, RebateStatusVoided, blocked.Status)
	require.Equal(t, "inviter_min_paid_not_met", blocked.VoidReason)

	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)
	var payoutLots int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("user_id = ? AND source = ?", inviter.Id, WalletCreditSourceRebate).Count(&payoutLots).Error)
	require.Zero(t, payoutLots)
	var capBuckets int64
	require.NoError(t, db.Model(&RebateCapBucket{}).Where("owner_id = ?", inviter.Id).Count(&capBuckets).Error)
	require.Zero(t, capBuckets)

	// A single real top-up clears the prerequisite; later invitee spending pays.
	credited, err := CreditTopUpTx(db, inviter.Id, "trade-inviter-paid", 1000)
	require.NoError(t, err)
	require.True(t, credited)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	quotaBeforeReward := int64(inviterUser.Quota)
	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-farm-allowed", 200, 100_000_000)

	var allowed RebateRecord
	require.NoError(t, db.Where("settlement_key != ? AND invitee_id = ?", blocked.SettlementKey, invitee.Id).First(&allowed).Error)
	require.Equal(t, RebateStatusPaid, allowed.Status)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.EqualValues(t, quotaBeforeReward+allowed.RewardQuota, int64(inviterUser.Quota))
}

// TestRebateSecurityPayoutHoldDelaysImmediateMode proves the chargeback risk
// window prevents any reward balance from being paid before the hold elapses,
// then pays it exactly once when due.
func TestRebateSecurityPayoutHoldDelaysImmediateMode(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	setting := rebateRewardSetting()
	setting.PayoutHoldHours = 48
	useRebateRewardSetting(t, setting)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	before := time.Now().UTC()
	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-hold", 200, 100_000_000)

	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.Equal(t, RebateStatusPending, record.Status)
	require.False(t, record.HeldByClosure)
	require.True(t, record.AvailableAt.After(before.Add(47*time.Hour)), "available_at must respect the hold window")

	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)

	// Not due yet.
	paid, err := PayDueWalletRebates(db, before.Add(47*time.Hour), 10)
	require.NoError(t, err)
	require.Zero(t, paid)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)

	// Due exactly once after the hold.
	paid, err = PayDueWalletRebates(db, before.Add(49*time.Hour), 10)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.EqualValues(t, record.RewardQuota, int64(inviterUser.Quota))
	require.NoError(t, db.First(&record, record.ID).Error)
	require.Equal(t, RebateStatusPaid, record.Status)

	paid, err = PayDueWalletRebates(db, before.Add(72*time.Hour), 10)
	require.NoError(t, err)
	require.Zero(t, paid)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.EqualValues(t, record.RewardQuota, int64(inviterUser.Quota))
}

// TestRebateSecurityZeroHoldKeepsImmediatePayout confirms the security options
// do not change default behavior when disabled.
func TestRebateSecurityZeroHoldKeepsImmediatePayout(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000
	useRebateRewardSetting(t, rebateRewardSetting())

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-no-hold", 200, 100_000_000)
	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Positive(t, inviterUser.Quota)
}

// TestRebateSecuritySettingsValidateNewFields guards the option surface: the
// anti-farming prerequisite and payout hold must reject out-of-range values so
// an operator cannot accidentally disable the guard with a malformed value.
func TestRebateSecuritySettingsValidateNewFields(t *testing.T) {
	base := operation_setting.GetRebateSetting()
	require.NoError(t, operation_setting.ValidateRebateSetting(base))

	bad := base
	bad.InviterMinPaidMoneyMicros = -1
	require.Error(t, operation_setting.ValidateRebateSetting(bad))

	bad = base
	bad.PayoutHoldHours = 721
	require.Error(t, operation_setting.ValidateRebateSetting(bad))

	require.NoError(t, operation_setting.ValidateRebateSettingField("inviter_min_paid_money_micros", "5000000"))
	require.NoError(t, operation_setting.ValidateRebateSettingField("payout_hold_hours", "72"))
	require.Error(t, operation_setting.ValidateRebateSettingField("inviter_min_paid_money_micros", "-5"))
	require.Error(t, operation_setting.ValidateRebateSettingField("payout_hold_hours", "999"))
	require.Error(t, operation_setting.ValidateRebateSettingField("payout_hold_hours", "abc"))
}
