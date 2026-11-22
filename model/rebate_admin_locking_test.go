package model

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletAdminTransactionRetryBoundary(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"mysql deadlock", &mysql.MySQLError{Number: 1213}, true},
		{"mysql timeout", &mysql.MySQLError{Number: 1205}, true},
		{"postgres deadlock", &pgconn.PgError{Code: "40P01"}, true},
		{"postgres serialization", &pgconn.PgError{Code: "40001"}, true},
		{"postgres lock timeout", &pgconn.PgError{Code: "55P03"}, true},
		{"mysql duplicate", &mysql.MySQLError{Number: 1062}, false},
		{"postgres duplicate", &pgconn.PgError{Code: "23505"}, false},
		{"postgres connection failure", &pgconn.PgError{Code: "08006"}, false},
		{"business error mentioning locks", errors.New("deadlock: database is locked"), false},
		{"canceled", context.Canceled, false},
	}
	db := newRebateTestDB(t)
	user := createRebateTestUser(t, db, 0)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("wallet write: %w", tc.err)
			calls := 0
			err := walletAdminTransaction(db, func(tx *gorm.DB) error {
				calls++
				if err := tx.Model(&User{}).Where("id = ?", user.Id).
					UpdateColumn("quota", gorm.Expr("quota + 1")).Error; err != nil {
					return err
				}
				return wrapped
			})
			require.ErrorIs(t, err, wrapped)
			wantCalls := 1
			if tc.retryable {
				wantCalls = 3
			}
			require.Equal(t, wantCalls, calls)
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Zero(t, stored.Quota, "all failed attempts must roll back")
		})
	}

	deadlock := &mysql.MySQLError{Number: 1213}
	calls := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		return walletAdminTransaction(tx, func(inner *gorm.DB) error {
			calls++
			return deadlock
		})
	})
	require.ErrorIs(t, err, deadlock)
	require.Equal(t, 1, calls, "a savepoint cannot restart an aborted outer transaction")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	err = walletAdminTransaction(db.WithContext(ctx), func(tx *gorm.DB) error {
		calls++
		cancel()
		return deadlock
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

// Use a real driver error; SQLite's error code fields are intentionally private.
func TestWalletAdminRecognizesSQLiteBusy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "admin-lock.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, db.Exec("CREATE TABLE admin_lock_probe (id INTEGER PRIMARY KEY, value INTEGER)").Error)
	require.NoError(t, db.Exec("INSERT INTO admin_lock_probe VALUES (1, 0)").Error)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	holder, err := pool.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer holder.Rollback()
	_, err = holder.ExecContext(ctx, "UPDATE admin_lock_probe SET value = 1 WHERE id = 1")
	require.NoError(t, err)
	contender, err := pool.Conn(ctx)
	require.NoError(t, err)
	defer contender.Close()
	_, err = contender.ExecContext(ctx, "PRAGMA busy_timeout = 0")
	require.NoError(t, err)
	_, err = contender.ExecContext(ctx, "UPDATE admin_lock_probe SET value = 2 WHERE id = 1")
	require.Error(t, err)
	require.True(t, isRetryableWalletAdminLockError(fmt.Errorf("write: %w", err)))
	_, err = contender.ExecContext(ctx, "SELECT * FROM missing_admin_lock_table")
	require.Error(t, err)
	require.False(t, isRetryableWalletAdminLockError(err))
}

func TestManualCompleteTopUpRetriesAfterQuotaWrite(t *testing.T) {
	truncateTables(t)
	operatorID := insertPaymentGuardRoot(t)
	const targetID = 910001
	const tradeNo = "manual-lock-retry"
	insertUserForPaymentGuardTest(t, targetID, 0)
	insertTopUpForPaymentGuardTest(t, tradeNo, targetID, PaymentProviderCreem)
	require.NoError(t, DB.Create(&RebateSystemState{
		Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC(),
	}).Error)

	gateWrites, orderReads, roleReads, quotaWrites := 0, 0, 0, 0
	observing := true
	queryCallback := "test:manual-admin-lock-query"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
		if !observing || tx.Error != nil {
			return
		}
		if _, inTransaction := tx.Statement.ConnPool.(gorm.TxCommitter); !inTransaction {
			return
		}
		switch tx.Statement.Table {
		case "top_ups":
			orderReads++
			require.Positive(t, gateWrites, "baseline write must precede order and role locks")
			order, ok := tx.Statement.Dest.(*TopUp)
			require.True(t, ok)
			require.Equal(t, common.TopUpStatusPending, order.Status, "the previous attempt must roll back")
		case "users":
			if len(tx.Statement.Selects) == 3 && tx.Statement.Selects[1] == "role" {
				roleReads++
				require.Positive(t, gateWrites)
			}
		}
	}))
	t.Cleanup(func() { DB.Callback().Query().Remove(queryCallback) })
	updateCallback := "test:manual-admin-lock-update"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
		if !observing || tx.Error != nil {
			return
		}
		if tx.Statement.Table == "rebate_system_states" {
			gateWrites++
		}
		if tx.Statement.Table == "users" {
			updates, ok := tx.Statement.Dest.(map[string]interface{})
			if ok {
				if _, actualQuotaWrite := updates["quota"].(int64); actualQuotaWrite {
					quotaWrites++
					if quotaWrites == 1 {
						tx.AddError(&pgconn.PgError{Code: "40P01"})
						gateWrites = 0
					}
				}
			}
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(updateCallback) })

	require.NoError(t, ManualCompleteTopUp(tradeNo, "127.0.0.1", operatorID))
	observing = false
	require.Equal(t, 2, orderReads)
	require.Equal(t, 2, roleReads, "retry must revalidate both roles")
	require.Equal(t, 2, quotaWrites)
	require.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, targetID))
	require.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, tradeNo))
	var lots, logs int64
	require.NoError(t, DB.Model(&WalletCreditLot{}).Where("user_id = ?", targetID).Count(&lots).Error)
	require.EqualValues(t, 1, lots)
	require.NoError(t, ManualCompleteTopUp(tradeNo, "127.0.0.1", operatorID))
	require.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, targetID))
	require.NoError(t, LOG_DB.Model(&Log{}).Where("user_id = ? AND type = ?", targetID, LogTypeTopup).Count(&logs).Error)
	require.EqualValues(t, 1, logs)
}

func TestManualCompleteTopUpPreservesOrderQueryErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		attempts int
	}{
		{"deadlock", &mysql.MySQLError{Number: 1213}, 3},
		{"connection", &pgconn.PgError{Code: "08006"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			calls := 0
			callback := "test:manual-admin-order-error"
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "top_ups" {
					calls++
					tx.AddError(tc.err)
				}
			}))
			t.Cleanup(func() { DB.Callback().Query().Remove(callback) })
			err := ManualCompleteTopUp("query-error", "127.0.0.1", 1)
			require.ErrorIs(t, err, tc.err, "database failure must not become order-not-found")
			require.Equal(t, tc.attempts, calls)
		})
	}
}

func TestClawbackLocksWholeBatchAndRollsBackBeforeRetry(t *testing.T) {
	for _, retryable := range []bool{true, false} {
		t.Run(fmt.Sprint(retryable), func(t *testing.T) {
			db, inviter, invitee, first := rebateAdminTestSetup(t, 0)
			settleRebateEligibleUsage(t, db, invitee.Id, "admin-lock-second", 200, 100_000_000)
			var second RebateRecord
			require.NoError(t, db.Where("invitee_id = ? AND id <> ?", invitee.Id, first.ID).First(&second).Error)
			var failure error = errors.New("nonretryable debit failure")
			if retryable {
				failure = &mysql.MySQLError{Number: 1213}
			}
			var locked []int64
			gateWritten := false
			updates, attempts := 0, 0
			observing := true
			queryCallback := "test:clawback-batch-lock-query"
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
				if !observing || tx.Error != nil {
					return
				}
				if tx.Statement.Table == "rebate_records" {
					require.True(t, gateWritten, "baseline must precede rebate record locks")
					record, ok := tx.Statement.Dest.(*RebateRecord)
					require.True(t, ok)
					locked = append(locked, record.ID)
				}
				if tx.Statement.Table == "users" {
					require.Equal(t, []int64{first.ID, second.ID}, locked, "lock every record before the first wallet")
				}
			}))
			t.Cleanup(func() { db.Callback().Query().Remove(queryCallback) })
			updateCallback := "test:clawback-batch-lock-update"
			require.NoError(t, db.Callback().Update().After("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
				if !observing || tx.Error != nil {
					return
				}
				if tx.Statement.Table == "rebate_system_states" {
					if !gateWritten {
						attempts++
					}
					gateWritten = true
				}
				if tx.Statement.Table == "rebate_records" {
					updates++
					if updates == 2 {
						// Both debits are already written; none may survive this failure.
						tx.AddError(failure)
						locked = nil
						gateWritten = false
					}
				}
			}))
			t.Cleanup(func() { db.Callback().Update().Remove(updateCallback) })

			missingID := second.ID + 1000
			clawed, blocked, err := ClawbackPaidWalletRebates(db, []int64{second.ID, missingID, first.ID, second.ID}, 7, "lock retry")
			observing = false
			var user User
			require.NoError(t, db.First(&user, inviter.Id).Error)
			var adjustmentCount int64
			require.NoError(t, db.Model(&WalletConsumptionSettlement{}).
				Where("user_id = ? AND accounting_kind = ?", inviter.Id, "adjustment").Count(&adjustmentCount).Error)
			if retryable {
				require.NoError(t, err)
				require.Equal(t, 2, attempts)
				require.Equal(t, 2, clawed, "rolled-back counts must not leak into the result")
				require.Equal(t, []int64{missingID}, blocked)
				require.Zero(t, user.Quota)
				require.EqualValues(t, 2, adjustmentCount)
				clawed, blocked, err = ClawbackPaidWalletRebates(db, []int64{first.ID, second.ID, missingID}, 7, "lock retry")
				require.NoError(t, err)
				require.Equal(t, 2, clawed)
				require.Equal(t, []int64{missingID}, blocked)
				require.NoError(t, db.First(&user, inviter.Id).Error)
				require.Zero(t, user.Quota)
			} else {
				require.ErrorIs(t, err, failure)
				require.Equal(t, 1, attempts)
				require.Zero(t, clawed)
				require.Nil(t, blocked)
				require.EqualValues(t, first.RewardQuota+second.RewardQuota, user.Quota)
				require.Zero(t, adjustmentCount)
				var paidCount int64
				require.NoError(t, db.Model(&RebateRecord{}).Where("id IN ? AND status = ?",
					[]int64{first.ID, second.ID}, RebateStatusPaid).Count(&paidCount).Error)
				require.EqualValues(t, 2, paidCount)
			}
			mismatches, _, done, err := ReconcileWalletCreditBatch(db, 0, 100)
			require.NoError(t, err)
			require.True(t, done)
			require.Empty(t, mismatches)
		})
	}
}
