package model

import (
	"time"

	"gorm.io/gorm"
)

// MarkBillingReservationDispatched 将发送意图持久化后才允许访问上游。
// 与过期退款共享用户锁；重复调用不能将已终结的预扣重新激活。
func MarkBillingReservationDispatched(db *gorm.DB, key string, userID int) error {
	if err := validateWalletReservationInput(db, key, userID); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if _, err := walletLockUserTx(tx, userID); err != nil {
			return err
		}
		r, err := walletReservationTx(tx, key, userID)
		if err != nil {
			return err
		}
		if r.State != WalletReservationReserved {
			return ErrWalletReservationConflict
		}
		if r.DispatchState == BillingDispatchStarted {
			return nil
		}
		if r.DispatchState != "" && r.DispatchState != BillingDispatchPending {
			return ErrWalletReservationConflict
		}
		result := tx.Model(r).Where("state = ? AND dispatch_state = ?", WalletReservationReserved, r.DispatchState).
			Updates(map[string]interface{}{"dispatch_state": BillingDispatchStarted, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWalletReservationConflict
		}
		return nil
	})
}
