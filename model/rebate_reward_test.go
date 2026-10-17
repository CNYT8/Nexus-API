package model

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useRebateRewardSetting(t *testing.T, setting operation_setting.RebateSetting) {
	t.Helper()
	original := operation_setting.GetRebateSetting()
	require.NoError(t, operation_setting.UpdateRebateSetting(setting))
	t.Cleanup(func() {
		require.NoError(t, operation_setting.UpdateRebateSetting(original))
	})
}

func createRebateInvitation(t *testing.T, db *gorm.DB) (*User, *User) {
	t.Helper()
	inviter := createRebateTestUser(t, db, 0)
	invitee := createRebateTestUser(t, db, 0)
	require.NoError(t, db.Model(&User{}).Where("id = ?", invitee.Id).Update("inviter_id", inviter.Id).Error)
	invitee.InviterId = inviter.Id
	return inviter, invitee
}

func rebateRewardSetting() operation_setting.RebateSetting {
	return operation_setting.RebateSetting{
		Enabled:            true,
		RateBasisPoints:    250,
		PayoutMode:         operation_setting.RebatePayoutImmediate,
		PayoutHour:         7,
		PayoutMinute:       0,
		InviterLimitPeriod: operation_setting.RebateLimitPeriodMonthly,
		SiteTimezone:       "UTC",
	}
}

// settleRebateEligibleUsage credits one eligible redemption lot and settles it
// as fully consumed eligible usage, returning the settlement result.
func settleRebateEligibleUsage(t *testing.T, db *gorm.DB, userID int, reference string, quota, moneyMicros int64) *WalletSettlementResult {
	t.Helper()
	key, err := NewWalletCreditEventKey(userID, WalletCreditSourceRedemption, "redemption", reference, "redeemed")
	require.NoError(t, err)
	creditRebateTestLot(t, db, WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 userID,
		Source:                 WalletCreditSourceRedemption,
		Component:              "redeemed",
		Eligible:               true,
		ReferenceType:          "redemption",
		ReferenceID:            reference,
		QuotaTotal:             quota,
		QuotaRemaining:         quota,
		EligibleMoneyMicros:    moneyMicros,
		EligibleMoneyRemaining: moneyMicros,
		QuotaPerUnitMicros:     500_000 * MoneyMicrosPerUnit,
	})
	// Simulate the reservation of the whole balance, then settle at zero delta.
	require.NoError(t, db.Model(&User{}).Where("id = ?", userID).Update("quota", 0).Error)
	settlementKey, err := NewWalletFinalSettlementKey(userID, "relay", reference)
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(db, settlementKey, userID, quota, quota)
	require.NoError(t, err)
	return result
}

func TestRecordWalletRebateImmediatePayoutIsAtomicAndIdempotent(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000
	useRebateRewardSetting(t, rebateRewardSetting())

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	result := settleRebateEligibleUsage(t, db, invitee.Id, "rebate-immediate", 200, 100_000_000)
	require.EqualValues(t, 100_000_000, result.EligibleMoneyConsumedMicros)

	var updatedInviter User
	require.NoError(t, db.First(&updatedInviter, inviter.Id).Error)
	require.Equal(t, 1_250_000, updatedInviter.Quota)

	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.Equal(t, RebateStatusPaid, record.Status)
	require.EqualValues(t, 2_500_000, record.RewardMoneyMicros)
	require.EqualValues(t, 1_250_000, record.RewardQuota)
	require.Zero(t, record.DiscardedRewardMoneyMicros)
	require.NotNil(t, record.PayoutLotID)
	require.NotNil(t, record.PaidAt)

	var payoutLot WalletCreditLot
	require.NoError(t, db.Where("id = ?", *record.PayoutLotID).First(&payoutLot).Error)
	require.Equal(t, WalletCreditSourceRebate, payoutLot.Source)
	require.False(t, payoutLot.Eligible)
	require.EqualValues(t, 1_250_000, payoutLot.QuotaRemaining)

	// The record is unique per settlement and a retry never pays twice.
	retry := settleRebateEligibleUsage2(t, db, invitee.Id, "rebate-immediate", 200, 100_000_000)
	require.EqualValues(t, result.Settlement.ID, retry.Settlement.ID)
	var recordCount int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&recordCount).Error)
	require.EqualValues(t, 1, recordCount)
	require.NoError(t, db.First(&updatedInviter, inviter.Id).Error)
	require.Equal(t, 1_250_000, updatedInviter.Quota)
}

// settleRebateEligibleUsage2 retries the exact same settlement identity.
func settleRebateEligibleUsage2(t *testing.T, db *gorm.DB, userID int, reference string, quota, moneyMicros int64) *WalletSettlementResult {
	t.Helper()
	settlementKey, err := NewWalletFinalSettlementKey(userID, "relay", reference)
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(db, settlementKey, userID, quota, quota)
	require.NoError(t, err)
	return result
}

func TestRecordWalletRebateInviteeLifetimeCapDiscardsExcess(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	setting := rebateRewardSetting()
	setting.InviteeEligibleMoneyLimit = 40_000_000
	useRebateRewardSetting(t, setting)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-invitee-cap", 200, 100_000_000)

	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.EqualValues(t, 1_000_000, record.RewardMoneyMicros)
	require.EqualValues(t, 500_000, record.RewardQuota)
	require.EqualValues(t, 1_500_000, record.DiscardedRewardMoneyMicros)
	require.EqualValues(t, 40_000_000, record.InviteeCapUsedAfterMicros)

	var bucket RebateCapBucket
	require.NoError(t, db.Where("owner_id = ? AND cap_type = ?", invitee.Id, RebateCapTypeInviteeLifetime).First(&bucket).Error)
	require.EqualValues(t, 40_000_000, bucket.UsedMicros)

	// A fully capped settlement keeps a discarded audit record, but no payout.
	beforeQuota := int64(0)
	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	beforeQuota = int64(inviterUser.Quota)
	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-invitee-cap-2", 200, 100_000_000)

	var recordCount int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&recordCount).Error)
	require.EqualValues(t, 2, recordCount)
	var discarded RebateRecord
	require.NoError(t, db.Where("invitee_id = ? AND status = ?", invitee.Id, RebateStatusDiscarded).First(&discarded).Error)
	require.EqualValues(t, 2_500_000, discarded.DiscardedRewardMoneyMicros)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.EqualValues(t, beforeQuota, inviterUser.Quota)
}

func TestRecordWalletRebateInviterPeriodCapDiscardsExcess(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	setting := rebateRewardSetting()
	setting.InviterRewardLimit = 1_000_000
	useRebateRewardSetting(t, setting)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-inviter-cap", 200, 100_000_000)

	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.EqualValues(t, 1_000_000, record.RewardMoneyMicros)
	require.EqualValues(t, 500_000, record.RewardQuota)
	require.EqualValues(t, 1_500_000, record.DiscardedRewardMoneyMicros)
	require.Equal(t, time.Now().UTC().Format("2006-01"), record.InviterCapPeriodKey)

	var bucket RebateCapBucket
	require.NoError(t, db.Where("owner_id = ? AND cap_type = ?", inviter.Id, RebateCapTypeInviterMonthly).First(&bucket).Error)
	require.EqualValues(t, 1_000_000, bucket.UsedMicros)
}

func TestRecordWalletRebateSkipsWhenDisabledOrScheduled(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	disabled := rebateRewardSetting()
	disabled.Enabled = false
	useRebateRewardSetting(t, disabled)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-disabled", 200, 100_000_000)
	var recordCount int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&recordCount).Error)
	require.Zero(t, recordCount)
	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)

	scheduled := rebateRewardSetting()
	scheduled.PayoutMode = operation_setting.RebatePayoutScheduled
	scheduled.PayoutHour = 7
	scheduled.PayoutMinute = 30
	useRebateRewardSetting(t, scheduled)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-scheduled", 200, 100_000_000)

	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.Equal(t, RebateStatusPending, record.Status)
	require.Nil(t, record.PayoutLotID)
	require.True(t, record.AvailableAt.After(time.Now().UTC()))
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)
}

func TestGetWalletRebateSummaryAndRecordsStayUserScoped(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000
	setting := rebateRewardSetting()
	setting.InviterRewardLimit = 100_000_000
	useRebateRewardSetting(t, setting)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)
	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-query", 200, 100_000_000)

	summary, err := GetWalletRebateSummary(db, inviter.Id)
	require.NoError(t, err)
	require.True(t, summary.Enabled)
	require.EqualValues(t, 250, summary.RateBasisPoints)
	require.Zero(t, summary.PendingQuota)
	require.EqualValues(t, 1_250_000, summary.PaidQuota)
	require.EqualValues(t, 2_500_000, summary.PaidMoneyMicros)
	require.EqualValues(t, 1, summary.RewardCount)
	require.EqualValues(t, 2_500_000, summary.InviterPeriodUsedMicros)

	records, total, err := GetWalletRebateRecords(db, inviter.Id, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, records, 1)
	require.Equal(t, invitee.Id, records[0].InviteeID)
	require.Equal(t, invitee.Username, records[0].InviteeUsername)
	require.EqualValues(t, 1_250_000, records[0].RewardQuota)
	require.Equal(t, RebateStatusPaid, records[0].Status)

	// Another user must never observe these records.
	other := createRebateTestUser(t, db, 0)
	otherRecords, otherTotal, err := GetWalletRebateRecords(db, other.Id, 0, 20)
	require.NoError(t, err)
	require.Zero(t, otherTotal)
	require.Empty(t, otherRecords)

	otherSummary, err := GetWalletRebateSummary(db, other.Id)
	require.NoError(t, err)
	require.Zero(t, otherSummary.PaidQuota)
	require.Zero(t, otherSummary.RewardCount)
}

func TestPayDueWalletRebatesPaysPendingRecordsOnce(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	setting := rebateRewardSetting()
	setting.PayoutMode = operation_setting.RebatePayoutScheduled
	useRebateRewardSetting(t, setting)

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)
	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-payout", 200, 100_000_000)

	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.Equal(t, RebateStatusPending, record.Status)
	require.NoError(t, db.Model(&RebateRecord{}).Where("id = ?", record.ID).Update("available_at", time.Now().UTC().Add(-time.Minute)).Error)

	paid, err := PayDueWalletRebates(db, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, paid)

	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Equal(t, 1_250_000, inviterUser.Quota)
	require.NoError(t, db.First(&record, record.ID).Error)
	require.Equal(t, RebateStatusPaid, record.Status)
	require.NotNil(t, record.PayoutLotID)

	// The payout task is idempotent and never pays the same record twice.
	paid, err = PayDueWalletRebates(db, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.Zero(t, paid)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Equal(t, 1_250_000, inviterUser.Quota)
}

func TestRebateIndependentSpendingCapsExamples(t *testing.T) {
	for _, period := range []string{operation_setting.RebateLimitPeriodDaily, operation_setting.RebateLimitPeriodMonthly, operation_setting.RebateLimitPeriodPermanent} {
		for _, inviterCap := range []int64{0, 3000 * MoneyMicrosPerUnit} {
			t.Run(period+"/"+strconv.FormatInt(inviterCap, 10), func(t *testing.T) {
				setting := rebateRewardSetting()
				setting.RateBasisPoints = 500
				setting.InviteeEligibleMoneyLimit = 2000 * MoneyMicrosPerUnit
				setting.InviterEligibleMoneyLimit = inviterCap
				setting.InviterLimitPeriod = period
				useRebateRewardSetting(t, setting)
				db := newRebatePayoutDB(t)
				inviter, b := createRebateInvitation(t, db)
				c := createRebateTestUser(t, db, 0)
				require.NoError(t, db.Model(&User{}).Where("id = ?", c.Id).Update("inviter_id", inviter.Id).Error)
				settleRebateEligibleUsage(t, db, b.Id, "B3000", 100, 3000*MoneyMicrosPerUnit)
				settleRebateEligibleUsage(t, db, c.Id, "C500", 100, 500*MoneyMicrosPerUnit)
				summary, err := GetWalletRebateSummary(db, inviter.Id)
				require.NoError(t, err)
				require.EqualValues(t, 125*MoneyMicrosPerUnit, summary.PaidMoneyMicros)
				require.EqualValues(t, 50*MoneyMicrosPerUnit, summary.DiscardedMoneyMicros)
				if inviterCap > 0 {
					require.EqualValues(t, 2500*MoneyMicrosPerUnit, summary.InviterPeriodEligibleMoneyMicros)
					settleRebateEligibleUsage(t, db, c.Id, "C-more", 100, 1000*MoneyMicrosPerUnit)
					summary, err = GetWalletRebateSummary(db, inviter.Id)
					require.NoError(t, err)
					require.EqualValues(t, 150*MoneyMicrosPerUnit, summary.PaidMoneyMicros)
					require.EqualValues(t, 3000*MoneyMicrosPerUnit, summary.InviterPeriodEligibleMoneyMicros)
					// The invitee uses 1500, independently of the inviter's 3000 cap.
					cSummary, err := GetWalletRebateSummary(db, c.Id)
					require.NoError(t, err)
					require.EqualValues(t, 1500*MoneyMicrosPerUnit, cSummary.InviteeEligibleMoneyMicros)
				}
			})
		}
	}
}

func TestRebateClosureCountsCapsWithoutPayingNewRewards(t *testing.T) {
	setting := rebateRewardSetting()
	setting.Enabled, setting.CountDuringClosure = false, true
	setting.RateBasisPoints = 500
	setting.InviteeEligibleMoneyLimit = 2000 * MoneyMicrosPerUnit
	setting.InviterEligibleMoneyLimit = 3000 * MoneyMicrosPerUnit
	setting.InviterRewardLimit = 100 * MoneyMicrosPerUnit
	useRebateRewardSetting(t, setting)
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	settleRebateEligibleUsage(t, db, invitee.Id, "closed-new", 100, 1000*MoneyMicrosPerUnit)
	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.Equal(t, RebateStatusHeld, record.Status)
	require.True(t, record.CountedDuringClosure)
	require.True(t, record.HeldByClosure)
	require.EqualValues(t, 50*MoneyMicrosPerUnit, record.RewardMoneyMicros)
	require.EqualValues(t, 1000*MoneyMicrosPerUnit, record.InviteeCapUsedAfterMicros)
	require.EqualValues(t, 50*MoneyMicrosPerUnit, record.InviterCapUsedAfterMicros)
	paid, err := PayDueWalletRebates(db, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.Zero(t, paid)
	setting.Enabled = true
	require.NoError(t, operation_setting.UpdateRebateSetting(setting))
	paid, err = PayDueWalletRebates(db, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.EqualValues(t, 25_000_000, inviter.Quota)
}

func TestRebateRetryAndPendingKeepGenerationSnapshot(t *testing.T) {
	setting := rebateRewardSetting()
	setting.PayoutMode = operation_setting.RebatePayoutCustom
	setting.PayoutIntervalHours = 7
	setting.InviterEligibleMoneyLimit = 3000 * MoneyMicrosPerUnit
	useRebateRewardSetting(t, setting)
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	originalRatio := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalRatio })
	common.QuotaPerUnit = 7 // deliberately different from source lot snapshot
	settled := settleRebateEligibleUsage(t, db, invitee.Id, "stable-snapshot", 200, 100*MoneyMicrosPerUnit)
	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.EqualValues(t, 500_000*MoneyMicrosPerUnit, record.QuotaPerUnitMicros)
	require.EqualValues(t, 1_250_000, record.RewardQuota)
	setting.Enabled, setting.RateBasisPoints = false, 9000
	setting.PayoutMode, setting.PayoutIntervalHours, setting.SiteTimezone = operation_setting.RebatePayoutHalfDay, 12, "Asia/Shanghai"
	setting.InviterEligibleMoneyLimit = 1
	require.NoError(t, operation_setting.UpdateRebateSetting(setting))
	common.QuotaPerUnit = 999_999
	var retry *RebateRecord
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		retry, err = RecordWalletRebateTx(tx, settled.Settlement)
		return err
	}))
	require.Equal(t, record.ID, retry.ID)
	require.Equal(t, record.RateBasisPoints, retry.RateBasisPoints)
	require.True(t, record.AvailableAt.Equal(retry.AvailableAt))
	paid, err := PayDueWalletRebates(db, record.AvailableAt.Add(-time.Second), 100)
	require.NoError(t, err)
	require.Zero(t, paid)
	paid, err = PayDueWalletRebates(db, record.AvailableAt, 100)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.EqualValues(t, 1_250_000, inviter.Quota)
}

func TestRebateDustIsHeldNotDiscarded(t *testing.T) {
	for _, money := range []int64{1, 20, 60} {
		t.Run(strconv.FormatInt(money, 10), func(t *testing.T) {
			setting := rebateRewardSetting()
			setting.RateBasisPoints = 500
			setting.Enabled, setting.CountDuringClosure = false, true
			useRebateRewardSetting(t, setting)
			db := newRebatePayoutDB(t)
			inviter, invitee := createRebateInvitation(t, db)
			settleRebateEligibleUsage(t, db, invitee.Id, "dust", 1, money)
			var record RebateRecord
			require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
			require.Equal(t, RebateStatusHeld, record.Status)
			require.False(t, record.HeldByClosure)
			require.True(t, record.CountedDuringClosure)
			require.EqualValues(t, money, record.EligibleMoneyMicros)
			require.EqualValues(t, money/20, record.RewardMoneyMicros)
			require.Zero(t, record.DiscardedRewardMoneyMicros)
			setting.Enabled = true
			require.NoError(t, operation_setting.UpdateRebateSetting(setting))
			paid, err := PayDueWalletRebates(db, time.Now().UTC(), 100)
			require.NoError(t, err)
			require.Zero(t, paid)
			summary, err := GetWalletRebateSummary(db, inviter.Id)
			require.NoError(t, err)
			require.EqualValues(t, money/20, summary.PendingMoneyMicros)
		})
	}
}

func TestRebateRewardAndCapNumericBounds(t *testing.T) {
	v, err := rebateRewardMicros(math.MaxInt64, 10_000)
	require.NoError(t, err)
	require.EqualValues(t, math.MaxInt64, v)
	for _, args := range [][2]int64{{-1, 500}, {1, -1}, {1, 10_001}} {
		_, err := rebateRewardMicros(args[0], args[1])
		require.Error(t, err)
	}
	_, err = rebateQuotaForMoney(math.MaxInt64, math.MaxInt64)
	require.Error(t, err)
	_, err = rebateQuotaForMoney(1, 0)
	require.Error(t, err)
	db := newRebatePayoutDB(t)
	bucket := RebateCapBucket{OwnerID: 1, CapType: rebateInviterEligibleCapType, PeriodKey: "lifetime", UsedMicros: math.MaxInt64}
	require.NoError(t, db.Create(&bucket).Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := rebateCapConsumeTx(tx, &bucket, 1); return err }))
	require.NoError(t, db.First(&bucket, bucket.ID).Error)
	require.EqualValues(t, math.MaxInt64, bucket.UsedMicros)
}

func TestRebateMixedLotSnapshotsAndCapPrefix(t *testing.T) {
	setting := rebateRewardSetting()
	setting.RateBasisPoints = 500
	useRebateRewardSetting(t, setting)
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	key, err := NewWalletFinalSettlementKey(invitee.Id, "relay", "mixed-snapshot")
	require.NoError(t, err)
	settlement := WalletConsumptionSettlement{ID: 1, FinalSettlementKey: key, UserID: invitee.Id, EligibleMoneyConsumedMicros: 2 * MoneyMicrosPerUnit}
	for i, ratio := range []int64{100 * MoneyMicrosPerUnit, 200 * MoneyMicrosPerUnit} {
		lotKey, err := NewWalletCreditEventKey(invitee.Id, WalletCreditSourceRedemption, "redemption", "mixed-"+strconv.Itoa(i), "redeemed")
		require.NoError(t, err)
		lot := WalletCreditLot{
			CreditEventKey: lotKey, UserID: invitee.Id, Source: WalletCreditSourceRedemption, Component: "redeemed", Eligible: true,
			ReferenceType: "redemption", ReferenceID: "mixed-" + strconv.Itoa(i), QuotaTotal: ratio / MoneyMicrosPerUnit, QuotaRemaining: ratio / MoneyMicrosPerUnit,
			EligibleMoneyMicros: MoneyMicrosPerUnit, EligibleMoneyRemaining: MoneyMicrosPerUnit, QuotaPerUnitMicros: ratio,
		}
		require.NoError(t, db.Create(&lot).Error)
		allocation := WalletConsumptionAllocation{FinalSettlementKey: key, LotID: lot.ID, UserID: invitee.Id, QuotaConsumed: lot.QuotaTotal, EligibleQuotaConsumed: lot.QuotaTotal, EligibleMoneyConsumedMicros: MoneyMicrosPerUnit}
		require.NoError(t, db.Create(&allocation).Error)
	}
	for _, tc := range []struct{ base, reward, quota int64 }{
		{2 * MoneyMicrosPerUnit, 100_000, 15},
		{MoneyMicrosPerUnit, 50_000, 5},
		{2 * MoneyMicrosPerUnit, 60_000, 7}, // legacy reward cap truncates second lot
	} {
		quota, ratio, dust, err := rebateQuotaFromAllocationsTx(db, settlement, tc.base, tc.reward, 500)
		require.NoError(t, err)
		require.EqualValues(t, tc.quota, quota)
		require.Zero(t, ratio) // mixed snapshot, not today's ratio
		require.False(t, dust)
	}
	// Sub-micro money from different ratios must not get reassigned by floor.
	require.NoError(t, db.Model(&WalletConsumptionAllocation{}).Where("final_settlement_key = ?", key).Update("eligible_money_consumed_micros", 1).Error)
	settlement.EligibleMoneyConsumedMicros = 2
	quota, _, dust, err := rebateQuotaFromAllocationsTx(db, settlement, 2, 1, 5000)
	require.NoError(t, err)
	require.Zero(t, quota)
	require.True(t, dust)
	// Missing provenance must fail closed, never fall back to current ratio.
	require.NoError(t, db.Where("user_id = ?", invitee.Id).Delete(&WalletCreditLot{}).Error)
	_, _, _, err = rebateQuotaFromAllocationsTx(db, settlement, 2, 1, 5000)
	require.ErrorContains(t, err, "source-lot")
	require.NotZero(t, inviter.Id)
}

func TestRebateGenerationFailureRollsBackCapsWalletAndLots(t *testing.T) {
	setting := rebateRewardSetting()
	setting.InviteeEligibleMoneyLimit = 2000 * MoneyMicrosPerUnit
	setting.InviterEligibleMoneyLimit = 3000 * MoneyMicrosPerUnit
	setting.InviterRewardLimit = 150 * MoneyMicrosPerUnit
	useRebateRewardSetting(t, setting)
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	key, err := NewWalletFinalSettlementKey(invitee.Id, "relay", "generation-rollback")
	require.NoError(t, err)
	lotKey, err := NewWalletCreditEventKey(invitee.Id, WalletCreditSourceRedemption, "redemption", "generation-rollback", "redeemed")
	require.NoError(t, err)
	lot := WalletCreditLot{CreditEventKey: lotKey, UserID: invitee.Id, Source: WalletCreditSourceRedemption, Component: "redeemed", Eligible: true,
		ReferenceType: "redemption", ReferenceID: "generation-rollback", QuotaTotal: 100, QuotaRemaining: 100,
		EligibleMoneyMicros: MoneyMicrosPerUnit, EligibleMoneyRemaining: MoneyMicrosPerUnit, QuotaPerUnitMicros: 200 * MoneyMicrosPerUnit}
	require.NoError(t, db.Create(&lot).Error)
	require.NoError(t, db.Create(&WalletConsumptionAllocation{FinalSettlementKey: key, LotID: lot.ID, UserID: invitee.Id, QuotaConsumed: 100, EligibleQuotaConsumed: 100, EligibleMoneyConsumedMicros: MoneyMicrosPerUnit}).Error)
	settlement := WalletConsumptionSettlement{ID: 1, FinalSettlementKey: key, UserID: invitee.Id, EligibleQuotaConsumed: 100, EligibleMoneyConsumedMicros: MoneyMicrosPerUnit}
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_rebate_record BEFORE INSERT ON rebate_records BEGIN SELECT RAISE(ABORT, 'injected rebate insert failure'); END`).Error)
	err = db.Transaction(func(tx *gorm.DB) error { _, err := RecordWalletRebateTx(tx, settlement); return err })
	require.ErrorContains(t, err, "injected rebate insert failure")
	var count int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&RebateCapBucket{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("source = ?", WalletCreditSourceRebate).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Zero(t, inviter.Quota)
	require.NoError(t, db.Exec("DROP TRIGGER fail_rebate_record").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := RecordWalletRebateTx(tx, settlement); return err }))
	// Exact 5 quota claim: the failed attempt rolled back its credit too.
	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	require.Equal(t, RebateStatusPaid, record.Status)
	require.EqualValues(t, 25_000, record.RewardMoneyMicros)
	require.EqualValues(t, 5, record.RewardQuota)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.EqualValues(t, 5, inviter.Quota)
}
