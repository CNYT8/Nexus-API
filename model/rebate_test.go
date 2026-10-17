package model

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNewWalletCreditEventKeyIsStableAndComponentScoped(t *testing.T) {
	first, err := NewWalletCreditEventKey(17, WalletCreditSourceTopUp, "topup", "trade-1", "paid")
	require.NoError(t, err)
	second, err := NewWalletCreditEventKey(17, WalletCreditSourceTopUp, "topup", "trade-1", "paid")
	require.NoError(t, err)
	gift, err := NewWalletCreditEventKey(17, WalletCreditSourceTopUp, "topup", "trade-1", "gift")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NotEqual(t, first, gift)
	require.True(t, isCanonicalSHA256(first))
}

func TestWalletCreditLotRejectsIneligibleSourcesAndBadBalances(t *testing.T) {
	validKey, err := NewWalletCreditEventKey(9, WalletCreditSourceLegacy, "migration", "baseline", "legacy_baseline")
	require.NoError(t, err)
	base := WalletCreditLot{
		CreditEventKey: validKey,
		UserID:         9,
		Source:         WalletCreditSourceLegacy,
		Component:      "legacy_baseline",
		Eligible:       false,
		ReferenceType:  "migration",
		ReferenceID:    "baseline",
		QuotaTotal:     100,
		QuotaRemaining: 100,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	require.NoError(t, base.BeforeCreate(nil))

	bad := base
	bad.Eligible = true
	require.Error(t, bad.BeforeCreate(nil))
	bad = base
	bad.QuotaRemaining = 101
	require.Error(t, bad.BeforeCreate(nil))
	bad = base
	bad.EligibleMoneyMicros = 1
	require.Error(t, bad.BeforeCreate(nil))
}

func TestRebateRecordRequiresStableFinalSettlementIdentity(t *testing.T) {
	settlementKey, err := NewRebateIdempotencyKey("final-settlement", "invitee:3", "settlement:abc")
	require.NoError(t, err)
	payoutKey, err := NewRebateIdempotencyKey("rebate-payout", settlementKey)
	require.NoError(t, err)

	record := RebateRecord{
		SettlementKey:   settlementKey,
		PayoutKey:       payoutKey,
		InviterID:       2,
		InviteeID:       3,
		RateBasisPoints: 250,
		Status:          RebateStatusPending,
		AvailableAt:     time.Now().UTC(),
	}
	require.NoError(t, record.BeforeCreate(nil))

	bad := record
	bad.InviterID = bad.InviteeID
	require.Error(t, bad.BeforeCreate(nil))
	bad = record
	bad.SettlementKey = "client-request-id"
	require.Error(t, bad.BeforeCreate(nil))
	bad = record
	bad.RateBasisPoints = 10_001
	require.Error(t, bad.BeforeCreate(nil))
}

func TestRebateConfigUsesFixedPointAndValidatesBounds(t *testing.T) {
	valid := RebateSettingForTest()
	require.NoError(t, operation_setting.ValidateRebateSetting(valid))

	valid.RateBasisPoints = 10_001
	require.Error(t, operation_setting.ValidateRebateSetting(valid))
	valid = RebateSettingForTest()
	valid.InviterRewardLimit = math.MaxInt64
	require.NoError(t, operation_setting.ValidateRebateSetting(valid))
}

func TestEligibleMoneyValuationForQuotaUsesFixedPoint(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = 2.5

	moneyMicros, quotaPerUnitMicros, err := EligibleMoneyValuationForQuota(1)
	require.NoError(t, err)
	require.EqualValues(t, 400_000, moneyMicros)
	require.EqualValues(t, 2_500_000, quotaPerUnitMicros)

	_, _, err = EligibleMoneyValuationForQuota(0)
	require.Error(t, err)
}

func TestInitializeWalletCreditBaselineIsIdempotentAndNonEligible(t *testing.T) {
	db := newRebateTestDB(t)
	user := createRebateTestUser(t, db, 1234)

	require.NoError(t, InitializeWalletCreditBaseline(db))
	require.NoError(t, InitializeWalletCreditBaseline(db))

	var lots []WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&lots).Error)
	require.Len(t, lots, 1)
	require.Equal(t, WalletCreditSourceLegacy, lots[0].Source)
	require.False(t, lots[0].Eligible)
	require.EqualValues(t, 1234, lots[0].QuotaRemaining)
	require.Zero(t, lots[0].EligibleMoneyRemaining)
}

func TestAddWalletCreditLotTxRequiresBaseline(t *testing.T) {
	db := newRebateTestDB(t)
	user := createRebateTestUser(t, db, 0)
	key, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceRedemption, "redemption", "code-before-baseline", "redeemed")
	require.NoError(t, err)
	lot := WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 user.Id,
		Source:                 WalletCreditSourceRedemption,
		Component:              "redeemed",
		Eligible:               true,
		ReferenceType:          "redemption",
		ReferenceID:            "code-before-baseline",
		QuotaTotal:             10,
		QuotaRemaining:         10,
		EligibleMoneyMicros:    1_000_000,
		EligibleMoneyRemaining: 1_000_000,
		QuotaPerUnitMicros:     100_000_000,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		_, txErr := AddWalletCreditLotTx(tx, lot)
		return txErr
	})
	require.ErrorContains(t, err, "wallet credit baseline is not initialized")
	var lotCount int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Count(&lotCount).Error)
	require.Zero(t, lotCount)
	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Zero(t, updated.Quota)
}

func TestAddWalletCreditLotTxCreditsOnceAndRollsBackOnOverflow(t *testing.T) {
	db := newRebateTestDB(t)
	marker := RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&marker).Error)

	user := createRebateTestUser(t, db, 0)
	key, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceRedemption, "redemption", "code-1", "redeemed")
	require.NoError(t, err)
	lot := WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 user.Id,
		Source:                 WalletCreditSourceRedemption,
		Component:              "redeemed",
		Eligible:               true,
		ReferenceType:          "redemption",
		ReferenceID:            "code-1",
		QuotaTotal:             100,
		QuotaRemaining:         100,
		EligibleMoneyMicros:    1_000_000,
		EligibleMoneyRemaining: 1_000_000,
		QuotaPerUnitMicros:     100_000_000,
	}

	credited, err := addTestWalletCreditLot(db, lot)
	require.NoError(t, err)
	require.True(t, credited)
	credited, err = addTestWalletCreditLot(db, lot)
	require.NoError(t, err)
	require.False(t, credited)

	changedLot := lot
	changedLot.QuotaTotal++
	changedLot.QuotaRemaining++
	_, err = addTestWalletCreditLot(db, changedLot)
	require.ErrorContains(t, err, "wallet credit event key reused with different immutable values")

	var updated User
	require.NoError(t, db.Select("quota").First(&updated, user.Id).Error)
	require.Equal(t, 100, updated.Quota)

	overflowUser := createRebateTestUser(t, db, math.MaxInt64-1)
	overflowKey, err := NewWalletCreditEventKey(overflowUser.Id, WalletCreditSourceRedemption, "redemption", "code-overflow", "redeemed")
	require.NoError(t, err)
	overflowLot := lot
	overflowLot.ID = 0
	overflowLot.CreditEventKey = overflowKey
	overflowLot.UserID = overflowUser.Id
	overflowLot.ReferenceID = "code-overflow"
	overflowLot.QuotaTotal = 2
	overflowLot.QuotaRemaining = 2

	err = db.Transaction(func(tx *gorm.DB) error {
		_, txErr := AddWalletCreditLotTx(tx, overflowLot)
		return txErr
	})
	require.Error(t, err)
	var lotCount int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("credit_event_key = ?", overflowKey).Count(&lotCount).Error)
	require.Zero(t, lotCount)
}

func newRebateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:rebate-test-"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&User{},
		&WalletReservation{},
		&WalletLotHold{},
		&WalletDebtRepayment{},
		&WalletCreditLot{},
		&WalletConsumptionSettlement{},
		&WalletConsumptionAllocation{},
		&RebateCapBucket{},
		&RebateSystemState{},
		&RebateRecord{},
	))
	return db
}

func createRebateTestUser(t *testing.T, db *gorm.DB, quota int) *User {
	t.Helper()
	user := &User{
		Username: common.GetUUID(),
		Password: "rebate-test-password",
		AffCode:  common.GetUUID(),
		Group:    "default",
		Quota:    quota,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func addTestWalletCreditLot(db *gorm.DB, lot WalletCreditLot) (credited bool, err error) {
	err = db.Transaction(func(tx *gorm.DB) error {
		var addErr error
		credited, addErr = AddWalletCreditLotTx(tx, lot)
		return addErr
	})
	return credited, err
}

func TestFinalizeWalletConsumptionAllocatesEligibleUsageAndIsIdempotent(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)
	key, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceRedemption, "redemption", "settlement-lot", "redeemed")
	require.NoError(t, err)
	lot := WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 user.Id,
		Source:                 WalletCreditSourceRedemption,
		Component:              "redeemed",
		Eligible:               true,
		ReferenceType:          "redemption",
		ReferenceID:            "settlement-lot",
		QuotaTotal:             100,
		QuotaRemaining:         100,
		EligibleMoneyMicros:    1_000_000,
		EligibleMoneyRemaining: 1_000_000,
		QuotaPerUnitMicros:     100_000_000,
	}
	credited, err := addTestWalletCreditLot(db, lot)
	require.NoError(t, err)
	require.True(t, credited)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 50).Error)

	settlementKey, err := NewRebateIdempotencyKey("final-settlement", "user", fmt.Sprint(user.Id), "request-1")
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(db, settlementKey, user.Id, 50, 80)
	require.NoError(t, err)
	require.EqualValues(t, 80, result.EligibleQuotaConsumed)
	require.EqualValues(t, 800_000, result.EligibleMoneyConsumedMicros)
	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 20, updated.Quota)
	var remaining WalletCreditLot
	require.NoError(t, db.Where("credit_event_key = ?", key).First(&remaining).Error)
	require.EqualValues(t, 20, remaining.QuotaRemaining)
	require.EqualValues(t, 200_000, remaining.EligibleMoneyRemaining)

	retry, err := FinalizeWalletConsumption(db, settlementKey, user.Id, 50, 80)
	require.NoError(t, err)
	require.EqualValues(t, result.Settlement.ID, retry.Settlement.ID)
	require.EqualValues(t, result.EligibleMoneyConsumedMicros, retry.EligibleMoneyConsumedMicros)
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 20, updated.Quota)

	_, err = FinalizeWalletConsumption(db, settlementKey, user.Id, 50, 81)
	require.ErrorContains(t, err, "different immutable values")
}

func TestFinalizeWalletConsumptionRefundsReservationAndAllowsZeroUsage(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)
	key, err := NewWalletCreditEventKey(user.Id, WalletCreditSourcePromotion, "promotion", "zero-usage", "bonus")
	require.NoError(t, err)
	_, err = addTestWalletCreditLot(db, WalletCreditLot{
		CreditEventKey: key,
		UserID:         user.Id,
		Source:         WalletCreditSourcePromotion,
		Component:      "gift",
		Eligible:       false,
		ReferenceType:  "promotion",
		ReferenceID:    "zero-usage",
		QuotaTotal:     100,
		QuotaRemaining: 100,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 40).Error)

	refundKey, err := NewRebateIdempotencyKey("final-settlement", "user", fmt.Sprint(user.Id), "refund")
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(db, refundKey, user.Id, 60, 20)
	require.NoError(t, err)
	require.Zero(t, result.EligibleQuotaConsumed)
	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 80, updated.Quota)

	zeroKey, err := NewRebateIdempotencyKey("final-settlement", "user", fmt.Sprint(user.Id), "zero")
	require.NoError(t, err)
	result, err = FinalizeWalletConsumption(db, zeroKey, user.Id, 0, 0)
	require.NoError(t, err)
	require.Zero(t, result.EligibleQuotaConsumed)
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 80, updated.Quota)
}

func TestFinalizeWalletConsumptionRecordsUnallocatedDebtAndConsumesIneligibleFirst(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)

	newLot := func(source WalletCreditSource, component string, eligible bool, quota, money int64, ref string) WalletCreditLot {
		key, err := NewWalletCreditEventKey(user.Id, source, string(source), ref, component)
		require.NoError(t, err)
		lot := WalletCreditLot{
			CreditEventKey:         key,
			UserID:                 user.Id,
			Source:                 source,
			Component:              component,
			Eligible:               eligible,
			ReferenceType:          string(source),
			ReferenceID:            ref,
			QuotaTotal:             quota,
			QuotaRemaining:         quota,
			EligibleMoneyMicros:    money,
			EligibleMoneyRemaining: money,
		}
		if eligible {
			lot.QuotaPerUnitMicros = 100_000_000
		}
		return lot
	}

	gift := creditRebateTestLot(t, db, newLot(WalletCreditSourcePromotion, "gift", false, 30, 0, "debt-gift"))
	small := creditRebateTestLot(t, db, newLot(WalletCreditSourceRedemption, "redeemed", true, 40, 400_000, "debt-small"))
	large := creditRebateTestLot(t, db, newLot(WalletCreditSourceRedemption, "redeemed", true, 100, 2_000_000, "debt-large"))

	var credited User
	require.NoError(t, db.First(&credited, user.Id).Error)
	require.Equal(t, 170, credited.Quota)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 20).Error)

	key, err := NewWalletFinalSettlementKey(user.Id, "relay", "debt-request-1")
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(db, key, user.Id, 150, 200)
	require.NoError(t, err)
	require.EqualValues(t, 140, result.EligibleQuotaConsumed)
	require.EqualValues(t, 2_400_000, result.EligibleMoneyConsumedMicros)
	require.EqualValues(t, 30, result.UnallocatedQuota)
	require.EqualValues(t, 30, result.Settlement.UnallocatedQuota)

	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, -30, updated.Quota)

	for _, lot := range []*WalletCreditLot{gift, small, large} {
		var stored WalletCreditLot
		require.NoError(t, db.First(&stored, lot.ID).Error)
		require.Zero(t, stored.QuotaRemaining)
		require.Zero(t, stored.EligibleMoneyRemaining)
	}

	var allocations []WalletConsumptionAllocation
	require.NoError(t, db.Where("final_settlement_key = ?", key).Order("id ASC").Find(&allocations).Error)
	require.Len(t, allocations, 3)
	require.EqualValues(t, gift.ID, allocations[0].LotID)
	require.Zero(t, allocations[0].EligibleQuotaConsumed)
	require.Zero(t, allocations[0].EligibleMoneyConsumedMicros)
	require.EqualValues(t, small.ID, allocations[1].LotID)
	require.EqualValues(t, 40, allocations[1].EligibleQuotaConsumed)
	require.EqualValues(t, 400_000, allocations[1].EligibleMoneyConsumedMicros)
	require.EqualValues(t, large.ID, allocations[2].LotID)
	require.EqualValues(t, 100, allocations[2].EligibleQuotaConsumed)
	require.EqualValues(t, 2_000_000, allocations[2].EligibleMoneyConsumedMicros)
}

func TestFinalizeWalletConsumptionPreservesEligibleMoneyRemainder(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)
	key, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceRedemption, "redemption", "rounding", "redeemed")
	require.NoError(t, err)
	lot := creditRebateTestLot(t, db, WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 user.Id,
		Source:                 WalletCreditSourceRedemption,
		Component:              "redeemed",
		Eligible:               true,
		ReferenceType:          "redemption",
		ReferenceID:            "rounding",
		QuotaTotal:             3,
		QuotaRemaining:         3,
		EligibleMoneyMicros:    1_000_000,
		EligibleMoneyRemaining: 1_000_000,
		QuotaPerUnitMicros:     500_000,
	})

	// Simulate the reservation that already debited the pre-consumed quota.
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 0).Error)

	firstKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "rounding-1")
	require.NoError(t, err)
	first, err := FinalizeWalletConsumption(db, firstKey, user.Id, 3, 1)
	require.NoError(t, err)
	require.EqualValues(t, 333_333, first.EligibleMoneyConsumedMicros)
	require.Zero(t, first.UnallocatedQuota)
	var stored WalletCreditLot
	require.NoError(t, db.First(&stored, lot.ID).Error)
	require.EqualValues(t, 2, stored.QuotaRemaining)
	require.EqualValues(t, 666_667, stored.EligibleMoneyRemaining)

	// The second request reserves its pre-consumed quota from the refunded balance.
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 0).Error)

	secondKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "rounding-2")
	require.NoError(t, err)
	second, err := FinalizeWalletConsumption(db, secondKey, user.Id, 2, 2)
	require.NoError(t, err)
	require.EqualValues(t, 666_667, second.EligibleMoneyConsumedMicros)
	require.Zero(t, second.UnallocatedQuota)
	require.NoError(t, db.First(&stored, lot.ID).Error)
	require.Zero(t, stored.QuotaRemaining)
	require.Zero(t, stored.EligibleMoneyRemaining)
	require.EqualValues(t, 1_000_000, first.EligibleMoneyConsumedMicros+second.EligibleMoneyConsumedMicros)

	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Zero(t, updated.Quota)

	// A zero-delta settlement must still fail closed when the user is gone.
	require.NoError(t, db.Delete(&User{}, user.Id).Error)
	missingKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "rounding-3")
	require.NoError(t, err)
	_, err = FinalizeWalletConsumption(db, missingKey, user.Id, 0, 0)
	require.ErrorContains(t, err, "not found while settling wallet quota")
}

func creditRebateTestLot(t *testing.T, db *gorm.DB, lot WalletCreditLot) *WalletCreditLot {
	t.Helper()
	credited, err := addTestWalletCreditLot(db, lot)
	require.NoError(t, err)
	require.True(t, credited)
	var stored WalletCreditLot
	require.NoError(t, db.Where("credit_event_key = ?", lot.CreditEventKey).First(&stored).Error)
	return &stored
}

func TestCreditTopUpTxKeepsLegacyPathBeforeBaseline(t *testing.T) {
	db := newRebateTestDB(t)
	user := createRebateTestUser(t, db, 10)

	applied, err := CreditTopUpTx(db, user.Id, "trade-legacy", 25)
	require.NoError(t, err)
	require.True(t, applied)

	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 35, updated.Quota)
	var lotCount int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Count(&lotCount).Error)
	require.Zero(t, lotCount)
}

func TestCreditTopUpTxWritesPaidLotAfterBaseline(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)

	applied, err := CreditTopUpTx(db, user.Id, "trade-paid", 25)
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = CreditTopUpTx(db, user.Id, "trade-paid", 25)
	require.NoError(t, err)
	require.False(t, applied)

	var updated User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 25, updated.Quota)

	var lot WalletCreditLot
	require.NoError(t, db.Where("reference_id = ?", "trade-paid").First(&lot).Error)
	require.Equal(t, WalletCreditSourceTopUp, lot.Source)
	require.Equal(t, "paid", lot.Component)
	require.True(t, lot.Eligible)
	require.EqualValues(t, 25, lot.QuotaRemaining)
	require.EqualValues(t, 50, lot.EligibleMoneyMicros)
	require.True(t, isCanonicalSHA256(lot.CreditEventKey))
}

func TestAccountingMigrationCreatesTablesOnSQLite(t *testing.T) {
	require.NoError(t, migrateAccountingTables(DB))
	for _, model := range []interface{}{
		&WalletCreditLot{},
		&WalletConsumptionSettlement{},
		&WalletConsumptionAllocation{},
		&RebateCapBucket{},
		&RebateSystemState{},
		&RebateRecord{},
	} {
		require.True(t, DB.Migrator().HasTable(model))
	}
	require.True(t, DB.Migrator().HasIndex(&WalletCreditLot{}, "CreditEventKey"))
}

func RebateSettingForTest() operation_setting.RebateSetting {
	return operation_setting.RebateSetting{
		RateBasisPoints:           250,
		PayoutMode:                "immediate",
		PayoutHour:                7,
		InviterLimitPeriod:        "monthly",
		InviteeEligibleMoneyLimit: 10 * MoneyMicrosPerUnit,
		InviterRewardLimit:        100 * MoneyMicrosPerUnit,
		SiteTimezone:              "UTC",
	}
}
