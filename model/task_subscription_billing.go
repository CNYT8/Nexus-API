package model

import (
	"math"
	"time"

	"gorm.io/gorm"
)

// ValidateSubscriptionTaskReservation requires the same durable identity on
// both records. Older subscription tasks without that evidence are not safe to
// replay: their funding/token writes could already have committed separately.
func ValidateSubscriptionTaskReservation(task *Task, r *WalletReservation, requireOwner bool) error {
	if task == nil || r == nil || task.ID <= 0 || task.Quota < 0 ||
		task.PrivateData.BillingSource != "subscription" || task.PrivateData.BillingKey == "" ||
		task.PrivateData.BillingKey != r.ReservationKey || task.UserId != r.UserID ||
		r.FundingMode != BillingReservationSubscription || r.SubscriptionID <= 0 ||
		task.PrivateData.SubscriptionId != r.SubscriptionID || r.PeriodStart <= 0 ||
		task.PrivateData.SubscriptionPeriodStart != r.PeriodStart ||
		r.ReservedQuota < 0 || r.ReservedQuota > int64(math.MaxInt) || r.TokenID < 0 ||
		(r.TokenID > 0 && task.PrivateData.TokenId != r.TokenID) {
		return ErrWalletReservationConflict
	}
	if requireOwner && (r.TaskKind != "task" || r.TaskID != task.ID) {
		return ErrWalletReservationConflict
	}
	return nil
}

// alignSubscriptionTaskReservationTx runs after task creation, in that same
// transaction. Submit-time actual usage may exceed the plan/token quota, just
// as ordinary subscription settlement does. A failed insert/bind/token update
// rolls the adjustment back and leaves the original pending debit refundable.
func alignSubscriptionTaskReservationTx(tx *gorm.DB, task *Task, r *WalletReservation) error {
	if err := ValidateSubscriptionTaskReservation(task, r, false); err != nil {
		return err
	}
	if r.State != WalletReservationReserved || r.TaskKind != "" || r.TaskID != 0 {
		return ErrWalletReservationConflict
	}
	var sub UserSubscription
	if err := walletCurrentTx(tx).Where("id = ? AND user_id = ?", r.SubscriptionID, r.UserID).First(&sub).Error; err != nil {
		return err
	}
	delta := int64(task.Quota) - r.ReservedQuota
	if delta == 0 {
		return nil
	}
	if _, err := adjustUserSubscriptionDeltaTx(tx, r.SubscriptionID, delta, r.PeriodStart, r.CreatedAt.Unix(), delta < 0, delta > 0); err != nil {
		return err
	}
	if err := AdjustTaskTokenTx(tx, r.UserID, r.TokenID, int(delta), false); err != nil {
		return err
	}
	result := tx.Model(&WalletReservation{}).Where("id = ? AND state = ? AND reserved_quota = ? AND task_kind = ? AND task_id = ?", r.ID, r.State, r.ReservedQuota, "", 0).
		Updates(map[string]interface{}{"reserved_quota": task.Quota, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}

// SettleSubscriptionTaskBillingTx adjusts funding and the persisted token. The
// caller holds the user/task locks and commits the task's done marker in this
// transaction. false means a refund belonged to an expired subscription period;
// token quota remains lifetime-scoped and is still adjusted exactly once.
func SettleSubscriptionTaskBillingTx(tx *gorm.DB, task *Task, actual int, refund bool) (bool, error) {
	if tx == nil || task == nil || actual < 0 || (refund && actual != 0) {
		return false, ErrWalletReservationConflict
	}
	r, err := walletReservationTx(tx, task.PrivateData.BillingKey, task.UserId)
	if err != nil {
		return false, err
	}
	if err := ValidateSubscriptionTaskReservation(task, r, true); err != nil {
		return false, err
	}
	if r.ReservedQuota != int64(task.Quota) ||
		(r.State != WalletReservationReserved && r.State != WalletReservationFinalizing) {
		return false, ErrWalletReservationConflict
	}
	var sub UserSubscription
	if err := walletCurrentTx(tx).Where("id = ? AND user_id = ?", r.SubscriptionID, r.UserID).First(&sub).Error; err != nil {
		return false, err
	}
	fundingApplied := true
	if refund {
		fundingApplied = r.ReservedQuota == 0 || subscriptionPeriodMatches(r.PeriodStart, r.CreatedAt.Unix(), &sub)
		err = RefundWalletReservationTx(tx, r.ReservationKey, r.UserID)
	} else {
		_, fundingApplied, err = FinalizeBillingFundingTx(tx, r.ReservationKey, r.UserID, r.ReservedQuota, int64(actual))
	}
	if err != nil {
		return false, err
	}
	if err := AdjustTaskTokenTx(tx, r.UserID, r.TokenID, actual-task.Quota, false); err != nil {
		return false, err
	}
	return fundingApplied, nil
}
