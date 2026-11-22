package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// SQLite exercises entry ordering and rollback only, not MySQL/PostgreSQL
// isolation. A gate failure must stop every legacy entry before touching users.
func TestUserQuotaEntriesStopAtBaselineGate(t *testing.T) {
	entries := []struct {
		name string
		call func(User) error
	}{
		{"affiliate", func(user User) error { return user.TransferAffQuotaToQuota(int(common.QuotaPerUnit)) }},
		{"increase", func(user User) error { return increaseUserQuota(user.Id, 10) }},
		{"decrease", func(user User) error { return decreaseUserQuota(user.Id, 10) }},
		{"reserve", func(user User) error { return ReserveUserQuota(user.Id, 10) }},
		{"direct_decrease", func(user User) error { return DecreaseUserQuotaDirect(user.Id, 10) }},
		{"direct_increase", func(user User) error { return IncreaseUserQuotaDirect(user.Id, 10) }},
		{"override", func(user User) error { return SetUserQuotaAbsolute(user.Id, 110) }},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			truncateTables(t)
			clearCreditSourceBaseline(t)
			user := creditSourceTestUser(t, "quota-gate-"+entry.name, 100)
			gateErr := errors.New("injected quota baseline gate failure")
			observing, sawGate, touchedUser := true, false, false
			const callback = "test:user-quota-baseline-gate"
			require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if !observing {
					return
				}
				if tx.Statement.Table == "rebate_system_states" {
					sawGate = true
					tx.AddError(gateErr)
				}
				if tx.Statement.Table == "users" {
					touchedUser = true
				}
			}))
			t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callback)) })
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if observing && tx.Statement.Table == "users" {
					touchedUser = true
				}
			}))
			t.Cleanup(func() { require.NoError(t, DB.Callback().Query().Remove(callback)) })
			err := entry.call(user)
			observing = false
			require.ErrorIs(t, err, gateErr)
			require.True(t, sawGate)
			require.False(t, touchedUser)
			var refreshed User
			require.NoError(t, DB.First(&refreshed, user.Id).Error)
			require.Equal(t, 100, refreshed.Quota)
		})
	}
}

func isolateUserWalletBatchStores(t *testing.T) {
	t.Helper()
	previous := make([]map[int]int, BatchUpdateTypeCount)
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		previous[i] = batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		for i := 0; i < BatchUpdateTypeCount; i++ {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = previous[i]
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestUserQuotaBatchFlagKeepsWalletWritesSynchronous(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	isolateUserWalletBatchStores(t)
	previous := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = previous })
	user := creditSourceTestUser(t, "quota-sync-batch", 100)
	require.NoError(t, IncreaseUserQuota(user.Id, 25, false))
	require.NoError(t, DecreaseUserQuota(user.Id, 4, false))
	var refreshed User
	require.NoError(t, DB.First(&refreshed, user.Id).Error)
	require.Equal(t, 121, refreshed.Quota)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	queued := len(batchUpdateStores[BatchUpdateTypeUserQuota])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	require.Zero(t, queued)
}

func TestLegacyUserQuotaBatchFlushAfterActivation(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	isolateUserWalletBatchStores(t)
	user := creditSourceTestUser(t, "quota-legacy-queued", 0)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, 25)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 3)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	// Model a queue created before activation and flushed after the marker.
	// This is deliberately not a database concurrency test.
	enableCreditSourceBaseline(t)
	batchUpdate()
	var refreshed User
	require.NoError(t, DB.First(&refreshed, user.Id).Error)
	require.Equal(t, 25, refreshed.Quota)
	require.Equal(t, 3, refreshed.UsedQuota)
	require.Equal(t, 1, refreshed.RequestCount)
	lot := firstCreditSourceLot(t, user.Id)
	require.Equal(t, WalletCreditSourcePromotion, lot.Source)
	require.Equal(t, "compensation", lot.Component)
	require.False(t, lot.Eligible)
	require.Zero(t, lot.EligibleMoneyMicros)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -4)
	batchUpdate()
	require.NoError(t, DB.First(&refreshed, user.Id).Error)
	require.Equal(t, 21, refreshed.Quota)
	require.NoError(t, DB.First(&lot, lot.ID).Error)
	require.EqualValues(t, 21, lot.QuotaRemaining)
	var adjustment WalletConsumptionSettlement
	require.NoError(t, DB.Where("user_id = ? AND accounting_kind = ?", user.Id, "adjustment").First(&adjustment).Error)
	require.EqualValues(t, 4, adjustment.WalletQuota)
	require.Zero(t, adjustment.EligibleMoneyConsumedMicros)
}

func TestLegacyUserQuotaBatchFailureRequeuesAtomically(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	isolateUserWalletBatchStores(t)
	user := creditSourceTestUser(t, "quota-batch-retry", 0)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, 25)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 3)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	enableCreditSourceBaseline(t)
	const callback = "test:user-quota-batch-rollback"
	rejectStats := true
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if !rejectStats || tx.Statement.Table != "users" {
			return
		}
		if updates, ok := tx.Statement.Dest.(map[string]interface{}); ok {
			if _, exists := updates["used_quota"]; exists {
				tx.AddError(errors.New("injected batch statistics failure"))
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callback)) })
	batchUpdate()
	rejectStats = false
	var refreshed User
	require.NoError(t, DB.First(&refreshed, user.Id).Error)
	require.Zero(t, refreshed.Quota)
	require.Zero(t, refreshed.UsedQuota)
	require.Zero(t, refreshed.RequestCount)
	var lots int64
	require.NoError(t, DB.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).Count(&lots).Error)
	require.Zero(t, lots)
	for type_, expected := range map[int]int{BatchUpdateTypeUserQuota: 25, BatchUpdateTypeUsedQuota: 3, BatchUpdateTypeRequestCount: 1} {
		batchUpdateLocks[type_].Lock()
		queued := batchUpdateStores[type_][user.Id]
		batchUpdateLocks[type_].Unlock()
		require.Equal(t, expected, queued)
	}
	batchUpdate()
	require.NoError(t, DB.First(&refreshed, user.Id).Error)
	require.Equal(t, 25, refreshed.Quota)
	require.Equal(t, 3, refreshed.UsedQuota)
	require.Equal(t, 1, refreshed.RequestCount)
}
