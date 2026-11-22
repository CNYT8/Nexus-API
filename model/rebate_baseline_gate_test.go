package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletBaselineGateRejectsOpenLegacyReservationsIncludingZeroDebit(t *testing.T) {
	for _, state := range []string{WalletReservationReserved, WalletReservationFinalizing} {
		t.Run(state, func(t *testing.T) {
			db := newRebateTestDB(t)
			user := createRebateTestUser(t, db, 100)
			key, err := NewWalletFinalSettlementKey(user.Id, "baseline-gate-test", state)
			require.NoError(t, err)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				active, err := LockWalletCreditBaselineTx(tx)
				if err != nil {
					return err
				}
				require.False(t, active)
				return tx.Create(&WalletReservation{
					ReservationKey: key, UserID: user.Id, FundingMode: BillingReservationLegacy,
					State: state, ReservedQuota: 0, UpdatedAt: time.Now().UTC(),
				}).Error
			}))
			require.ErrorIs(t, InitializeWalletCreditBaseline(db), ErrWalletBaselineBusy)
			active, err := WalletCreditLedgerActive(db)
			require.NoError(t, err)
			require.False(t, active)
			var lotCount int64
			require.NoError(t, db.Model(&WalletCreditLot{}).Count(&lotCount).Error)
			require.Zero(t, lotCount)
			// Explicit recovery/completion makes the request terminal. A timeout
			// alone must never authorize initialization while it can still write.
			require.NoError(t, db.Model(&WalletReservation{}).Where("reservation_key = ?", key).
				UpdateColumn("state", WalletReservationRefunded).Error)
			require.NoError(t, InitializeWalletCreditBaseline(db))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				active, err := LockWalletCreditBaselineTx(tx)
				require.True(t, active)
				return err
			}))
			require.NoError(t, InitializeWalletCreditBaseline(db))
			require.NoError(t, db.Model(&WalletCreditLot{}).Count(&lotCount).Error)
			require.EqualValues(t, 1, lotCount)
		})
	}
}

func TestWalletBaselineGateFailsClosedOnIncompleteMarkerAndDatabaseError(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{
		Key: walletCreditBaselineStateKey, Value: "running:interrupted", UpdatedAt: time.Now().UTC(),
	}).Error)
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := LockWalletCreditBaselineTx(tx)
		return err
	}), "wallet credit baseline initialization is incomplete")
	require.NoError(t, db.Migrator().DropTable(&RebateSystemState{}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := LockWalletCreditBaselineTx(tx)
		return err
	}))
	_, err := LockWalletCreditBaselineTx(nil)
	require.Error(t, err)
}
