package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestRebateMaintenanceDrainRejectsNewWorkAndTimesOut(t *testing.T) {
	SetRebateMaintenancePaused(false)
	require.True(t, BeginTaskBillingWork())
	ended := false
	t.Cleanup(func() {
		if !ended {
			EndTaskBillingWork()
		}
		SetRebateMaintenancePaused(false)
	})
	SetRebateMaintenancePaused(true)
	require.False(t, BeginTaskBillingWork())
	require.False(t, WaitForRebateTasksIdle(0))
	EndTaskBillingWork()
	ended = true
	require.True(t, WaitForRebateTasksIdle(0))
	SetRebateMaintenancePaused(false)
	require.True(t, BeginTaskBillingWork())
	EndTaskBillingWork()
}

func TestRebateMaintenancePausesTaskRecoveryAndExpiry(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	persistTaskBillingOutcome(t, task, 4500, false)
	key, err := ReservePendingMidjourneyQuota(task.UserId, task.PrivateData.TokenId, 500)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.WalletReservation{}).Where("reservation_key = ?", key).UpdateColumn("updated_at", time.Now().Add(-100*time.Hour)).Error)

	SetRebateMaintenancePaused(true)
	t.Cleanup(func() { SetRebateMaintenancePaused(false) })
	runRebateRecoveryOnce()
	runStaleReservationExpiryOnce()
	require.Equal(t, 6500, getUserQuota(t, task.UserId))
	require.Equal(t, 6500, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.True(t, WaitForRebateTasksIdle(0))

	SetRebateMaintenancePaused(false)
	runRebateRecoveryOnce()
	runStaleReservationExpiryOnce()
	assertRecoveredTaskBalances(t, db, task, 4500)
	require.True(t, WaitForRebateTasksIdle(0))
}
