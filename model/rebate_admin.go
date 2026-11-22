package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	sqlitedriver "github.com/glebarez/go-sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// walletAdminTransaction retries only complete, rolled-back transactions. The
// active baseline fast path does not serialize writers on MySQL/PostgreSQL, and
// consumption locks invitee -> inviter, which can cross administrative locks.
func walletAdminTransaction(db *gorm.DB, fn func(*gorm.DB) error) error {
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		// A deadlock can abort the outer transaction; a savepoint is not a retry.
		return db.Transaction(fn)
	}
	const maxAttempts = 3
	for attempt := 0; ; attempt++ {
		err := db.Transaction(fn)
		if err == nil || attempt+1 == maxAttempts || !isRetryableWalletAdminLockError(err) {
			return err
		}
		// Wait outside the rolled-back transaction and honor caller cancellation.
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-db.Statement.Context.Done():
			timer.Stop()
			return db.Statement.Context.Err()
		case <-timer.C:
		}
	}
}

func isRetryableWalletAdminLockError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1213 || mysqlErr.Number == 1205 // deadlock / lock wait timeout
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" || pgErr.Code == "40001" || pgErr.Code == "55P03"
	}
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		// BUSY (including BUSY_SNAPSHOT) and LOCKED_SHAREDCACHE can clear after
		// restarting the transaction. Generic SQLITE_LOCKED may be a usage bug.
		return sqliteErr.Code()&0xff == 5 || sqliteErr.Code() == 262
	}
	return false
}

// maxRebateAdminRecords bounds one administrative rebate mutation so a single
// request cannot hold locks on an unbounded number of financial rows.
const maxRebateAdminRecords = 100

func normalizeRebateAdminInput(recordIDs []int64, operatorID int, reason string) ([]int64, string, error) {
	if operatorID <= 0 {
		return nil, "", errors.New("invalid rebate operator")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 128 {
		return nil, "", errors.New("invalid rebate void reason")
	}
	if len(recordIDs) == 0 || len(recordIDs) > maxRebateAdminRecords {
		return nil, "", fmt.Errorf("rebate mutation requires between 1 and %d records", maxRebateAdminRecords)
	}
	seen := make(map[int64]struct{}, len(recordIDs))
	unique := make([]int64, 0, len(recordIDs))
	for _, id := range recordIDs {
		if id <= 0 {
			return nil, "", errors.New("invalid rebate record id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	// 所有批量操作按相同顺序获取记录锁，避免交叉批次反向等待。
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique, reason, nil
}

// VoidWalletRebates cancels rewards that have not been paid yet. Voiding is
// idempotent, audited with the operator and reason, and it permanently removes
// the obligation from the payout task. Paid or discarded records are reported
// back untouched instead of being silently rewritten.
func VoidWalletRebates(db *gorm.DB, recordIDs []int64, operatorID int, reason string) (int, []int64, error) {
	if db == nil {
		return 0, nil, errors.New("rebate void requires a database")
	}
	ids, reason, err := normalizeRebateAdminInput(recordIDs, operatorID, reason)
	if err != nil {
		return 0, nil, err
	}
	now := time.Now().UTC()
	voided := 0
	blocked := make([]int64, 0)
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			var record RebateRecord
			if err := lockForUpdate(tx).Where("id = ?", id).First(&record).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					blocked = append(blocked, id)
					continue
				}
				return err
			}
			switch record.Status {
			case RebateStatusVoided:
				voided++
			case RebateStatusPending, RebateStatusHeld:
				result := tx.Model(&RebateRecord{}).
					Where("id = ? AND status = ?", id, record.Status).
					Updates(map[string]interface{}{
						"status":          RebateStatusVoided,
						"voided_at":       now,
						"voided_by":       operatorID,
						"void_reason":     reason,
						"held_by_closure": false,
						"updated_at":      now,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("rebate record changed concurrently")
				}
				voided++
			default:
				blocked = append(blocked, id)
			}
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return voided, blocked, nil
}

// ClawbackPaidWalletRebates reverses already-paid rewards after confirmed
// fraud such as a chargeback. The inviter's wallet is debited with a durable,
// idempotent adjustment key so lot provenance stays exact and a retry can
// never debit twice; the record is then voided for audit. Repeating the call
// for an already clawed-back record is a no-op. If the inviter already spent
// the reward, the debit records explicit unallocated debt rather than hiding
// the loss. Records that are not paid are reported back untouched.
func ClawbackPaidWalletRebates(db *gorm.DB, recordIDs []int64, operatorID int, reason string) (int, []int64, error) {
	if db == nil {
		return 0, nil, errors.New("rebate clawback requires a database")
	}
	ids, reason, err := normalizeRebateAdminInput(recordIDs, operatorID, reason)
	if err != nil {
		return 0, nil, err
	}
	// 审计前缀也占用数据库列宽；在任何扣款之前拒绝过长输入。
	if len(reason) > 128-len("clawback:") {
		return 0, nil, fmt.Errorf("rebate clawback reason exceeds %d bytes", 128-len("clawback:"))
	}
	now := time.Now().UTC()
	clawed := 0
	blocked := make([]int64, 0)
	affectedUsers := make(map[int]struct{})
	err = walletAdminTransaction(db, func(tx *gorm.DB) error {
		clawed = 0
		blocked = blocked[:0]
		affectedUsers = make(map[int]struct{})
		if _, err := LockWalletCreditBaselineTx(tx); err != nil {
			return err
		}
		// Lock the entire sorted batch before touching any wallet. Interleaving
		// record -> wallet -> next record can deadlock even with sorted IDs.
		records := make([]RebateRecord, len(ids))
		for i, id := range ids {
			if err := lockForUpdate(tx).Where("id = ?", id).First(&records[i]).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
		}
		for i, id := range ids {
			record := &records[i]
			if record.ID == 0 {
				blocked = append(blocked, id)
				continue
			}
			if record.Status == RebateStatusVoided && strings.HasPrefix(record.VoidReason, "clawback:") {
				clawed++
				continue
			}
			if record.Status != RebateStatusPaid || record.RewardQuota <= 0 {
				blocked = append(blocked, id)
				continue
			}
			key, err := NewWalletAdjustmentKey(record.InviterID, "rebate_clawback", record.SettlementKey)
			if err != nil {
				return err
			}
			if err := AdjustWalletDebitTx(tx, key, record.InviterID, record.RewardQuota); err != nil {
				return err
			}
			result := tx.Model(&RebateRecord{}).
				Where("id = ? AND status = ?", id, RebateStatusPaid).
				Updates(map[string]interface{}{
					"status":      RebateStatusVoided,
					"voided_at":   now,
					"voided_by":   operatorID,
					"void_reason": "clawback:" + reason,
					"updated_at":  now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("rebate record changed concurrently")
			}
			affectedUsers[record.InviterID] = struct{}{}
			clawed++
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	for userID := range affectedUsers {
		_ = walletAfterCommit(db, userID, nil)
	}
	return clawed, blocked, nil
}
