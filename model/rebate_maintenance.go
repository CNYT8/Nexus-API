package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// WalletCreditReconciliation identifies one user whose wallet balance no longer
// equals the source lots minus open unallocated debt.
type WalletCreditReconciliation struct {
	UserID        int   `json:"user_id"`
	Quota         int64 `json:"quota"`
	LotRemaining  int64 `json:"lot_remaining"`
	HeldQuota     int64 `json:"held_quota"`
	OpenDebt      int64 `json:"open_debt"`
	ExpectedQuota int64 `json:"expected_quota"`
}

// ReconcileWalletCreditBatch verifies the wallet credit invariant for one batch
// of users ordered by id:
//
//	users.quota == SUM(lot.quota_remaining) - SUM(active hold.quota_held) - SUM(open unallocated debt)
//
// It is a read-only maintenance check. Run it with writers stopped; a mismatch
// means an unmigrated writer changed the wallet outside the ledger and MUST be
// resolved before enabling rebates.
func ReconcileWalletCreditBatch(db *gorm.DB, afterUserID, batchSize int) ([]WalletCreditReconciliation, int, bool, error) {
	if db == nil {
		return nil, afterUserID, false, errors.New("wallet reconciliation requires a database")
	}
	if afterUserID < 0 || batchSize <= 0 || batchSize > 5000 {
		return nil, afterUserID, false, errors.New("invalid wallet reconciliation input")
	}
	var users []walletBaselineUser
	if err := db.Model(&User{}).
		Select("id", "COALESCE(quota, 0) AS quota").
		Where("id > ?", afterUserID).
		Order("id ASC").
		Limit(batchSize).
		Find(&users).Error; err != nil {
		return nil, afterUserID, false, err
	}
	if len(users) == 0 {
		return nil, afterUserID, true, nil
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}

	type aggregate struct {
		UserID int   `gorm:"column:user_id"`
		Total  int64 `gorm:"column:total"`
	}
	lots := make([]aggregate, 0, len(ids))
	if err := db.Model(&WalletCreditLot{}).
		Select("user_id, COALESCE(SUM(quota_remaining), 0) AS total").
		Where("user_id IN ?", ids).
		Group("user_id").
		Scan(&lots).Error; err != nil {
		return nil, afterUserID, false, err
	}
	holds := make([]aggregate, 0, len(ids))
	if err := db.Model(&WalletLotHold{}).
		Select("user_id, COALESCE(SUM(quota_held), 0) AS total").
		Where("user_id IN ? AND quota_held > 0", ids).
		Group("user_id").
		Scan(&holds).Error; err != nil {
		return nil, afterUserID, false, err
	}
	debts := make([]aggregate, 0, len(ids))
	if err := db.Model(&WalletConsumptionSettlement{}).
		Select("user_id, COALESCE(SUM(unallocated_quota - debt_repaid_quota), 0) AS total").
		Where("user_id IN ? AND unallocated_quota > debt_repaid_quota", ids).
		Group("user_id").
		Scan(&debts).Error; err != nil {
		return nil, afterUserID, false, err
	}
	lotByUser := make(map[int]int64, len(lots))
	for _, row := range lots {
		lotByUser[row.UserID] = row.Total
	}
	holdByUser := make(map[int]int64, len(holds))
	for _, row := range holds {
		holdByUser[row.UserID] = row.Total
	}
	debtByUser := make(map[int]int64, len(debts))
	for _, row := range debts {
		debtByUser[row.UserID] = row.Total
	}

	mismatches := make([]WalletCreditReconciliation, 0)
	for _, user := range users {
		lotRemaining := lotByUser[user.ID]
		heldQuota := holdByUser[user.ID]
		openDebt := debtByUser[user.ID]
		expected := lotRemaining - heldQuota - openDebt
		if int64(user.Quota) != expected {
			mismatches = append(mismatches, WalletCreditReconciliation{
				UserID:        user.ID,
				Quota:         int64(user.Quota),
				LotRemaining:  lotRemaining,
				HeldQuota:     heldQuota,
				OpenDebt:      openDebt,
				ExpectedQuota: expected,
			})
		}
	}
	done := len(users) < batchSize
	return mismatches, users[len(users)-1].ID, done, nil
}

// ExpireStaleWalletReservations refunds reservations that were never settled or
// refunded, for example because the process crashed mid-request. A reservation
// debits the wallet the moment it is created, so without this sweep an
// interrupted request would permanently reduce the user's balance.
//
// The TTL must be far longer than any legitimate request (including streaming),
// so an in-flight request is never expired in practice. Errors on individual
// rows are collected and do not stop the sweep; concurrently finalized rows are
// skipped instead of reported.
func ExpireStaleWalletReservations(db *gorm.DB, olderThan time.Time, maxRecords int) (int, error) {
	if db == nil || olderThan.IsZero() || maxRecords <= 0 {
		return 0, errors.New("invalid wallet reservation expiry input")
	}
	var stale []WalletReservation
	if err := db.Where("state = ? AND updated_at < ?", WalletReservationReserved, olderThan).
		Order("updated_at ASC, id ASC").Limit(maxRecords).Find(&stale).Error; err != nil {
		return 0, err
	}
	refunded := 0
	var failures []error
	for _, reservation := range stale {
		// Re-check the same timestamp and state under the user lock. A request
		// may have renewed the reservation after the scan but before refund.
		err := db.Transaction(func(tx *gorm.DB) error {
			if _, err := walletLockUserTx(tx, reservation.UserID); err != nil {
				return err
			}
			var current WalletReservation
			if err := tx.Where("id = ? AND reservation_key = ? AND user_id = ? AND state = ? AND updated_at < ?", reservation.ID, reservation.ReservationKey, reservation.UserID, WalletReservationReserved, olderThan).First(&current).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrWalletReservationConflict
				}
				return err
			}
			return RefundWalletReservationTx(tx, current.ReservationKey, current.UserID)
		})
		switch {
		case err == nil:
			refunded++
		case errors.Is(err, ErrWalletReservationConflict):
			// Another worker finalized or refunded it first.
		default:
			failures = append(failures, err)
		}
	}
	return refunded, errors.Join(failures...)
}
