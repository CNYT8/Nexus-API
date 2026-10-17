package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// WalletDebtRepayment burns credit against an earlier uncovered debit. Money
// removed here is an audit value ONLY, never a second eligible consumption.
type WalletDebtRepayment struct {
	ID                 int64     `gorm:"primaryKey"`
	LotID              int64     `gorm:"not null;uniqueIndex:ux_wallet_debt_lot_settlement,priority:1"`
	FinalSettlementKey string    `gorm:"type:char(64);not null;uniqueIndex:ux_wallet_debt_lot_settlement,priority:2"`
	UserID             int       `gorm:"not null;index"`
	QuotaRepaid        int64     `gorm:"not null"`
	MoneyRetiredMicros int64     `gorm:"not null;default:0"`
	CreatedAt          time.Time `gorm:"not null"`
}

// repayWalletDebtTx runs under the credit recipient's user lock. It does not
// derive debt/eligibility from a negative users.quota, and never invokes reward
// creation. The new credit's entire face value still increases users.quota;
// only the part left AFTER debt becomes a spendable source lot.
func repayWalletDebtTx(tx *gorm.DB, lot *WalletCreditLot) error {
	var debts []WalletConsumptionSettlement
	if err := walletCurrentTx(tx).Where("user_id = ? AND unallocated_quota > debt_repaid_quota", lot.UserID).Order("finalized_at ASC, id ASC").Find(&debts).Error; err != nil {
		return err
	}
	for _, debt := range debts {
		if lot.QuotaRemaining == 0 {
			break
		}
		if debt.DebtRepaidQuota < 0 || debt.UnallocatedQuota < debt.DebtRepaidQuota {
			return errors.New("invalid persisted wallet debt")
		}
		n := debt.UnallocatedQuota - debt.DebtRepaidQuota
		if n > lot.QuotaRemaining {
			n = lot.QuotaRemaining
		}
		money, err := walletFloorProduct(n, lot.EligibleMoneyRemaining, lot.QuotaRemaining)
		if err != nil {
			return err
		}
		// Exact exhaustion transfers all remaining micros, including floor dust.
		if n == lot.QuotaRemaining {
			money = lot.EligibleMoneyRemaining
		}
		lr := tx.Model(&WalletCreditLot{}).Where("id = ? AND user_id = ? AND quota_remaining = ? AND eligible_money_remaining = ?", lot.ID, lot.UserID, lot.QuotaRemaining, lot.EligibleMoneyRemaining).
			Updates(map[string]interface{}{"quota_remaining": lot.QuotaRemaining - n, "eligible_money_remaining": lot.EligibleMoneyRemaining - money, "updated_at": time.Now().UTC()})
		if lr.Error != nil {
			return lr.Error
		}
		if lr.RowsAffected != 1 {
			return errors.New("wallet debt credit changed")
		}
		dr := tx.Model(&WalletConsumptionSettlement{}).Where("id = ? AND user_id = ? AND unallocated_quota = ? AND debt_repaid_quota = ?", debt.ID, lot.UserID, debt.UnallocatedQuota, debt.DebtRepaidQuota).UpdateColumn("debt_repaid_quota", debt.DebtRepaidQuota+n)
		if dr.Error != nil {
			return dr.Error
		}
		if dr.RowsAffected != 1 {
			return errors.New("wallet debt changed")
		}
		link := WalletDebtRepayment{LotID: lot.ID, FinalSettlementKey: debt.FinalSettlementKey, UserID: lot.UserID, QuotaRepaid: n, MoneyRetiredMicros: money, CreatedAt: time.Now().UTC()}
		res := tx.Create(&link)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("wallet debt repayment insert failed")
		}
		lot.QuotaRemaining -= n
		lot.EligibleMoneyRemaining -= money
	}
	return nil
}

// AdjustWalletDebit is for admin/legacy NON-consumption debits. Callers supply
// a server-owned unique mutation key. Never route consumption through this API,
// and never route admin adjustments through FinalizeWalletConsumption.
func AdjustWalletDebit(db *gorm.DB, key string, userID int, quota int64) error {
	if db == nil {
		return errors.New("wallet adjustment requires a database")
	}
	err := db.Transaction(func(tx *gorm.DB) error { return AdjustWalletDebitTx(tx, key, userID, quota) })
	return walletAfterCommit(db, userID, err)
}

func AdjustWalletDebitTx(tx *gorm.DB, key string, userID int, quota int64) error {
	if err := validateWalletReservationInput(tx, key, userID); err != nil {
		return err
	}
	if quota < 0 {
		return ErrInvalidTopUpQuota
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	active, err := walletCreditBaselineState(tx)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("wallet credit baseline is not initialized")
	}
	reservation, reservationErr := walletReservationTx(tx, key, userID)
	if reservationErr != nil && !errors.Is(reservationErr, gorm.ErrRecordNotFound) {
		return reservationErr
	}
	var old WalletConsumptionSettlement
	err = walletCurrentTx(tx).Where("final_settlement_key = ?", key).First(&old).Error
	if err == nil {
		if old.UserID != userID || old.WalletQuota != quota || old.AccountingKind != "adjustment" {
			return ErrWalletReservationConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if reservation != nil {
		return ErrWalletReservationConflict
	}
	if err := walletBindTerminalKeyTx(tx, key, userID, 0, quota); err != nil {
		return err
	}
	if err := walletChangeQuotaTx(tx, user, -quota, false); err != nil {
		return err
	}
	allocated, _, _, err := allocateWalletLotsTx(tx, key, userID, quota)
	if err != nil {
		return err
	}
	// Allocation valuation is auditable retirement only. No settlement eligible
	// totals and no reward call are created for administrative mutations.
	header := WalletConsumptionSettlement{FinalSettlementKey: key, UserID: userID, WalletQuota: quota, UnallocatedQuota: quota - allocated, AccountingKind: "adjustment", FinalizedAt: time.Now().UTC()}
	res := tx.Create(&header)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}
