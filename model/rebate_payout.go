package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// PayDueWalletRebates credits immutable generation-time snapshots. maxRecords
// limits successful payments, not failed attempts: keyset scanning past poison
// rows prevents a permanently bad first page starving every later obligation.
// A fixed high-water ID bounds each run; concurrent inserts wait for the next.
// Each payout commits its lot, wallet and status atomically. Count a payout
// only AFTER Transaction returns successfully (including database commit).
func PayDueWalletRebates(db *gorm.DB, now time.Time, maxRecords int) (int, error) {
	if db == nil || now.IsZero() || maxRecords <= 0 {
		return 0, errors.New("invalid rebate payout input")
	}
	var highWater int64
	if err := db.Model(&RebateRecord{}).Select("COALESCE(MAX(id), 0)").Scan(&highWater).Error; err != nil {
		return 0, err
	}
	pageSize := min(maxRecords, 100)
	paid, failures := 0, 0
	var cursor int64
	var firstErr error
	for cursor < highWater && paid < maxRecords {
		var due []RebateRecord
		// Held records are inspected too, so deleted inviters cannot leave
		// unpaid closure/dust obligations immortal. They are not blindly paid.
		if err := db.Where("id > ? AND id <= ? AND status IN (?, ?) AND available_at <= ?", cursor, highWater, RebateStatusPending, RebateStatusHeld, now).
			Order("id ASC").Limit(pageSize).Find(&due).Error; err != nil {
			return paid, errors.Join(firstErr, err)
		}
		if len(due) == 0 {
			break
		}
		for _, candidate := range due {
			cursor = candidate.ID
			didPay := false
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				didPay, err = payWalletRebateTx(tx, candidate.ID, now)
				return err
			})
			if err != nil {
				failures++
				if firstErr == nil {
					firstErr = fmt.Errorf("rebate %d: %w", candidate.ID, err)
				}
				continue
			}
			if didPay {
				_ = walletAfterCommit(db, candidate.InviterID, nil)
				paid++
			}
			if paid == maxRecords {
				break
			}
		}
	}
	if firstErr != nil {
		return paid, fmt.Errorf("%d rebate payouts failed; first failure: %w", failures, firstErr)
	}
	return paid, nil
}

func payWalletRebateTx(tx *gorm.DB, id int64, now time.Time) (bool, error) {
	var record RebateRecord
	if err := lockForUpdate(tx).Where("id = ?", id).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if (record.Status != RebateStatusPending && record.Status != RebateStatusHeld) || record.AvailableAt.After(now) {
		return false, nil
	}
	var inviter User
	err := lockForUpdate(tx).Select("id").Where("id = ?", record.InviterID).First(&inviter).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, updateRebatePayoutStateTx(tx, &record, map[string]interface{}{
			"status": RebateStatusVoided, "voided_at": now, "void_reason": "inviter_deleted", "updated_at": now,
		})
	}
	if err != nil {
		return false, err
	}
	// Closure only pauses rewards CREATED during closure. Previously owed
	// pending rewards remain payable even if the feature is currently off.
	if record.HeldByClosure && !operation_setting.GetRebateSetting().Enabled {
		return false, nil
	}
	if record.Status == RebateStatusHeld && !record.HeldByClosure {
		// Precision hold: never discard or infer a payout from today's ratio.
		return false, nil
	}
	if record.RewardQuota < 0 || record.RewardMoneyMicros < 0 {
		return false, errors.New("invalid rebate payout snapshot")
	}
	if record.RewardQuota == 0 {
		return false, updateRebatePayoutStateTx(tx, &record, map[string]interface{}{
			"status": RebateStatusHeld, "held_by_closure": false, "updated_at": now,
		})
	}
	lotKey, err := NewWalletCreditEventKey(record.InviterID, WalletCreditSourceRebate, "rebate", record.SettlementKey, "payout")
	if err != nil {
		return false, err
	}
	lot := WalletCreditLot{
		CreditEventKey: lotKey,
		UserID:         record.InviterID,
		Source:         WalletCreditSourceRebate,
		Component:      "payout",
		Eligible:       false,
		ReferenceType:  "rebate",
		ReferenceID:    record.SettlementKey,
		QuotaTotal:     record.RewardQuota,
		QuotaRemaining: record.RewardQuota,
	}
	applied, err := AddWalletCreditLotTx(tx, lot)
	if err != nil {
		return false, err
	}
	if !applied {
		return false, errors.New("rebate payout lot already exists without a paid record")
	}
	var storedLot WalletCreditLot
	if err := tx.Where("credit_event_key = ?", lotKey).First(&storedLot).Error; err != nil {
		return false, err
	}
	if err := updateRebatePayoutStateTx(tx, &record, map[string]interface{}{
		"status": RebateStatusPaid, "payout_lot_id": storedLot.ID, "paid_at": now,
		"held_by_closure": false, "updated_at": now,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func updateRebatePayoutStateTx(tx *gorm.DB, record *RebateRecord, values map[string]interface{}) error {
	result := tx.Model(&RebateRecord{}).Where("id = ? AND status = ?", record.ID, record.Status).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("rebate record changed concurrently")
	}
	return nil
}
