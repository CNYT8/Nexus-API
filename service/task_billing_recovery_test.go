package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func persistTaskBillingOutcome(t *testing.T, task *model.Task, actual int, failure bool) {
	t.Helper()
	previous := task.Status
	task.Status, task.Progress = model.TaskStatusSuccess, "100%"
	task.BillingState, task.BillingQuota = model.TaskBillingPending, actual
	if failure {
		task.Status, task.BillingQuota = model.TaskStatusFailure, 0
	}
	won, err := task.UpdateWithStatus(previous)
	require.NoError(t, err)
	require.True(t, won)
}

func assertRecoveredTaskBalances(t *testing.T, db *gorm.DB, task *model.Task, actual int) {
	t.Helper()
	var stored model.Task
	var token model.Token
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.NoError(t, db.First(&token, task.PrivateData.TokenId).Error)
	require.Equal(t, actual, stored.Quota)
	require.Equal(t, actual, stored.BillingQuota)
	require.Equal(t, model.TaskBillingDone, stored.BillingState)
	require.Equal(t, 10000-actual, getUserQuota(t, task.UserId))
	require.Equal(t, 10000-actual, token.RemainQuota)
	require.Equal(t, actual, token.UsedQuota)
}

// Uses a real SQLite transaction and the real baseline initializer. Historical
// pre-consumption must not be recreated as an eligible reservation or rebate.
func TestTaskRecoveryLegacyAcrossBaseline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actual  int
		failure bool
	}{{"failure", 0, true}, {"empty_success", 0, false}, {"refund_difference", 2000, false}, {"unchanged", 3000, false}, {"debit_difference", 4500, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, db.Where("1 = 1").Delete(&model.RebateSystemState{}).Error)
			require.NoError(t, db.Where("1 = 1").Delete(&model.WalletCreditLot{}).Error)
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", task.UserId).UpdateColumn("quota", 7000).Error)
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", task.PrivateData.TokenId).Updates(map[string]interface{}{"remain_quota": 7000, "used_quota": 3000}).Error)
			task.Quota = 3000
			require.NoError(t, db.Model(task).UpdateColumn("quota", 3000).Error)
			require.NoError(t, model.InitializeWalletCreditBaseline(db))
			persistTaskBillingOutcome(t, task, tc.actual, tc.failure)

			n, err := RecoverTaskBilling(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			assertRecoveredTaskBalances(t, db, task, tc.actual)
			n, err = RecoverTaskBilling(context.Background(), 1)
			require.NoError(t, err)
			require.Zero(t, n)
			if tc.actual > 3000 {
				// A non-rewarding debit adjustment permanently binds its key;
				// it is not a new reservation of historical pre-consumption.
				var reservation model.WalletReservation
				var settlement model.WalletConsumptionSettlement
				require.NoError(t, db.First(&reservation).Error)
				require.NoError(t, db.First(&settlement).Error)
				require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletReservation{}))
				require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
				require.Equal(t, model.WalletReservationFinalized, reservation.State)
				require.Zero(t, reservation.ReservedQuota)
				require.EqualValues(t, tc.actual-3000, reservation.FinalQuota)
				require.Equal(t, "adjustment", settlement.AccountingKind)
				require.EqualValues(t, tc.actual-3000, settlement.WalletQuota)
			} else {
				require.Zero(t, taskLedgerCount(t, db, &model.WalletReservation{}))
				require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			}
			require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
			var lots []model.WalletCreditLot
			require.NoError(t, db.Where("user_id = ?", task.UserId).Find(&lots).Error)
			var remaining int64
			for _, lot := range lots {
				require.False(t, lot.Eligible)
				remaining += lot.QuotaRemaining
			}
			require.EqualValues(t, 10000-tc.actual, remaining)
		})
	}
}

func TestTaskRecoverySuccessCrashWindows(t *testing.T) {
	for _, walletRecoveredFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("wallet_recovered_%t", walletRecoveredFirst), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, ReserveTaskWalletQuota(task, 3000))
			key, err := TaskWalletBillingKey(task)
			require.NoError(t, err)
			if walletRecoveredFirst {
				// Reproduce the old worker: only the wallet has reached 4500.
				markTaskSuccess(t, db, task)
				require.NoError(t, model.PrepareWalletFinalization(db, key, task.UserId, 3000, 4500))
				_, err = model.FinalizeWalletConsumption(db, key, task.UserId, 3000, 4500)
				require.NoError(t, err)
				require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			} else {
				// Crash immediately after SUCCESS, before Prepare or settlement.
				persistTaskBillingOutcome(t, task, 4500, false)
				require.NoError(t, db.Model(&model.WalletReservation{}).Where("reservation_key = ?", key).UpdateColumn("updated_at", time.Now().Add(-100*time.Hour)).Error)
				n, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 1)
				require.NoError(t, err)
				require.Zero(t, n)
			}
			n, err := RecoverTaskBilling(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			assertRecoveredTaskBalances(t, db, task, 4500)
			n, err = RecoverTaskBilling(context.Background(), 1)
			require.NoError(t, err)
			require.Zero(t, n)
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
		})
	}
}

func TestTaskRecoveryTransactionRollsBackAllQuotas(t *testing.T) {
	for _, table := range []string{"tokens", "tasks"} {
		t.Run(table, func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, ReserveTaskWalletQuota(task, 3000))
			persistTaskBillingOutcome(t, task, 4500, false)
			column := "remain_quota"
			if table == "tasks" {
				column = "quota"
			}
			require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER recovery_write_failure BEFORE UPDATE OF %s ON %s BEGIN SELECT RAISE(FAIL, 'recovery write failure'); END", column, table)).Error)
			n, err := RecoverTaskBilling(context.Background(), 1)
			require.ErrorContains(t, err, "recovery write failure")
			require.Zero(t, n)
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.Equal(t, 3000, stored.Quota)
			require.Equal(t, model.TaskBillingPending, stored.BillingState)
			require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
			require.NoError(t, db.Exec("DROP TRIGGER recovery_write_failure").Error)
			n, err = RecoverTaskBilling(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			assertRecoveredTaskBalances(t, db, task, 4500)
		})
	}
}

func TestTaskRecoveryAndPollingConcurrentSettlement(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	persistTaskBillingOutcome(t, task, 4500, false)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(recover bool) {
			defer wg.Done()
			copy := *task
			if recover {
				_, err := RecoverTaskBilling(context.Background(), 1)
				errs <- err
			} else {
				_, _, err := settleTaskWalletQuota(&copy, 4500, false)
				errs <- err
			}
		}(i%2 == 0)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assertRecoveredTaskBalances(t, db, task, 4500)
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestTaskOwnedReservationDoesNotStarveRecoveryOrExpiry(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	key, err := TaskWalletBillingKey(task)
	require.NoError(t, err)
	persistTaskBillingOutcome(t, task, 3000, false)
	require.NoError(t, model.PrepareWalletFinalization(db, key, task.UserId, 3000, 3000))
	orphan, err := ReservePendingTaskWalletQuota(task.UserId, 500)
	require.NoError(t, err)
	require.NoError(t, model.PrepareWalletFinalization(db, orphan, task.UserId, 500, 500))
	n, err := model.RecoverFinalizingWalletSettlements(db, 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	var owned model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&owned).Error)
	require.Equal(t, model.WalletReservationFinalizing, owned.State)

	// A live bound row before an orphan must not consume the expiry page.
	live := makeTask(task.UserId, 0, 0, task.PrivateData.TokenId, BillingSourceWallet, 0)
	require.NoError(t, db.Create(live).Error)
	require.NoError(t, ReserveTaskWalletQuota(live, 100))
	pending, err := ReservePendingMidjourneyQuota(task.UserId, task.PrivateData.TokenId, 200)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.WalletReservation{}).Where("state = ?", model.WalletReservationReserved).UpdateColumn("updated_at", time.Now().Add(-100*time.Hour)).Error)
	walletBefore, tokenBefore := getUserQuota(t, task.UserId), getTokenRemainQuota(t, task.PrivateData.TokenId)
	n, err = model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, walletBefore+200, getUserQuota(t, task.UserId))
	require.Equal(t, tokenBefore+200, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.NoError(t, RefundPendingMidjourneyQuota(pending, task.UserId))
	require.Equal(t, tokenBefore+200, getTokenRemainQuota(t, task.PrivateData.TokenId))
}
