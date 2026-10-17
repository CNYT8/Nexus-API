package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newRebatePayoutDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	return db
}

func seedRebatePayout(t *testing.T, db *gorm.DB, inviterID, inviteeID int, quota int64, status string, held bool) RebateRecord {
	t.Helper()
	key, err := NewRebateIdempotencyKey("payout-fixture", fmt.Sprintf("%d:%d:%d", inviterID, inviteeID, time.Now().UnixNano()))
	require.NoError(t, err)
	payoutKey, err := NewRebateIdempotencyKey("payout", key)
	require.NoError(t, err)
	record := RebateRecord{
		SettlementKey: key, PayoutKey: payoutKey, InviterID: inviterID, InviteeID: inviteeID,
		RewardMoneyMicros: quota * 2, RewardQuota: quota, RateBasisPoints: 500,
		QuotaPerUnitMicros: 500_000 * MoneyMicrosPerUnit,
		Status:             status, HeldByClosure: held, CountedDuringClosure: held,
		AvailableAt: time.Now().UTC().Add(-time.Hour),
	}
	require.NoError(t, db.Create(&record).Error)
	return record
}

func TestRebatePayoutClosurePaysOldDebtOnly(t *testing.T) {
	setting := rebateRewardSetting()
	setting.Enabled = false
	setting.CountDuringClosure = true
	useRebateRewardSetting(t, setting)
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	old := seedRebatePayout(t, db, inviter.Id, invitee.Id, 10, RebateStatusPending, false)
	held := seedRebatePayout(t, db, inviter.Id, invitee.Id, 20, RebateStatusHeld, true)
	paid, err := PayDueWalletRebates(db, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&old, old.ID).Error)
	require.Equal(t, RebateStatusPaid, old.Status)
	require.NoError(t, db.First(&held, held.ID).Error)
	require.Equal(t, RebateStatusHeld, held.Status)
	paid, err = PayDueWalletRebates(db, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Zero(t, paid)
	setting.Enabled = true
	require.NoError(t, operation_setting.UpdateRebateSetting(setting))
	paid, err = PayDueWalletRebates(db, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&held, held.ID).Error)
	require.Equal(t, RebateStatusPaid, held.Status)
	require.False(t, held.HeldByClosure)
	require.True(t, held.CountedDuringClosure)
}

func TestRebatePayoutDeletedInviterVoidsWithoutBlocking(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	for _, hardDelete := range []bool{false, true} {
		t.Run(fmt.Sprintf("hard=%t", hardDelete), func(t *testing.T) {
			db := newRebatePayoutDB(t)
			deleted, invitee := createRebateInvitation(t, db)
			first := seedRebatePayout(t, db, deleted.Id, invitee.Id, 10, RebateStatusPending, false)
			held := seedRebatePayout(t, db, deleted.Id, invitee.Id, 0, RebateStatusHeld, false)
			if hardDelete {
				require.NoError(t, db.Unscoped().Delete(deleted).Error)
			} else {
				require.NoError(t, db.Delete(deleted).Error)
			}
			live := createRebateTestUser(t, db, 0)
			last := seedRebatePayout(t, db, live.Id, invitee.Id, 20, RebateStatusPending, false)
			paid, err := PayDueWalletRebates(db, time.Now().UTC(), 1)
			require.NoError(t, err)
			require.Equal(t, 1, paid)
			for _, id := range []int64{first.ID, held.ID} {
				var record RebateRecord
				require.NoError(t, db.First(&record, id).Error)
				require.Equal(t, RebateStatusVoided, record.Status)
				require.NotNil(t, record.VoidedAt)
				require.Equal(t, "inviter_deleted", record.VoidReason)
			}
			require.NoError(t, db.First(&last, last.ID).Error)
			require.Equal(t, RebateStatusPaid, last.Status)
		})
	}
}

func TestRebatePayoutBadRecordRollsBackAndSkipsToNextPage(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	bad := seedRebatePayout(t, db, inviter.Id, invitee.Id, 10, RebateStatusPending, false)
	good := seedRebatePayout(t, db, inviter.Id, invitee.Id, 20, RebateStatusPending, false)
	// Fail AFTER the lot and wallet updates, at the final status write.
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_rebate_status BEFORE UPDATE ON rebate_records WHEN NEW.id = %d AND NEW.status = 'paid' BEGIN SELECT RAISE(ABORT, 'injected rebate status failure'); END`, bad.ID)).Error)
	paid, err := PayDueWalletRebates(db, time.Now().UTC(), 1)
	require.ErrorContains(t, err, "injected rebate status failure")
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, 20, inviter.Quota)
	var lots int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("source = ?", WalletCreditSourceRebate).Count(&lots).Error)
	require.EqualValues(t, 1, lots)
	require.NoError(t, db.First(&bad, bad.ID).Error)
	require.Equal(t, RebateStatusPending, bad.Status)
	require.Nil(t, bad.PayoutLotID)
	require.NoError(t, db.First(&good, good.ID).Error)
	require.Equal(t, RebateStatusPaid, good.Status)
	require.NoError(t, db.Exec("DROP TRIGGER fail_rebate_status").Error)
	paid, err = PayDueWalletRebates(db, time.Now().UTC(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, 30, inviter.Quota)
}

// A driver-level commit rejection must not increment the success counter.
// Roll back the real test transaction before returning the simulated failure.
type rebateCommitFailurePool struct {
	*sql.DB
	fail *atomic.Bool
}

func (p rebateCommitFailurePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &rebateCommitFailureTx{Tx: tx, fail: p.fail}, nil
}

type rebateCommitFailureTx struct {
	*sql.Tx
	fail *atomic.Bool
}

func (tx rebateCommitFailureTx) Commit() error {
	if tx.fail.CompareAndSwap(true, false) {
		if err := tx.Tx.Rollback(); err != nil {
			return err
		}
		return errors.New("injected rebate commit failure")
	}
	return tx.Tx.Commit()
}

func TestRebatePayoutCountsOnlyCommittedTransactions(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	bad := seedRebatePayout(t, db, inviter.Id, invitee.Id, 10, RebateStatusPending, false)
	good := seedRebatePayout(t, db, inviter.Id, invitee.Id, 20, RebateStatusPending, false)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	fail := &atomic.Bool{}
	fail.Store(true)
	wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	wrapped.Statement.ConnPool = rebateCommitFailurePool{DB: sqlDB, fail: fail}
	paid, err := PayDueWalletRebates(wrapped, time.Now().UTC(), 1)
	require.ErrorContains(t, err, "injected rebate commit failure")
	require.Equal(t, 1, paid)
	require.NoError(t, db.First(&bad, bad.ID).Error)
	require.Equal(t, RebateStatusPending, bad.Status)
	require.NoError(t, db.First(&good, good.ID).Error)
	require.Equal(t, RebateStatusPaid, good.Status)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, 20, inviter.Quota)
}

func TestRebatePayoutDuplicateWorkersAndDustHold(t *testing.T) {
	useRebateRewardSetting(t, rebateRewardSetting())
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	// Legacy zero-quota pending rewards must become held, never discarded.
	dust := seedRebatePayout(t, db, inviter.Id, invitee.Id, 0, RebateStatusPending, false)
	require.NoError(t, db.Model(&RebateRecord{}).Where("id = ?", dust.ID).Update("reward_money_micros", 1).Error)
	for i := 0; i < 12; i++ {
		seedRebatePayout(t, db, inviter.Id, invitee.Id, 10, RebateStatusPending, false)
	}
	type result struct {
		count int
		err   error
	}
	results := make(chan result, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			count, err := PayDueWalletRebates(db, time.Now().UTC(), 100)
			results <- result{count, err}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for result := range results {
		require.NoError(t, result.err)
		count += result.count
	}
	require.Equal(t, 12, count)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, 120, inviter.Quota)
	require.NoError(t, db.First(&dust, dust.ID).Error)
	require.Equal(t, RebateStatusHeld, dust.Status)
	require.EqualValues(t, 1, dust.RewardMoneyMicros)
	require.Zero(t, dust.DiscardedRewardMoneyMicros)
	var lots int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("source = ?", WalletCreditSourceRebate).Count(&lots).Error)
	require.EqualValues(t, 12, lots)
}
