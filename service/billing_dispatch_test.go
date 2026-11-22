package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestBillingDispatchProtectsFailedPrepareFromExpiry(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			if legacy {
				require.NoError(t, db.Where(map[string]interface{}{"key": "wallet_credit_baseline_v1"}).Delete(&model.RebateSystemState{}).Error)
				require.NoError(t, db.Where("user_id = ?", task.UserId).Delete(&model.WalletCreditLot{}).Error)
			}
			ctx, info := newWalletBillingTestContext(task.UserId, task.PrivateData.TokenId, "task-token", 10000)
			session, apiErr := NewBillingSession(ctx, info, 3000)
			require.Nil(t, apiErr)
			stopBillingTestLease(t, session)
			info.Billing = session
			require.NoError(t, MarkBillingDispatched(info))
			require.NoError(t, MarkBillingDispatched(info))
			key := session.WalletReservationKey()
			require.NoError(t, db.Exec("CREATE TRIGGER fail_billing_prepare BEFORE UPDATE ON wallet_reservations WHEN NEW.state = 'finalizing' BEGIN SELECT RAISE(ABORT, 'billing prepare write failed'); END").Error)
			require.ErrorContains(t, session.Settle(4000), "billing prepare write failed")
			session.Refund(ctx)
			require.NoError(t, db.Model(&model.WalletReservation{}).Where("reservation_key = ?", key).UpdateColumn("updated_at", time.Now().Add(-96*time.Hour)).Error)
			count, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
			require.Equal(t, model.WalletReservationReserved, reservation.State)
			require.Equal(t, model.BillingDispatchStarted, reservation.DispatchState)
			require.NoError(t, db.Exec("DROP TRIGGER fail_billing_prepare").Error)
			require.NoError(t, session.Settle(4000))
			require.NoError(t, session.Settle(4000))
			require.Equal(t, 6000, getUserQuota(t, task.UserId))
			require.Equal(t, 6000, getTokenRemainQuota(t, task.PrivateData.TokenId))
		})
	}
}

func TestBillingDispatchFailurePreservesPendingReservation(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	ctx, info := newWalletBillingTestContext(task.UserId, task.PrivateData.TokenId, "task-token", 10000)
	session, apiErr := NewBillingSession(ctx, info, 3000)
	require.Nil(t, apiErr)
	stopBillingTestLease(t, session)
	info.Billing = session
	key := session.WalletReservationKey()
	require.NoError(t, db.Exec("CREATE TRIGGER fail_billing_dispatch BEFORE UPDATE ON wallet_reservations WHEN NEW.dispatch_state = 'dispatched' BEGIN SELECT RAISE(ABORT, 'billing dispatch write failed'); END").Error)
	require.ErrorContains(t, MarkBillingDispatched(info), "billing dispatch write failed")
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.BillingDispatchPending, reservation.DispatchState)
	require.Equal(t, 7000, getUserQuota(t, task.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.NoError(t, db.Exec("DROP TRIGGER fail_billing_dispatch").Error)
	require.NoError(t, model.RefundBillingReservation(db, key, task.UserId))
	require.ErrorIs(t, model.MarkBillingReservationDispatched(db, key, task.UserId), model.ErrWalletReservationConflict)
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
}

func TestBillingDispatchUnknownLegacyRowsRequireExplicitRefund(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	key := reservationTestKey(t, task.UserId, "legacy-dispatch-unknown")
	require.NoError(t, model.ReserveBillingWalletQuota(db, key, task.UserId, task.PrivateData.TokenId, 3000))
	require.NoError(t, db.Model(&model.WalletReservation{}).Where("reservation_key = ?", key).Updates(map[string]interface{}{
		"dispatch_state": "", "updated_at": time.Now().Add(-96 * time.Hour),
	}).Error)
	count, err := model.ExpireStaleWalletReservations(db, time.Now().Add(-72*time.Hour), 10)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, 7000, getUserQuota(t, task.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Error(t, model.MarkBillingReservationDispatched(db, key, task.UserId+1000))
	require.NoError(t, model.MarkBillingReservationDispatched(db, key, task.UserId))
	require.NoError(t, model.RefundBillingReservation(db, key, task.UserId))
	require.NoError(t, model.RefundBillingReservation(db, key, task.UserId))
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
}
