package model

import (
	"errors"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	TaskBillingPending = "pending"
	TaskBillingDone    = "done"
	TaskBillingSkipped = "skipped"
)

// LockTaskBillingUserTx establishes the common baseline gate -> User -> task ->
// reservation -> token lock order. All callers must already own a database transaction.
func LockTaskBillingUserTx(tx *gorm.DB, userID int) error {
	_, err := walletLockUserTx(tx, userID)
	return err
}

// SettleLegacyTaskDeltaTx handles a debit that predates ledger activation.
// Its pre-consumption is already absent from the baseline: only the difference
// may move current lots. Historical usage never earns a retrospective rebate.
// The caller locks the durable task and commits its billing marker atomically.
func SettleLegacyTaskDeltaTx(tx *gorm.DB, key string, userID, delta int) error {
	active, err := LockWalletCreditBaselineTx(tx)
	if err != nil {
		return err
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	if !active {
		return walletChangeQuotaTx(tx, user, -int64(delta), false)
	}
	if delta > 0 {
		return AdjustWalletDebitTx(tx, key, userID, int64(delta))
	}
	if delta < 0 {
		_, err := CreditWalletSourceTx(tx, userID, WalletCreditSourceLegacy, "legacy_baseline", "legacy_task", key, -delta)
		return err
	}
	return nil
}

// WalletReservationTaskOwnerTx also recognizes pre-upgrade rows without an
// owner column. It deliberately avoids dialect-specific JSON operators.
// Caller holds the user lock, so task binding and expiry cannot cross.
func WalletReservationTaskOwnerTx(tx *gorm.DB, r *WalletReservation) (bool, error) {
	if r.TaskKind != "" {
		return true, nil
	}
	if tx.Migrator().HasTable(&Task{}) {
		var tasks []Task
		if err := walletCurrentTx(tx).Where("user_id = ?", r.UserID).Find(&tasks).Error; err != nil {
			return false, err
		}
		for _, task := range tasks {
			key := task.PrivateData.BillingKey
			if key == "" {
				key, _ = NewWalletFinalSettlementKey(task.UserId, "task", strconv.FormatInt(task.ID, 10))
			}
			if key == r.ReservationKey {
				return true, nil
			}
		}
	}
	if tx.Migrator().HasTable(&Midjourney{}) {
		var count int64
		if err := tx.Model(&Midjourney{}).Where("user_id = ? AND billing_key = ?", r.UserID, r.ReservationKey).Count(&count).Error; err != nil {
			return false, err
		}
		return count != 0, nil
	}
	return false, nil
}

// AdjustTaskTokenTx runs after the user/task/reservation locks. A deleted token
// cannot be spent again, so its absence must not block a wallet refund.
func AdjustTaskTokenTx(tx *gorm.DB, userID, tokenID, delta int, reserve bool) error {
	if tokenID <= 0 || delta == 0 {
		return nil
	}
	var token Token
	if err := walletCurrentTx(tx).Where("id = ?", tokenID).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) && !reserve {
			return nil
		}
		return err
	}
	if token.UserId != userID {
		return errors.New("task token ownership mismatch")
	}
	if (delta > 0 && (token.RemainQuota < math.MinInt+delta || token.UsedQuota > math.MaxInt-delta)) ||
		(delta < 0 && (token.RemainQuota > math.MaxInt+delta || token.UsedQuota < math.MinInt-delta)) {
		return errors.New("task token quota adjustment overflows")
	}
	remaining, used := token.RemainQuota-delta, token.UsedQuota+delta
	if reserve && !token.UnlimitedQuota && remaining < 0 {
		return ErrInsufficientQuota
	}
	r := tx.Model(&Token{}).
		Where("id = ? AND user_id = ? AND remain_quota = ? AND used_quota = ?", token.Id, userID, token.RemainQuota, token.UsedQuota).
		Updates(map[string]interface{}{"remain_quota": remaining, "used_quota": used, "accessed_time": common.GetTimestamp()})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return errors.New("task token quota changed during billing")
	}
	return nil
}

// BillingReservationTokenID preserves the historical task-only token identity,
// while explicit funding modes treat TokenID=0 as intentional no-token use.
func BillingReservationTokenID(r *WalletReservation, fallback int) int {
	if r != nil && (r.FundingMode != "" || r.TokenID > 0) {
		return r.TokenID
	}
	return fallback
}

// RefundPendingTaskReservationTx is also used by TTL recovery. The persisted
// token identity makes a crash before the task insert refundable on both sides.
func RefundPendingTaskReservationTx(tx *gorm.DB, key string, userID int) error {
	if err := LockTaskBillingUserTx(tx, userID); err != nil {
		return err
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return err
	}
	owned, err := WalletReservationTaskOwnerTx(tx, r)
	if err != nil {
		return err
	}
	if owned {
		return ErrWalletReservationConflict
	}
	if r.State == WalletReservationRefunded {
		return nil
	}
	if r.ReservedQuota < 0 || r.ReservedQuota > int64(math.MaxInt) {
		return ErrWalletReservationConflict
	}
	if err := RefundWalletReservationTx(tx, key, userID); err != nil {
		return err
	}
	return AdjustTaskTokenTx(tx, userID, r.TokenID, -int(r.ReservedQuota), false)
}

func BindWalletReservationTaskTx(tx *gorm.DB, key string, userID int, kind string, id int64) error {
	if kind == "" || id <= 0 {
		return errors.New("invalid task reservation owner")
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return err
	}
	if r.TaskKind == kind && r.TaskID == id {
		return nil
	}
	if r.State == WalletReservationRefunded || r.TaskKind != "" || r.TaskID != 0 {
		return ErrWalletReservationConflict
	}
	if r.FundingMode == BillingReservationSubscription {
		if kind != "task" {
			return ErrWalletReservationConflict
		}
		var task Task
		if err := walletCurrentTx(tx).Where("id = ? AND user_id = ?", id, userID).First(&task).Error; err != nil {
			return err
		}
		if err := alignSubscriptionTaskReservationTx(tx, &task, r); err != nil {
			return err
		}
	}
	result := tx.Model(&WalletReservation{}).Where("id = ? AND task_kind = ? AND task_id = ?", r.ID, "", 0).
		Updates(map[string]interface{}{"task_kind": kind, "task_id": id})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}
