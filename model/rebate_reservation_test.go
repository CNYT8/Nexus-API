package model

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func reservationTestWallet(t *testing.T, quota int) (*gorm.DB, *User) {
	t.Helper()
	db := newRebateTestDB(t)
	user := createRebateTestUser(t, db, quota)
	require.NoError(t, InitializeWalletCreditBaseline(db))
	return db, user
}
func reservationTestKey(t *testing.T, user int, ref string) string {
	t.Helper()
	key, err := NewWalletFinalSettlementKey(user, "reservation-test", ref)
	require.NoError(t, err)
	return key
}
func reservationTestQuota(t *testing.T, db *gorm.DB, user int, want int) {
	t.Helper()
	var stored User
	require.NoError(t, db.First(&stored, user).Error)
	require.Equal(t, want, stored.Quota)
}
func reservationTestLot(t *testing.T, user int, ref string, quota, money int64) WalletCreditLot {
	t.Helper()
	key, err := NewWalletCreditEventKey(user, WalletCreditSourceRedemption, "redemption", ref, "redeemed")
	require.NoError(t, err)
	return WalletCreditLot{CreditEventKey: key, UserID: user, Source: WalletCreditSourceRedemption, Component: "redeemed", Eligible: true, ReferenceType: "redemption", ReferenceID: ref, QuotaTotal: quota, QuotaRemaining: quota, EligibleMoneyMicros: money, EligibleMoneyRemaining: money, QuotaPerUnitMicros: 1_000_000}
}

func TestWalletReservationAbsoluteTotalRollbackAndPermanentIdentity(t *testing.T) {
	db, user := reservationTestWallet(t, 100)
	key := reservationTestKey(t, user.Id, "absolute")
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 0))
	var r WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&r).Error)
	require.Equal(t, WalletReservationReserved, r.State)
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 40))
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 40))
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 60))
	reservationTestQuota(t, db, user.Id, 40)
	require.ErrorIs(t, ReserveWalletQuota(db, key, user.Id, 59), ErrWalletReservationConflict)
	other := createRebateTestUser(t, db, 100)
	require.ErrorIs(t, ReserveWalletQuota(db, key, other.Id, 0), ErrWalletReservationConflict)
	require.ErrorIs(t, RefundWalletReservation(db, key, other.Id), ErrWalletReservationConflict)
	require.ErrorIs(t, ReleaseWalletReservationTo(db, key, user.Id, 59, 40), ErrWalletReservationConflict)
	require.NoError(t, ReleaseWalletReservationTo(db, key, user.Id, 60, 40))
	require.ErrorIs(t, ReleaseWalletReservationTo(db, key, user.Id, 60, 40), ErrWalletReservationConflict)
	reservationTestQuota(t, db, user.Id, 60)
	abort := errors.New("force rollback")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := ReserveWalletQuotaTx(tx, key, user.Id, 80); err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	reservationTestQuota(t, db, user.Id, 60)
	require.NoError(t, RefundWalletReservation(db, key, user.Id))
	require.NoError(t, RefundWalletReservation(db, key, user.Id))
	reservationTestQuota(t, db, user.Id, 100)
	require.ErrorIs(t, ReserveWalletQuota(db, key, user.Id, 0), ErrWalletReservationConflict)
	_, err = FinalizeWalletConsumption(db, key, user.Id, 40, 0)
	require.ErrorIs(t, err, ErrWalletReservationConflict)
}

func TestWalletReservationConcurrentRequestsReleaseOnlyOwnHolds(t *testing.T) {
	db, user := reservationTestWallet(t, 100)
	const n = 10
	keys := make([]string, n)
	for i := range keys {
		keys[i] = reservationTestKey(t, user.Id, fmt.Sprint(i))
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, key := range keys {
		wg.Add(1)
		go func(key string) { defer wg.Done(); errs <- ReserveWalletQuota(db, key, user.Id, 10) }(key)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	reservationTestQuota(t, db, user.Id, 0)
	_, err := FinalizeWalletConsumption(db, keys[0], user.Id, 10, 15)
	require.NoError(t, err)
	reservationTestQuota(t, db, user.Id, -5)
	var settlement WalletConsumptionSettlement
	require.NoError(t, db.Where("final_settlement_key = ?", keys[0]).First(&settlement).Error)
	require.EqualValues(t, 5, settlement.UnallocatedQuota)
	require.NoError(t, RefundWalletReservation(db, keys[1], user.Id))
	require.NoError(t, RefundWalletReservation(db, keys[1], user.Id))
	reservationTestQuota(t, db, user.Id, 5)
	var holds []WalletLotHold
	require.NoError(t, db.Where("quota_held > 0").Find(&holds).Error)
	require.Len(t, holds, 8)
	for _, h := range holds {
		require.EqualValues(t, 10, h.QuotaHeld)
	}
	for _, key := range keys[2:] {
		require.NoError(t, RefundWalletReservation(db, key, user.Id))
	}
	reservationTestQuota(t, db, user.Id, 85)
}

func TestWalletFinalizingBarrierSurvivesFailedTransactionAndRecovery(t *testing.T) {
	db, user := reservationTestWallet(t, 100)
	key := reservationTestKey(t, user.Id, "prepare")
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 60))
	require.ErrorIs(t, PrepareWalletFinalization(db, key, user.Id, 59, 30), ErrWalletReservationConflict)
	require.NoError(t, PrepareWalletFinalization(db, key, user.Id, 60, 30))
	require.NoError(t, PrepareWalletFinalization(db, key, user.Id, 60, 30))
	require.ErrorIs(t, PrepareWalletFinalization(db, key, user.Id, 60, 31), ErrWalletReservationConflict)
	require.ErrorIs(t, RefundWalletReservation(db, key, user.Id), ErrWalletReservationConflict)
	require.ErrorIs(t, ReleaseWalletReservationTo(db, key, user.Id, 60, 0), ErrWalletReservationConflict)
	abort := errors.New("commit aborted")
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := FinalizeWalletConsumptionTx(tx, key, user.Id, 60, 30)
		if err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	reservationTestQuota(t, db, user.Id, 40)
	var saved WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&saved).Error)
	require.Equal(t, WalletReservationFinalizing, saved.State)
	require.EqualValues(t, 30, saved.FinalQuota)
	// Recovery reconstructs arguments only from durable rows, not memory.
	processed, err := RecoverFinalizingWalletSettlements(db, 1)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	processed, err = RecoverFinalizingWalletSettlements(db, 100)
	require.NoError(t, err)
	require.Zero(t, processed)
	retry, err := FinalizeWalletConsumption(db, key, user.Id, 60, 30)
	require.NoError(t, err)
	require.EqualValues(t, 30, retry.Settlement.WalletQuota)
	reservationTestQuota(t, db, user.Id, 70)
	require.ErrorIs(t, RefundWalletReservation(db, key, user.Id), ErrWalletReservationConflict)
}

func TestWalletReservationNoBalanceEligibilityAndDeletedZeroOperations(t *testing.T) {
	db, user := reservationTestWallet(t, 0)
	// An unmigrated quota-only writer must not synthesize eligible source lots.
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", 100).Error)
	key := reservationTestKey(t, user.Id, "missing-source")
	require.ErrorIs(t, ReserveWalletQuota(db, key, user.Id, 1), ErrWalletInsufficientQuota)
	reservationTestQuota(t, db, user.Id, 100)
	var count int64
	require.NoError(t, db.Model(&WalletReservation{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 0))
	require.NoError(t, db.Delete(&User{}, user.Id).Error)
	require.Error(t, ReserveWalletQuota(db, key, user.Id, 0))
	require.Error(t, RefundWalletReservation(db, key, user.Id))
	require.Error(t, PrepareWalletFinalization(db, key, user.Id, 0, 0))
	_, err := FinalizeWalletConsumption(db, key, user.Id, 0, 0)
	require.Error(t, err)
}

func TestWalletExactLargeValuationFloorAndOverflowRollback(t *testing.T) {
	v, err := walletFloorProduct(math.MaxInt64-1, math.MaxInt64-1, math.MaxInt64)
	require.NoError(t, err)
	require.EqualValues(t, math.MaxInt64-2, v)
	_, err = walletCheckedAdd(math.MaxInt64, 1)
	require.Error(t, err)
	db, user := reservationTestWallet(t, 0)
	lot := creditRebateTestLot(t, db, reservationTestLot(t, user.Id, "large-money", 3, math.MaxInt64))
	key := reservationTestKey(t, user.Id, "large-money")
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 1))
	result, err := FinalizeWalletConsumption(db, key, user.Id, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, math.MaxInt64/3, result.EligibleMoneyConsumedMicros)
	key2 := reservationTestKey(t, user.Id, "large-money-rest")
	require.NoError(t, ReserveWalletQuota(db, key2, user.Id, 2))
	rest, err := FinalizeWalletConsumption(db, key2, user.Id, 2, 2)
	require.NoError(t, err)
	require.EqualValues(t, math.MaxInt64, result.EligibleMoneyConsumedMicros+rest.EligibleMoneyConsumedMicros)
	var stored WalletCreditLot
	require.NoError(t, db.First(&stored, lot.ID).Error)
	require.Zero(t, stored.EligibleMoneyRemaining)
	// Two source valuations can overflow aggregation despite safe quota totals.
	creditRebateTestLot(t, db, reservationTestLot(t, user.Id, "overflow-1", 1, math.MaxInt64))
	creditRebateTestLot(t, db, reservationTestLot(t, user.Id, "overflow-2", 1, 1))
	key3 := reservationTestKey(t, user.Id, "money-overflow")
	require.NoError(t, ReserveWalletQuota(db, key3, user.Id, 2))
	_, err = FinalizeWalletConsumption(db, key3, user.Id, 2, 2)
	require.Error(t, err)
	require.NoError(t, RefundWalletReservation(db, key3, user.Id))
	reservationTestQuota(t, db, user.Id, 2)
}

func TestWalletCreditRepaysDebtWithAuditAndRollbackWithoutRebate(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	db, user := reservationTestWallet(t, 0)
	inviter := createRebateTestUser(t, db, 0)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("inviter_id", inviter.Id).Error)
	key := reservationTestKey(t, user.Id, "debt")
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 0))
	result, err := FinalizeWalletConsumption(db, key, user.Id, 0, 50)
	require.NoError(t, err)
	require.EqualValues(t, 50, result.UnallocatedQuota)
	lot := reservationTestLot(t, user.Id, "debt-credit", 80, 800_000)
	abort := errors.New("credit abort")
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := AddWalletCreditLotTx(tx, lot)
		if err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	reservationTestQuota(t, db, user.Id, -50)
	var count int64
	require.NoError(t, db.Model(&WalletDebtRepayment{}).Count(&count).Error)
	require.Zero(t, count)
	stored := creditRebateTestLot(t, db, lot)
	require.EqualValues(t, 30, stored.QuotaRemaining)
	require.EqualValues(t, 300_000, stored.EligibleMoneyRemaining)
	applied, err := addTestWalletCreditLot(db, lot)
	require.NoError(t, err)
	require.False(t, applied)
	reservationTestQuota(t, db, user.Id, 30)
	var debt WalletConsumptionSettlement
	require.NoError(t, db.Where("final_settlement_key = ?", key).First(&debt).Error)
	require.EqualValues(t, 50, debt.UnallocatedQuota)
	require.EqualValues(t, 50, debt.DebtRepaidQuota)
	require.Zero(t, debt.EligibleQuotaConsumed)
	var link WalletDebtRepayment
	require.NoError(t, db.First(&link).Error)
	require.EqualValues(t, 50, link.QuotaRepaid)
	require.EqualValues(t, 500_000, link.MoneyRetiredMicros)
	require.NoError(t, db.Model(&RebateRecord{}).Count(&count).Error)
	require.Zero(t, count)
	// Rebate credits themselves never create eligible balances/rewards.
	rebateKey, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceRebate, "rebate", "no-recursion", "payout")
	require.NoError(t, err)
	creditRebateTestLot(t, db, WalletCreditLot{CreditEventKey: rebateKey, UserID: user.Id, Source: WalletCreditSourceRebate, Component: "payout", ReferenceType: "rebate", ReferenceID: "no-recursion", QuotaTotal: 10, QuotaRemaining: 10})
	key2 := reservationTestKey(t, user.Id, "rebate-consume")
	require.NoError(t, ReserveWalletQuota(db, key2, user.Id, 10))
	res, err := FinalizeWalletConsumption(db, key2, user.Id, 10, 10)
	require.NoError(t, err)
	require.Zero(t, res.EligibleQuotaConsumed)
	require.NoError(t, db.Model(&RebateRecord{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestWalletLegacyNegativeBaselineAndNonConsumptionDebit(t *testing.T) {
	db, user := reservationTestWallet(t, -30)
	require.NoError(t, InitializeWalletCreditBaseline(db))
	lot := creditRebateTestLot(t, db, reservationTestLot(t, user.Id, "negative-baseline", 50, 500))
	require.EqualValues(t, 20, lot.QuotaRemaining)
	require.EqualValues(t, 200, lot.EligibleMoneyRemaining)
	key := reservationTestKey(t, user.Id, "admin-debit")
	require.NoError(t, AdjustWalletDebit(db, key, user.Id, 25))
	require.NoError(t, AdjustWalletDebit(db, key, user.Id, 25))
	reservationTestQuota(t, db, user.Id, -5)
	var header WalletConsumptionSettlement
	require.NoError(t, db.Where("final_settlement_key = ?", key).First(&header).Error)
	require.EqualValues(t, 5, header.UnallocatedQuota)
	require.Zero(t, header.EligibleQuotaConsumed)
	var count int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&count).Error)
	require.Zero(t, count)
	var total int64
	require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Count(&total).Error)
	require.EqualValues(t, 2, total)
}

func TestWalletDatabaseErrorsNeverMeanInactive(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Migrator().DropTable(&RebateSystemState{}))
	active, err := WalletCreditLedgerActive(db)
	require.Error(t, err)
	require.False(t, active)
}

func TestWalletQuotaLowerBoundAndCreditAtUpperBound(t *testing.T) {
	db, user := reservationTestWallet(t, 0)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", math.MinInt64).Error)
	key := reservationTestKey(t, user.Id, "underflow")
	_, err := FinalizeWalletConsumption(db, key, user.Id, 0, 1)
	require.ErrorIs(t, err, ErrTopUpQuotaOutOfRange)
	reservationTestQuota(t, db, user.Id, math.MinInt64)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", math.MaxInt64).Error)
	_, err = addTestWalletCreditLot(db, reservationTestLot(t, user.Id, "overflow", 1, 1))
	require.Error(t, err)
	reservationTestQuota(t, db, user.Id, math.MaxInt64)
}

func TestWalletRecoveryBatchContinuesAfterFailureAndDoesNotTouchTokens(t *testing.T) {
	db, user := reservationTestWallet(t, 100)
	bad := createRebateTestUser(t, db, 0)
	badKey := reservationTestKey(t, bad.Id, "recovery-deleted")
	require.NoError(t, ReserveWalletQuota(db, badKey, bad.Id, 0))
	require.NoError(t, PrepareWalletFinalization(db, badKey, bad.Id, 0, 1))
	require.NoError(t, db.Delete(&User{}, bad.Id).Error)
	goodKey := reservationTestKey(t, user.Id, "recovery-good")
	require.NoError(t, ReserveWalletQuota(db, goodKey, user.Id, 20))
	require.NoError(t, PrepareWalletFinalization(db, goodKey, user.Id, 20, 15))
	processed, err := RecoverFinalizingWalletSettlements(db, 1)
	require.Error(t, err)
	require.Zero(t, processed)
	processed, err = RecoverFinalizingWalletSettlements(db, 100)
	require.Error(t, err) // failed record remains durable; unrelated one commits
	require.Equal(t, 1, processed)
	reservationTestQuota(t, db, user.Id, 85)
	processed, err = RecoverFinalizingWalletSettlements(db, 100)
	require.Error(t, err)
	require.Zero(t, processed)
	// Test DB has no tokens table: recovery must be purely wallet accounting.
	require.False(t, db.Migrator().HasTable(&Token{}))
}

func TestWalletLegacyAndAdjustmentKeysCannotBecomeReservations(t *testing.T) {
	db, user := reservationTestWallet(t, 100)
	other := createRebateTestUser(t, db, 0)
	key := reservationTestKey(t, user.Id, "legacy-key")
	_, err := FinalizeWalletConsumption(db, key, user.Id, 0, 10)
	require.NoError(t, err)
	require.ErrorIs(t, ReserveWalletQuota(db, key, user.Id, 0), ErrWalletReservationConflict)
	require.ErrorIs(t, ReserveWalletQuota(db, key, other.Id, 0), ErrWalletReservationConflict)
	require.ErrorIs(t, RefundWalletReservation(db, key, user.Id), ErrWalletReservationConflict)
	adjust := reservationTestKey(t, user.Id, "adjust-key")
	require.NoError(t, AdjustWalletDebit(db, adjust, user.Id, 1))
	require.NoError(t, AdjustWalletDebit(db, adjust, user.Id, 1))
	require.ErrorIs(t, ReserveWalletQuota(db, adjust, other.Id, 0), ErrWalletReservationConflict)
	_, err = FinalizeWalletConsumption(db, adjust, user.Id, 0, 1)
	require.Error(t, err)
}

func TestWalletLegacyFinalizeSkipsAllForeignHolds(t *testing.T) {
	db, user := reservationTestWallet(t, 100)
	reserved := reservationTestKey(t, user.Id, "held")
	require.NoError(t, ReserveWalletQuota(db, reserved, user.Id, 80))
	legacy := reservationTestKey(t, user.Id, "unheld-maintenance")
	result, err := FinalizeWalletConsumption(db, legacy, user.Id, 0, 30)
	require.NoError(t, err)
	require.EqualValues(t, 10, result.UnallocatedQuota)
	var hold WalletLotHold
	require.NoError(t, db.Where("reservation_key = ?", reserved).First(&hold).Error)
	require.EqualValues(t, 80, hold.QuotaHeld)
	require.NoError(t, RefundWalletReservation(db, reserved, user.Id))
	reservationTestQuota(t, db, user.Id, 70)
}

func TestWalletPrepareZeroTrustedAfterDebt(t *testing.T) {
	db, user := reservationTestWallet(t, 0)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", -1).Error)
	key := reservationTestKey(t, user.Id, "trusted-zero")
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 0))
	require.NoError(t, PrepareWalletFinalization(db, key, user.Id, 0, 0))
	_, err := FinalizeWalletConsumption(db, key, user.Id, 0, 0)
	require.NoError(t, err)
}
