package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const walletCreditBaselineGateKey = "wallet_credit_baseline_gate_v1"

var ErrWalletBaselineBusy = errors.New("钱包仍有未结束的旧模式请求，请等待请求完成后重试初始化")

// LockWalletCreditBaselineTx must run before locking users, reservations or
// tokens in a wallet-writing transaction. Until activation, its database write
// barrier remains held until that transaction commits or rolls back. Once the
// irreversible initialized marker exists, no global writer lock is necessary.
func LockWalletCreditBaselineTx(tx *gorm.DB) (bool, error) {
	return lockWalletCreditBaselineTx(tx, true)
}

func lockWalletCreditBaselineTx(tx *gorm.DB, allowActiveFastPath bool) (bool, error) {
	if tx == nil {
		return false, errors.New("wallet credit baseline requires a transaction")
	}
	readState := func(locking bool) (bool, error) {
		var marker RebateSystemState
		query := tx.Where(map[string]interface{}{"key": walletCreditBaselineStateKey})
		if locking && tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		err := query.First(&marker).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !strings.HasPrefix(marker.Value, "initialized:") {
			return false, errors.New("wallet credit baseline initialization is incomplete")
		}
		return true, nil
	}
	// SQLite must obtain its write lock before reading; otherwise a concurrent
	// commit can make upgrading a deferred transaction fail with BUSY_SNAPSHOT.
	if allowActiveFastPath && tx.Dialector.Name() != "sqlite" {
		active, err := readState(false)
		if err != nil || active {
			return active, err
		}
	}
	barrier := RebateSystemState{Key: walletCreditBaselineGateKey, Value: "1", UpdatedAt: time.Now().UTC()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&barrier).Error; err != nil {
		return false, err
	}
	if err := tx.Model(&RebateSystemState{}).
		Where(map[string]interface{}{"key": walletCreditBaselineGateKey}).
		UpdateColumn("value", gorm.Expr("value")).Error; err != nil {
		return false, err
	}
	// A locking read bypasses an older MySQL REPEATABLE READ snapshot after
	// waiting for an initializer or another old-mode writer to release the gate.
	return readState(true)
}

// Called only while the initializer holds the baseline gate. Do not infer that
// a zero debit or an expired lease means the request has finished: recovery must
// make the durable reservation terminal before initialization may proceed.
func rejectOpenLegacyWalletReservationsTx(tx *gorm.DB) error {
	var reservations []WalletReservation
	query := tx.Select("id").Where("funding_mode = ? AND state IN ?", BillingReservationLegacy,
		[]string{WalletReservationReserved, WalletReservationFinalizing}).Limit(1)
	if tx.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&reservations).Error; err != nil {
		return err
	}
	if len(reservations) != 0 {
		return ErrWalletBaselineBusy
	}
	return nil
}
