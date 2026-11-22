package model

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	// Empty mode preserves existing ledger reservations. Legacy reservations
	// never reinterpret the pre-debit as a hold after baseline initialization.
	BillingReservationWallet       = "wallet"
	BillingReservationLegacy       = "legacy"
	BillingReservationSubscription = "subscription"
)

// ErrBillingTokenReserve distinguishes a token rejection from a funding
// shortfall, so it cannot accidentally trigger a wallet/subscription fallback.
var ErrBillingTokenReserve = errors.New("billing token reservation failed")

func billingReservationAfterCommit(db *gorm.DB, userID int, err error) error {
	if err == nil && db == DB {
		if cacheErr := InvalidateUserTokensCache(userID); cacheErr != nil {
			common.SysError(fmt.Sprintf("billing token cache invalidation: %v", cacheErr))
		}
	}
	return walletAfterCommit(db, userID, err)
}

// The reservation state and this adjustment commit together. A fresh child
// identity permits reserve/release/reserve cycles without reusing an old debit.
func adjustLegacyBillingReservationTx(tx *gorm.DB, r *WalletReservation, delta int64, operation string) error {
	if delta > int64(math.MaxInt) || delta < -int64(math.MaxInt) {
		return ErrWalletReservationConflict
	}
	key, err := NewWalletFinalSettlementKey(r.UserID, "billing-"+operation, r.ReservationKey+":"+common.GetUUID())
	if err != nil {
		return err
	}
	return SettleLegacyTaskDeltaTx(tx, key, r.UserID, int(delta))
}

// ReserveBillingWalletFundingTx fixes the accounting mode on the first debit.
// Existing tasks may call it while adjusting their token in the same transaction.
func ReserveBillingWalletFundingTx(tx *gorm.DB, key string, userID int, target int64) error {
	if err := validateWalletReservationInput(tx, key, userID); err != nil {
		return err
	}
	if target < 0 || target > int64(math.MaxInt) {
		return ErrWalletReservationConflict
	}
	active, err := LockWalletCreditBaselineTx(tx)
	if err != nil {
		return err
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	r, err := walletReservationTx(tx, key, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if active {
			if err := ReserveWalletQuotaTx(tx, key, userID, target); err != nil {
				return err
			}
			// Explicit mode also distinguishes an atomic no-token reservation
			// from old rows whose token identity was stored only on the task.
			result := tx.Model(&WalletReservation{}).Where("reservation_key = ? AND user_id = ? AND funding_mode = ?", key, userID, "").UpdateColumn("funding_mode", BillingReservationWallet)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrWalletReservationConflict
			}
			return nil
		}
		var count int64
		if err := tx.Model(&WalletConsumptionSettlement{}).Where("final_settlement_key = ?", key).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrWalletReservationConflict
		}
		r = &WalletReservation{ReservationKey: key, UserID: userID, FundingMode: BillingReservationLegacy, State: WalletReservationReserved, DispatchState: BillingDispatchPending}
		if err := tx.Create(r).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if r.FundingMode == "" || r.FundingMode == BillingReservationWallet {
		return ReserveWalletQuotaTx(tx, key, userID, target)
	}
	if r.FundingMode != BillingReservationLegacy || r.State != WalletReservationReserved || target < r.ReservedQuota {
		return ErrWalletReservationConflict
	}
	delta := target - r.ReservedQuota
	if delta == 0 {
		return nil
	}
	if int64(user.Quota) < delta {
		return ErrWalletInsufficientQuota
	}
	if err := adjustLegacyBillingReservationTx(tx, r, delta, "reserve"); err != nil {
		return err
	}
	result := tx.Model(r).Where("state = ? AND reserved_quota = ?", r.State, r.ReservedQuota).
		Updates(map[string]interface{}{"reserved_quota": target, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}

// ReserveBillingWalletQuota atomically reserves wallet and token even before a
// task exists. TokenID=0 is the durable identity for playground/no-token use.
func ReserveBillingWalletQuota(db *gorm.DB, key string, userID, tokenID int, target int64) error {
	if db == nil || tokenID < 0 {
		return ErrWalletReservationConflict
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := walletLockUserTx(tx, userID); err != nil {
			return err
		}
		var before int64
		r, err := walletReservationTx(tx, key, userID)
		if err == nil {
			if r.TokenID != tokenID || r.TaskKind != "" {
				return ErrWalletReservationConflict
			}
			before = r.ReservedQuota
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := ReserveBillingWalletFundingTx(tx, key, userID, target); err != nil {
			return err
		}
		if err := AdjustTaskTokenTx(tx, userID, tokenID, int(target-before), true); err != nil {
			return fmt.Errorf("%w: %w", ErrBillingTokenReserve, err)
		}
		if r == nil && tokenID > 0 {
			result := tx.Model(&WalletReservation{}).Where("reservation_key = ? AND user_id = ? AND token_id = 0", key, userID).
				UpdateColumn("token_id", tokenID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrWalletReservationConflict
			}
		}
		return nil
	})
	return billingReservationAfterCommit(db, userID, err)
}

func ReserveBillingSubscriptionQuota(db *gorm.DB, key string, userID, tokenID int, amount int64) (*SubscriptionPreConsumeResult, error) {
	if db == nil || amount <= 0 || amount > int64(math.MaxInt) || tokenID < 0 {
		return nil, ErrWalletReservationConflict
	}
	var result SubscriptionPreConsumeResult
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := validateWalletReservationInput(tx, key, userID); err != nil {
			return err
		}
		if _, err := walletLockUserTx(tx, userID); err != nil {
			return err
		}
		now := getDBTimestampTx(tx)
		r, err := walletReservationTx(tx, key, userID)
		if err == nil {
			if r.TaskKind != "" || r.FundingMode != BillingReservationSubscription || r.State != WalletReservationReserved || r.ReservedQuota != amount || r.TokenID != tokenID {
				return ErrWalletReservationConflict
			}
			var sub UserSubscription
			if err := walletCurrentTx(tx).First(&sub, r.SubscriptionID).Error; err != nil {
				return err
			}
			if !subscriptionPeriodMatches(r.PeriodStart, r.CreatedAt.Unix(), &sub) {
				return ErrSubscriptionPeriodChanged
			}
			result = SubscriptionPreConsumeResult{UserSubscriptionId: sub.Id, PreConsumed: amount, PeriodStart: r.PeriodStart, AmountTotal: sub.AmountTotal, AmountUsedBefore: sub.AmountUsed, AmountUsedAfter: sub.AmountUsed}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var subs []UserSubscription
		if err := walletCurrentTx(tx).Where("user_id = ? AND status = ? AND end_time > ?", userID, "active", now).Order("end_time asc, id asc").Find(&subs).Error; err != nil {
			return err
		}
		if len(subs) == 0 {
			return errors.New("no active subscription")
		}
		for _, sub := range subs {
			plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
			if err != nil {
				return err
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
				return err
			}
			ensureSubscriptionQuotaPeriod(&sub, now)
			before := sub.AmountUsed
			if before < 0 || sub.AmountTotal < 0 {
				return errors.New("invalid negative subscription amount")
			}
			if sub.AmountTotal > 0 && sub.AmountTotal-before < amount {
				continue
			}
			sub.AmountUsed, err = addSubscriptionAmountChecked(before, amount)
			if err != nil {
				return err
			}
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			r := &WalletReservation{ReservationKey: key, UserID: userID, TokenID: tokenID, FundingMode: BillingReservationSubscription, SubscriptionID: sub.Id, PeriodStart: currentSubscriptionQuotaPeriodStart(&sub), ReservedQuota: amount, State: WalletReservationReserved, DispatchState: BillingDispatchPending}
			if err := tx.Create(r).Error; err != nil {
				return err
			}
			if err := AdjustTaskTokenTx(tx, userID, tokenID, int(amount), true); err != nil {
				return fmt.Errorf("%w: %w", ErrBillingTokenReserve, err)
			}
			result = SubscriptionPreConsumeResult{UserSubscriptionId: sub.Id, PreConsumed: amount, PeriodStart: r.PeriodStart, AmountTotal: sub.AmountTotal, AmountUsedBefore: before, AmountUsedAfter: sub.AmountUsed}
			return nil
		}
		return fmt.Errorf("subscription quota insufficient, need=%d", amount)
	})
	return &result, billingReservationAfterCommit(db, userID, err)
}

// ResizeBillingReservation is an absolute-target CAS across funding and token.
func ResizeBillingReservation(db *gorm.DB, key string, userID int, expected, target int64) error {
	if db == nil || expected < 0 || target < 0 || target > int64(math.MaxInt) {
		return ErrWalletReservationConflict
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := walletLockUserTx(tx, userID); err != nil {
			return err
		}
		r, err := walletReservationTx(tx, key, userID)
		if err != nil {
			return err
		}
		if r.State != WalletReservationReserved || r.TaskKind != "" {
			return ErrWalletReservationConflict
		}
		if r.ReservedQuota == target {
			return nil
		}
		if r.ReservedQuota != expected {
			return ErrWalletReservationConflict
		}
		delta := target - expected
		if r.FundingMode == BillingReservationSubscription {
			applied, err := adjustUserSubscriptionDeltaTx(tx, r.SubscriptionID, delta, r.PeriodStart, r.CreatedAt.Unix(), true, false)
			if err != nil {
				return err
			}
			if !applied {
				return ErrSubscriptionPeriodChanged
			}
			result := tx.Model(r).Where("reserved_quota = ? AND state = ?", expected, r.State).
				Updates(map[string]interface{}{"reserved_quota": target, "updated_at": time.Now().UTC()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrWalletReservationConflict
			}
		} else if delta < 0 {
			if err := ReleaseWalletReservationToTx(tx, key, userID, expected, target); err != nil {
				return err
			}
		} else if err := ReserveBillingWalletFundingTx(tx, key, userID, target); err != nil {
			return err
		}
		if err := AdjustTaskTokenTx(tx, userID, r.TokenID, int(delta), delta > 0); err != nil {
			return fmt.Errorf("%w: %w", ErrBillingTokenReserve, err)
		}
		return nil
	})
	return billingReservationAfterCommit(db, userID, err)
}

// FinalizeBillingFundingTx is also used by task billing, which has its own
// durable token checkpoint. Non-ledger modes only apply their original delta.
func FinalizeBillingFundingTx(tx *gorm.DB, key string, userID int, pre, actual int64) (int, bool, error) {
	if pre < 0 || actual < 0 || pre > int64(math.MaxInt) || actual > int64(math.MaxInt) {
		return 0, false, ErrWalletReservationConflict
	}
	if _, err := walletLockUserTx(tx, userID); err != nil {
		return 0, false, err
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return 0, false, err
	}
	if r.ReservedQuota != pre {
		return 0, false, ErrWalletReservationConflict
	}
	if r.State == WalletReservationFinalized {
		if r.FinalQuota != actual {
			return 0, false, ErrWalletReservationConflict
		}
		return 0, false, nil
	}
	if (r.State != WalletReservationReserved && r.State != WalletReservationFinalizing) || (r.State == WalletReservationFinalizing && r.FinalQuota != actual) {
		return 0, false, ErrWalletReservationConflict
	}
	if r.FundingMode == "" || r.FundingMode == BillingReservationWallet {
		result, err := FinalizeWalletConsumptionTx(tx, key, userID, pre, actual)
		if err != nil {
			return 0, false, err
		}
		return result.PaidInviterID, true, nil
	}
	applied := true
	switch r.FundingMode {
	case BillingReservationLegacy:
		err = adjustLegacyBillingReservationTx(tx, r, actual-pre, "settle")
	case BillingReservationSubscription:
		delta := actual - pre
		applied, err = adjustUserSubscriptionDeltaTx(tx, r.SubscriptionID, delta, r.PeriodStart, r.CreatedAt.Unix(), delta < 0, delta > 0)
	default:
		err = ErrWalletReservationConflict
	}
	if err != nil {
		return 0, false, err
	}
	result := tx.Model(r).Where("state = ?", r.State).Updates(map[string]interface{}{"state": WalletReservationFinalized, "final_quota": actual, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return 0, false, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, false, ErrWalletReservationConflict
	}
	return 0, applied, nil
}

func FinalizeBillingReservationTx(tx *gorm.DB, key string, userID int, pre, actual int64) (int, bool, error) {
	if _, err := walletLockUserTx(tx, userID); err != nil {
		return 0, false, err
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return 0, false, err
	}
	if r.TaskKind != "" {
		return 0, false, ErrWalletReservationConflict
	}
	inviter, applied, err := FinalizeBillingFundingTx(tx, key, userID, pre, actual)
	if err != nil || r.State == WalletReservationFinalized {
		return inviter, applied, err
	}
	if err := AdjustTaskTokenTx(tx, userID, r.TokenID, int(actual-pre), false); err != nil {
		return 0, false, err
	}
	return inviter, applied, nil
}

func FinalizeBillingReservation(db *gorm.DB, key string, userID int, pre, actual int64) (bool, error) {
	if db == nil {
		return false, ErrWalletReservationConflict
	}
	var inviterID int
	var applied bool
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		inviterID, applied, err = FinalizeBillingReservationTx(tx, key, userID, pre, actual)
		return err
	})
	if err == nil && inviterID > 0 {
		_ = walletAfterCommit(db, inviterID, nil)
	}
	return applied, billingReservationAfterCommit(db, userID, err)
}

func RefundBillingReservation(db *gorm.DB, key string, userID int) error {
	if db == nil {
		return ErrWalletReservationConflict
	}
	err := db.Transaction(func(tx *gorm.DB) error { return RefundPendingTaskReservationTx(tx, key, userID) })
	return billingReservationAfterCommit(db, userID, err)
}

// RenewBillingReservation shares the expiry lock and refreshes only a live
// reservation. Request completion stops renewals; a crashed process loses them.
func RenewBillingReservation(db *gorm.DB, key string, userID int) (bool, error) {
	if db == nil {
		return false, ErrWalletReservationConflict
	}
	live := false
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := walletLockUserTx(tx, userID); err != nil {
			return err
		}
		r, err := walletReservationTx(tx, key, userID)
		if err != nil {
			return err
		}
		if r.State != WalletReservationReserved || r.TaskKind != "" {
			return nil
		}
		result := tx.Model(r).Where("state = ?", WalletReservationReserved).UpdateColumn("updated_at", time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		live = result.RowsAffected == 1
		return nil
	})
	return live, err
}
