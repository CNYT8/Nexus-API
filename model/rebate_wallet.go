package model

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// walletCreditBaselineState reports whether source-attributed wallet
// accounting has been initialized. It must be evaluated inside the same
// transaction that writes a credit, so an incomplete initialization can never
// be raced. A missing marker means accounting was never enabled and legacy
// quota-only writers stay authoritative.
func walletCreditBaselineState(tx *gorm.DB) (bool, error) {
	if tx == nil {
		return false, errors.New("wallet credit baseline requires a transaction")
	}
	var baseline RebateSystemState
	// Map conditions are quoted by GORM per dialect: `key` is a reserved word in
	// MySQL, so a raw "key = ?" predicate is a syntax error there.
	err := tx.Where(map[string]interface{}{"key": walletCreditBaselineStateKey}).First(&baseline).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		// Missing tables, connection errors and permission errors are not an
		// inactive ledger. Migrations must finish before writers are enabled.
		return false, err
	}
	if !strings.HasPrefix(baseline.Value, "initialized:") {
		return false, errors.New("wallet credit baseline initialization is incomplete")
	}
	return true, nil
}

// WalletCreditLedgerActive reports whether source-attributed wallet accounting
// is initialized. Callers use it to choose between the legacy quota-only path
// and the ledger-backed settlement path.
func WalletCreditLedgerActive(db *gorm.DB) (bool, error) {
	return walletCreditBaselineState(db)
}

// CreditTopUpTx credits one successful top-up order. Before the wallet
// baseline is initialized it keeps the legacy quota-only behavior so
// deployments roll out in order (stop writers, initialize baseline, restart
// compatible writers). Afterwards the credit becomes an eligible paid source
// lot in the same transaction as the wallet increase.
func CreditTopUpTx(tx *gorm.DB, userID int, referenceID string, quota int) (bool, error) {
	if quota <= 0 {
		return false, ErrInvalidTopUpQuota
	}
	active, err := walletCreditBaselineState(tx)
	if err != nil {
		return false, err
	}
	if !active {
		if _, err := walletLockUserTx(tx, userID); err != nil {
			return false, err
		}
		// Preserve pre-activation top-up policy/sentinels; lot accounting uses
		// actual column bounds only after the explicit baseline cutover.
		if err := addUserQuotaTx(tx, userID, quota); err != nil {
			return false, err
		}
		return true, nil
	}
	lot, err := newTopUpWalletLot(userID, referenceID, quota)
	if err != nil {
		return false, err
	}
	return AddWalletCreditLotTx(tx, lot)
}

// newTopUpWalletLot builds the paid, eligible source credit for one top-up
// order. The quota-ratio valuation snapshot is taken at credit time so later
// site ratio changes cannot revalue historical consumption.
func newTopUpWalletLot(userID int, referenceID string, quota int) (WalletCreditLot, error) {
	if userID <= 0 || strings.TrimSpace(referenceID) == "" || quota <= 0 {
		return WalletCreditLot{}, errors.New("invalid top-up wallet credit")
	}
	moneyMicros, ratioMicros, err := EligibleMoneyValuationForQuota(int64(quota))
	if err != nil {
		return WalletCreditLot{}, err
	}
	key, err := NewWalletCreditEventKey(userID, WalletCreditSourceTopUp, "topup", referenceID, "paid")
	if err != nil {
		return WalletCreditLot{}, err
	}
	return WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 userID,
		Source:                 WalletCreditSourceTopUp,
		Component:              "paid",
		Eligible:               true,
		ReferenceType:          "topup",
		ReferenceID:            referenceID,
		QuotaTotal:             int64(quota),
		QuotaRemaining:         int64(quota),
		EligibleMoneyMicros:    moneyMicros,
		EligibleMoneyRemaining: moneyMicros,
		QuotaPerUnitMicros:     ratioMicros,
	}, nil
}
