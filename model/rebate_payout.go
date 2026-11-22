package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const rebatePayoutCursorKey = "rebate_payout_cursor"

const (
	rebatePayoutScanLimit = 500
	rebatePayoutTimeLimit = 5 * time.Second
)

// PayDueWalletRebates 只发放生成时保存的金额快照。每轮同时限制成功数、
// 扫描数和耗时；持久游标越过坏记录和精度挂起记录，绕行结束后再重试。
// 每笔付款独立提交，游标不是幂等依据，多个实例仍由记录锁保护同一笔款。
func PayDueWalletRebates(db *gorm.DB, now time.Time, maxRecords int) (int, error) {
	if db == nil || now.IsZero() || maxRecords <= 0 {
		return 0, errors.New("invalid rebate payout input")
	}
	ctx, cancel := context.WithTimeout(db.Statement.Context, rebatePayoutTimeLimit)
	defer cancel()
	work := db.WithContext(ctx)
	state := RebateSystemState{Key: rebatePayoutCursorKey, Value: "0:0", UpdatedAt: now}
	if err := work.Session(&gorm.Session{SkipDefaultTransaction: true}).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return 0, err
	}
	if err := work.Where(map[string]interface{}{"key": rebatePayoutCursorKey}).First(&state).Error; err != nil {
		return 0, err
	}
	cursor, highWater, err := parseRebatePayoutCursor(state.Value)
	if err != nil {
		return 0, err
	}
	if cursor >= highWater {
		cursor = 0
		if err := work.Model(&RebateRecord{}).Select("COALESCE(MAX(id), 0)").Scan(&highWater).Error; err != nil {
			return 0, err
		}
	}
	pageSize := min(maxRecords, 100)
	paid, scanned, failures := 0, 0, 0
	var firstErr error
	for cursor < highWater && paid < maxRecords && scanned < rebatePayoutScanLimit && ctx.Err() == nil {
		var due []RebateRecord
		if err := work.Where("id > ? AND id <= ? AND status IN (?, ?) AND available_at <= ?", cursor, highWater, RebateStatusPending, RebateStatusHeld, now).
			Order("id ASC").Limit(min(pageSize, rebatePayoutScanLimit-scanned)).Find(&due).Error; err != nil {
			firstErr = errors.Join(firstErr, err)
			break
		}
		if len(due) == 0 {
			cursor = highWater
			break
		}
		for _, candidate := range due {
			if ctx.Err() != nil {
				break
			}
			cursor = candidate.ID
			scanned++
			didPay := false
			err := work.Transaction(func(tx *gorm.DB) error {
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
	if cursor >= highWater {
		cursor, highWater = 0, 0
	}
	// 即使工作预算已用完也给游标一次短暂的独立写入机会。比较旧值防止
	// 较慢的实例覆盖其它实例已经推进的进度；写入失败只导致安全地重扫。
	saveCtx, saveCancel := context.WithTimeout(db.Statement.Context, time.Second)
	defer saveCancel()
	saveErr := db.WithContext(saveCtx).Session(&gorm.Session{SkipDefaultTransaction: true}).Model(&RebateSystemState{}).
		Where(map[string]interface{}{"key": rebatePayoutCursorKey, "value": state.Value}).
		Updates(map[string]interface{}{"value": fmt.Sprintf("%d:%d", cursor, highWater), "updated_at": now}).Error
	if firstErr != nil {
		firstErr = fmt.Errorf("%d rebate payouts failed; first failure: %w", failures, firstErr)
	}
	return paid, errors.Join(firstErr, saveErr)
}

func parseRebatePayoutCursor(value string) (int64, int64, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid rebate payout cursor")
	}
	cursor, cursorErr := strconv.ParseInt(parts[0], 10, 64)
	highWater, highWaterErr := strconv.ParseInt(parts[1], 10, 64)
	if cursorErr != nil || highWaterErr != nil || cursor < 0 || highWater < 0 || cursor > highWater {
		return 0, 0, errors.New("invalid rebate payout cursor")
	}
	return cursor, highWater, nil
}

func payWalletRebateTx(tx *gorm.DB, id int64, now time.Time) (bool, error) {
	if _, err := LockWalletCreditBaselineTx(tx); err != nil {
		return false, err
	}
	// Routing hint only: do not lock a record before its invitee. Accounting
	// reads below must stay current even if this opens a MySQL RR snapshot.
	var hint RebateRecord
	if err := tx.Where("id = ?", id).First(&hint).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if _, err := walletLockUserTx(tx, hint.InviteeID); err != nil {
		return false, err
	}
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
	pending, err := rebateRecordReversalPendingTx(tx, &record)
	if err != nil {
		return false, err
	}
	if pending {
		return false, nil
	}
	setting, err := GetRebateSettingTx(tx)
	if err != nil {
		return false, err
	}
	var inviter User
	err = lockForUpdate(tx).Select("id").Where("id = ?", record.InviterID).First(&inviter).Error
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
	if record.HeldByClosure && !setting.Enabled {
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
	if err := walletCurrentTx(tx).Where("credit_event_key = ?", lotKey).First(&storedLot).Error; err != nil {
		return false, err
	}
	if err := updateRebatePayoutStateTx(tx, &record, map[string]interface{}{
		"status": RebateStatusPaid, "payout_lot_id": storedLot.ID, "paid_at": now,
		"held_by_closure": false, "updated_at": now,
	}); err != nil {
		return false, err
	}
	record.PayoutLotID = &storedLot.ID
	if err := ApplyRebateSourceReversalTx(tx, &record); err != nil {
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
