package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The returned task has a real atomic pre-consumption but is not yet inserted.
// Keep subscription tables local to this fixture instead of changing TestMain.
func setupPendingSubscriptionTask(t *testing.T, pre, submitted int) (*gorm.DB, *model.Task, *model.UserSubscription) {
	t.Helper()
	db, seed := setupTaskLedgerTest(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}))
	plan := &model.SubscriptionPlan{Title: "async-atomic-subscription", TotalAmount: 100000, QuotaResetPeriod: "never"}
	require.NoError(t, db.Create(plan).Error)
	now := time.Now().Unix()
	sub := &model.UserSubscription{UserId: seed.UserId, PlanId: plan.Id, AmountTotal: 100000,
		Status: "active", StartTime: now - 60, EndTime: now + 3600, QuotaPeriodStart: now - 60}
	require.NoError(t, db.Create(sub).Error)
	key := reservationTestKey(t, seed.UserId, "async-subscription-"+common.GetUUID())
	reserved, err := model.ReserveBillingSubscriptionQuota(db, key, seed.UserId, seed.PrivateData.TokenId, int64(pre))
	require.NoError(t, err)
	task := makeTask(seed.UserId, 0, submitted, seed.PrivateData.TokenId, BillingSourceSubscription, sub.Id)
	task.TaskID = "subscription-" + common.GetUUID()
	task.PrivateData.BillingKey = key
	task.PrivateData.SubscriptionPeriodStart = reserved.PeriodStart
	return db, task, sub
}

func readSubscriptionTaskReservation(t *testing.T, db *gorm.DB, task *model.Task) model.WalletReservation {
	t.Helper()
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", task.PrivateData.BillingKey).First(&reservation).Error)
	return reservation
}

func assertSubscriptionTaskBalances(t *testing.T, db *gorm.DB, task *model.Task, subID, actual int, subscriptionUsed int64) {
	t.Helper()
	require.Equal(t, subscriptionUsed, getSubscriptionUsed(t, subID))
	require.Equal(t, 10000-actual, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Equal(t, actual, getTokenUsedQuota(t, task.PrivateData.TokenId))
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestSubscriptionTaskInsertRollsBackAlignment(t *testing.T) {
	for _, failure := range []string{"token", "insert", "bind"} {
		t.Run(failure, func(t *testing.T) {
			db, task, sub := setupPendingSubscriptionTask(t, 3000, 4500)
			var trigger string
			switch failure {
			case "token":
				trigger = fmt.Sprintf("CREATE TRIGGER fail_subscription_submit BEFORE UPDATE ON tokens WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'subscription submit failed'); END", task.PrivateData.TokenId)
			case "insert":
				trigger = "CREATE TRIGGER fail_subscription_submit AFTER INSERT ON tasks BEGIN SELECT RAISE(ABORT, 'subscription submit failed'); END"
			case "bind":
				trigger = "CREATE TRIGGER fail_subscription_submit BEFORE UPDATE OF task_id ON wallet_reservations BEGIN SELECT RAISE(ABORT, 'subscription submit failed'); END"
			}
			require.NoError(t, db.Exec(trigger).Error)
			require.ErrorContains(t, task.Insert(), "subscription submit failed")
			var count int64
			require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", task.TaskID).Count(&count).Error)
			require.Zero(t, count)
			reservation := readSubscriptionTaskReservation(t, db, task)
			require.Equal(t, model.WalletReservationReserved, reservation.State)
			require.EqualValues(t, 3000, reservation.ReservedQuota)
			require.Empty(t, reservation.TaskKind)
			require.Zero(t, reservation.TaskID)
			assertSubscriptionTaskBalances(t, db, task, sub.Id, 3000, 3000)
			require.NoError(t, db.Exec("DROP TRIGGER fail_subscription_submit").Error)
			// A process can die before the controller invokes its refund. The
			// unowned original pre-consumption must still be recoverable by TTL.
			require.NoError(t, db.Model(&model.WalletReservation{}).Where("id = ?", reservation.ID).UpdateColumn("updated_at", time.Now().Add(-96*time.Hour)).Error)
			n, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			require.NoError(t, model.RefundBillingReservation(db, task.PrivateData.BillingKey, task.UserId))
			assertSubscriptionTaskBalances(t, db, task, sub.Id, 0, 0)
		})
	}
}

func TestSubscriptionTaskSubmitAlignmentTransfersOwnership(t *testing.T) {
	db, task, sub := setupPendingSubscriptionTask(t, 3000, 4500)
	require.NoError(t, task.Insert())
	reservation := readSubscriptionTaskReservation(t, db, task)
	require.EqualValues(t, 4500, reservation.ReservedQuota)
	require.Equal(t, model.WalletReservationReserved, reservation.State)
	require.Equal(t, "task", reservation.TaskKind)
	require.Equal(t, task.ID, reservation.TaskID)
	require.Equal(t, sub.Id, reservation.SubscriptionID)
	require.Equal(t, task.PrivateData.SubscriptionPeriodStart, reservation.PeriodStart)
	assertSubscriptionTaskBalances(t, db, task, sub.Id, 4500, 4500)
	require.Error(t, model.RefundBillingReservation(db, task.PrivateData.BillingKey, task.UserId))
	duplicate := *task
	duplicate.ID = 0
	require.ErrorIs(t, duplicate.Insert(), model.ErrWalletReservationConflict)
	assertSubscriptionTaskBalances(t, db, task, sub.Id, 4500, 4500)
	// The task owner, not generic reservation expiry, now controls refunds.
	require.NoError(t, db.Model(&model.WalletReservation{}).Where("id = ?", reservation.ID).UpdateColumn("updated_at", time.Now().Add(-96*time.Hour)).Error)
	n, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
	require.NoError(t, err)
	require.Zero(t, n)
	persistTaskBillingOutcome(t, task, 0, true)
	n, err = RecoverTaskBilling(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	assertSubscriptionTaskBalances(t, db, task, sub.Id, 0, 0)
}

func TestSubscriptionTaskTerminalFailureRecoversAtomically(t *testing.T) {
	for _, outcome := range []struct {
		name   string
		actual int
		refund bool
	}{{"success", 4500, false}, {"failure", 0, true}} {
		for _, write := range []string{"token", "task"} {
			t.Run(outcome.name+"/"+write, func(t *testing.T) {
				db, task, sub := setupPendingSubscriptionTask(t, 3000, 3000)
				require.NoError(t, task.Insert())
				persistTaskBillingOutcome(t, task, outcome.actual, outcome.refund)
				var trigger string
				if write == "token" {
					trigger = fmt.Sprintf("CREATE TRIGGER fail_subscription_terminal BEFORE UPDATE ON tokens WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'subscription terminal failed'); END", task.PrivateData.TokenId)
				} else {
					trigger = fmt.Sprintf("CREATE TRIGGER fail_subscription_terminal BEFORE UPDATE OF quota ON tasks WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'subscription terminal failed'); END", task.ID)
				}
				require.NoError(t, db.Exec(trigger).Error)
				applied, _, _, err := settleTaskQuota(task, outcome.actual, outcome.refund)
				require.ErrorContains(t, err, "subscription terminal failed")
				require.False(t, applied)
				assertSubscriptionTaskBalances(t, db, task, sub.Id, 3000, 3000)
				var stored model.Task
				require.NoError(t, db.First(&stored, task.ID).Error)
				require.Equal(t, 3000, stored.Quota)
				require.Equal(t, model.TaskBillingPending, stored.BillingState)
				require.Equal(t, outcome.actual, stored.BillingQuota)
				reservation := readSubscriptionTaskReservation(t, db, task)
				if outcome.refund {
					require.Equal(t, model.WalletReservationReserved, reservation.State)
				} else {
					require.Equal(t, model.WalletReservationFinalizing, reservation.State)
					require.EqualValues(t, outcome.actual, reservation.FinalQuota)
				}
				require.NoError(t, db.Exec("DROP TRIGGER fail_subscription_terminal").Error)
				n, err := RecoverTaskBilling(context.Background(), 10)
				require.NoError(t, err)
				require.Equal(t, 1, n)
				assertSubscriptionTaskBalances(t, db, task, sub.Id, outcome.actual, int64(outcome.actual))
				require.NoError(t, db.First(&stored, task.ID).Error)
				require.Equal(t, outcome.actual, stored.Quota)
				require.Equal(t, model.TaskBillingDone, stored.BillingState)
				applied, _, _, err = settleTaskQuota(task, outcome.actual, outcome.refund)
				require.NoError(t, err)
				require.False(t, applied)
				n, err = RecoverTaskBilling(context.Background(), 10)
				require.NoError(t, err)
				require.Zero(t, n)
				if !outcome.refund {
					_, _, _, err = settleTaskQuota(task, outcome.actual+1, false)
					require.ErrorIs(t, err, model.ErrWalletReservationConflict)
				}
				assertSubscriptionTaskBalances(t, db, task, sub.Id, outcome.actual, int64(outcome.actual))
			})
		}
	}
}

func TestSubscriptionTaskRefundKeepsPeriodIdentity(t *testing.T) {
	for _, refund := range []bool{false, true} {
		t.Run(fmt.Sprintf("refund=%v", refund), func(t *testing.T) {
			db, task, sub := setupPendingSubscriptionTask(t, 3000, 3000)
			require.NoError(t, task.Insert())
			nextPeriod := task.PrivateData.SubscriptionPeriodStart + 1
			require.NoError(t, db.Model(sub).Updates(map[string]interface{}{"quota_period_start": nextPeriod, "last_reset_time": nextPeriod, "amount_used": 800}).Error)
			actual := 1000
			if refund {
				actual = 0
			}
			persistTaskBillingOutcome(t, task, actual, refund)
			if refund {
				RefundTaskQuota(context.Background(), task, "old-period failure")
			} else {
				RecalculateTaskQuota(context.Background(), task, actual, "old-period difference")
			}
			require.Equal(t, model.TaskBillingDone, task.BillingState)
			assertSubscriptionTaskBalances(t, db, task, sub.Id, actual, 800)
			log := getLastLog(t)
			require.NotNil(t, log)
			var other map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			require.Equal(t, true, other["subscription_period_expired"])
			n, err := RecoverTaskBilling(context.Background(), 10)
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestSubscriptionTaskMissingEvidenceDoesNotReplay(t *testing.T) {
	db, task, sub := setupPendingSubscriptionTask(t, 3000, 3000)
	// A historical task lacks a durable key, even if its subscription ID and
	// quota happen to resemble a current reservation. Neither is proof.
	task.PrivateData.BillingKey = ""
	task.Status, task.BillingState, task.BillingQuota = model.TaskStatusFailure, model.TaskBillingPending, 0
	require.NoError(t, db.Create(task).Error)
	_, _, _, err := settleTaskQuota(task, 0, true)
	require.ErrorIs(t, err, ErrWalletBillingIdentityRequired)
	n, err := RecoverTaskBilling(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, n)
	assertSubscriptionTaskBalances(t, db, task, sub.Id, 3000, 3000)
}

func TestSubscriptionTaskMismatchedPeriodCannotBind(t *testing.T) {
	db, task, sub := setupPendingSubscriptionTask(t, 3000, 4500)
	task.PrivateData.SubscriptionPeriodStart++
	require.ErrorIs(t, task.Insert(), model.ErrWalletReservationConflict)
	reservation := readSubscriptionTaskReservation(t, db, task)
	require.Empty(t, reservation.TaskKind)
	require.Zero(t, reservation.TaskID)
	assertSubscriptionTaskBalances(t, db, task, sub.Id, 3000, 3000)
}
