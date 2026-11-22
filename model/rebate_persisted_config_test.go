package model

import (
	"sort"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// persistRebateTestSetting seeds the committed configuration independently of
// the process cache, so tests can also model stale caches on another instance.
func persistRebateTestSetting(t *testing.T, db *gorm.DB, setting operation_setting.RebateSetting) {
	t.Helper()
	if setting.PayoutIntervalHours == 0 {
		setting.PayoutIntervalHours = 24
	}
	if setting.SiteTimezone == "" {
		setting.SiteTimezone = "UTC"
	}
	require.NoError(t, operation_setting.ValidateRebateSetting(setting))
	snapshot, err := rebateConfigSnapshot(setting)
	require.NoError(t, err)
	keys := make([]string, 0, len(snapshot.Fields))
	for key := range snapshot.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, key := range keys {
			option := Option{Key: "rebate_setting." + key, Value: snapshot.Fields[key]}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).Create(&option).Error; err != nil {
				return err
			}
		}
		return nil
	}))
}

func TestRebateGenerationAndSummaryUseCommittedConfig(t *testing.T) {
	stale := rebateRewardSetting()
	stale.Enabled = false
	useRebateRewardSetting(t, stale)
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	committed := rebateRewardSetting()
	committed.RateBasisPoints = 750
	committed.PayoutHoldHours = 24
	committed.InviterLimitPeriod = operation_setting.RebateLimitPeriodDaily
	committed.InviterEligibleMoneyLimit = 200_000_000
	persistRebateTestSetting(t, db, committed)

	settleRebateEligibleUsage(t, db, invitee.Id, "persisted-config-enabled", 200, 100_000_000)
	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.EqualValues(t, 750, record.RateBasisPoints)
	require.EqualValues(t, 7_500_000, record.RewardMoneyMicros)
	require.Equal(t, RebateStatusPending, record.Status)
	require.False(t, operation_setting.GetRebateSetting().Enabled)
	summary, err := GetWalletRebateSummary(db, inviter.Id)
	require.NoError(t, err)
	require.True(t, summary.Enabled)
	require.EqualValues(t, 750, summary.RateBasisPoints)
	require.Equal(t, operation_setting.RebateLimitPeriodDaily, summary.InviterLimitPeriod)
	require.EqualValues(t, 200_000_000, summary.InviterEligibleMoneyLimitMicros)
	require.EqualValues(t, 100_000_000, summary.InviterPeriodEligibleMoneyMicros)

	// Another instance closes rebates while this process still thinks they
	// are enabled. New generation and the summary must both observe closure.
	useRebateRewardSetting(t, committed)
	committed.Enabled = false
	persistRebateTestSetting(t, db, committed)
	settleRebateEligibleUsage(t, db, invitee.Id, "persisted-config-disabled", 200, 100_000_000)
	var count int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	summary, err = GetWalletRebateSummary(db, inviter.Id)
	require.NoError(t, err)
	require.False(t, summary.Enabled)
}

func TestRebatePayoutUsesCommittedClosureConfig(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	committed := rebateRewardSetting()
	committed.Enabled = false
	committed.CountDuringClosure = true
	persistRebateTestSetting(t, db, committed)
	old := seedRebatePayout(t, db, inviter.Id, invitee.Id, 10, RebateStatusPending, false)
	held := seedRebatePayout(t, db, inviter.Id, invitee.Id, 20, RebateStatusHeld, true)
	paid, err := PayDueWalletRebates(db, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&old, old.ID).Error)
	require.Equal(t, RebateStatusPaid, old.Status)
	require.NoError(t, db.First(&held, held.ID).Error)
	require.Equal(t, RebateStatusHeld, held.Status)

	// Reopening on another instance releases closure holds, even while this
	// process still caches disabled. Original amounts remain unchanged.
	useRebateRewardSetting(t, committed)
	committed.Enabled = true
	committed.RateBasisPoints = 9000
	persistRebateTestSetting(t, db, committed)
	paid, err = PayDueWalletRebates(db, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&held, held.ID).Error)
	require.Equal(t, RebateStatusPaid, held.Status)
	require.EqualValues(t, 20, held.RewardQuota)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, 30, inviter.Quota)
}

func TestRebateConfigReadFailureRollsBackGenerationAndBlocksPayoutAndSummary(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	key, err := NewWalletCreditEventKey(invitee.Id, WalletCreditSourceRedemption, "redemption", "config-read-failure", "redeemed")
	require.NoError(t, err)
	creditRebateTestLot(t, db, WalletCreditLot{
		CreditEventKey: key, UserID: invitee.Id, Source: WalletCreditSourceRedemption,
		Component: "redeemed", Eligible: true, ReferenceType: "redemption", ReferenceID: "config-read-failure",
		QuotaTotal: 100, QuotaRemaining: 100, EligibleMoneyMicros: 200, EligibleMoneyRemaining: 200,
		QuotaPerUnitMicros: 500_000 * MoneyMicrosPerUnit,
	})
	require.NoError(t, db.Migrator().DropTable(&Option{}))
	settlementKey, err := NewWalletFinalSettlementKey(invitee.Id, "relay", "config-read-failure")
	require.NoError(t, err)
	_, err = FinalizeWalletConsumption(db, settlementKey, invitee.Id, 0, 100)
	require.Error(t, err)
	for _, table := range []interface{}{&WalletConsumptionSettlement{}, &WalletConsumptionAllocation{}, &RebateRecord{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, db.First(&invitee, invitee.Id).Error)
	require.Equal(t, 100, invitee.Quota)
	var lot WalletCreditLot
	require.NoError(t, db.Where("credit_event_key = ?", key).First(&lot).Error)
	require.EqualValues(t, 100, lot.QuotaRemaining)

	pending := seedRebatePayout(t, db, inviter.Id, invitee.Id, 20, RebateStatusPending, false)
	paid, err := PayDueWalletRebates(db, time.Now().UTC(), 10)
	require.Error(t, err)
	require.Zero(t, paid)
	require.NoError(t, db.First(&pending, pending.ID).Error)
	require.Equal(t, RebateStatusPending, pending.Status)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Zero(t, inviter.Quota)
	summary, err := GetWalletRebateSummary(db, inviter.Id)
	require.Error(t, err)
	require.Nil(t, summary)
}
