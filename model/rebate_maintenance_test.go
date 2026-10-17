package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReconcileWalletCreditBatchMatchesLedgerInvariant(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)

	healthy := createRebateTestUser(t, db, 0)
	credited, err := CreditTopUpTx(db, healthy.Id, "trade-reconcile", 100)
	require.NoError(t, err)
	require.True(t, credited)
	key, err := NewWalletFinalSettlementKey(healthy.Id, "relay", "reconcile-1")
	require.NoError(t, err)
	_, err = FinalizeWalletConsumption(db, key, healthy.Id, 0, 40)
	require.NoError(t, err)

	// A balance changed outside the ledger must be reported, never repaired.
	drifted := createRebateTestUser(t, db, 0)
	credited, err = CreditTopUpTx(db, drifted.Id, "trade-drift", 50)
	require.NoError(t, err)
	require.True(t, credited)
	require.NoError(t, db.Model(&User{}).Where("id = ?", drifted.Id).UpdateColumn("quota", 80).Error)

	mismatches, next, done, err := ReconcileWalletCreditBatch(db, 0, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, drifted.Id, next)
	require.Len(t, mismatches, 1)
	require.Equal(t, drifted.Id, mismatches[0].UserID)
	require.EqualValues(t, 80, mismatches[0].Quota)
	require.EqualValues(t, 50, mismatches[0].LotRemaining)
	require.Zero(t, mismatches[0].OpenDebt)
	require.EqualValues(t, 50, mismatches[0].ExpectedQuota)

	// Debt that has already been repaid by later credit is not open debt.
	mismatches, _, _, err = ReconcileWalletCreditBatch(db, 0, 100)
	require.NoError(t, err)
	require.Len(t, mismatches, 1)
}

func TestExpireStaleWalletReservationsRefundsOnlyAbandonedOnes(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)
	credited, err := CreditTopUpTx(db, user.Id, "trade-stale", 100)
	require.NoError(t, err)
	require.True(t, credited)

	staleKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "stale-1")
	require.NoError(t, err)
	activeKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "stale-2")
	require.NoError(t, err)
	require.NoError(t, ReserveWalletQuota(db, staleKey, user.Id, 30))
	require.NoError(t, ReserveWalletQuota(db, activeKey, user.Id, 20))
	require.Equal(t, 50, userQuota(t, db, user.Id))

	cutoff := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, db.Model(&WalletReservation{}).Where("reservation_key = ?", staleKey).UpdateColumn("updated_at", cutoff.Add(-time.Minute)).Error)
	refunded, err := ExpireStaleWalletReservations(db, cutoff, 10)
	require.NoError(t, err)
	require.Equal(t, 1, refunded)
	require.Equal(t, 80, userQuota(t, db, user.Id))

	var stale, active WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", staleKey).First(&stale).Error)
	require.Equal(t, WalletReservationRefunded, stale.State)
	require.NoError(t, db.Where("reservation_key = ?", activeKey).First(&active).Error)
	require.Equal(t, WalletReservationReserved, active.State)

	var holds []WalletLotHold
	require.NoError(t, db.Where("reservation_key = ? AND quota_held > 0", staleKey).Find(&holds).Error)
	require.Empty(t, holds)

	// Repeated sweeps are no-ops, and a non-refundable finalizing barrier is
	// never expired.
	require.NoError(t, PrepareWalletFinalization(db, activeKey, user.Id, 20, 5))
	refunded, err = ExpireStaleWalletReservations(db, cutoff, 10)
	require.NoError(t, err)
	require.Zero(t, refunded)
	require.Equal(t, 80, userQuota(t, db, user.Id))
}

func userQuota(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, db.First(&user, userID).Error)
	return user.Quota
}

func TestLedgerTreatsLegacyNullQuotaAsZero(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)
	require.NoError(t, db.Exec("UPDATE users SET quota = NULL WHERE id = ?", user.Id).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		applied, err := CreditWalletSourceTx(tx, user.Id, WalletCreditSourcePromotion, "compensation", "admin", "null-quota", 250)
		require.True(t, applied)
		return err
	})
	require.NoError(t, err)
	require.Equal(t, 250, userQuota(t, db, user.Id))

	mismatches, _, done, err := ReconcileWalletCreditBatch(db, 0, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, mismatches)
}

func TestReconcileWalletCreditBatchHandlesOpenDebt(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)

	key, err := NewWalletFinalSettlementKey(user.Id, "relay", "debt-1")
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(db, key, user.Id, 0, 30)
	require.NoError(t, err)
	require.EqualValues(t, 30, result.UnallocatedQuota)

	mismatches, _, done, err := ReconcileWalletCreditBatch(db, 0, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, mismatches)

	// Repaying the debt keeps the invariant: quota and lots move together.
	credited, err := CreditTopUpTx(db, user.Id, "trade-debt", 45)
	require.NoError(t, err)
	require.True(t, credited)
	mismatches, _, _, err = ReconcileWalletCreditBatch(db, 0, 10)
	require.NoError(t, err)
	require.Empty(t, mismatches)
	var saved User
	require.NoError(t, db.First(&saved, user.Id).Error)
	require.Equal(t, 15, saved.Quota)

	// An active reservation holds lot quota while already debiting users.quota;
	// reconciliation must account for active holds and not report false drift.
	resKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "debt-hold-1")
	require.NoError(t, err)
	require.NoError(t, ReserveWalletQuota(db, resKey, user.Id, 10))
	mismatches, _, _, err = ReconcileWalletCreditBatch(db, 0, 10)
	require.NoError(t, err)
	require.Empty(t, mismatches)
}
