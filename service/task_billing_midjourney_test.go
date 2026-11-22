package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestMidjourneyRecoveryRefundsWalletAndTokenOnce(t *testing.T) {
	for _, unlimited := range []bool{false, true} {
		t.Run(fmt.Sprintf("unlimited_%t", unlimited), func(t *testing.T) {
			db, seed := setupTaskLedgerTest(t)
			require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
			initialToken := 10000
			if unlimited {
				initialToken = 0
			}
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", seed.PrivateData.TokenId).Updates(map[string]interface{}{"unlimited_quota": unlimited, "remain_quota": initialToken}).Error)
			key, err := ReservePendingMidjourneyQuota(seed.UserId, seed.PrivateData.TokenId, 3000)
			require.NoError(t, err)
			task := &model.Midjourney{UserId: seed.UserId, Quota: 3000, BillingKey: key, TokenId: seed.PrivateData.TokenId, BillingSource: BillingSourceWallet, Status: "IN_PROGRESS", MjId: "mj-refund", Action: "IMAGINE"}
			require.NoError(t, task.Insert())
			var loaded model.Midjourney
			require.NoError(t, db.First(&loaded, task.Id).Error)
			require.Equal(t, seed.PrivateData.TokenId, loaded.TokenId)
			require.Equal(t, BillingSourceWallet, loaded.BillingSource)
			loaded.Status, loaded.Progress = "FAILURE", "100%"
			won, err := loaded.UpdateWithStatus("IN_PROGRESS")
			require.NoError(t, err)
			require.True(t, won)
			// Simulate a crash after the failure CAS, then race both callback
			// consumers with the recovery worker using stale copies.
			var wg sync.WaitGroup
			errs := make(chan error, 10)
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func(recover bool) {
					defer wg.Done()
					copy := loaded
					if recover {
						_, err := recoverMidjourneyTaskBilling(context.Background(), 1)
						errs <- err
					} else {
						_, err := SettleMidjourneyTaskBilling(context.Background(), &copy)
						errs <- err
					}
				}(i%2 == 0)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			require.Equal(t, 10000, getUserQuota(t, seed.UserId))
			require.Equal(t, initialToken, getTokenRemainQuota(t, seed.PrivateData.TokenId))
			var token model.Token
			require.NoError(t, db.First(&token, seed.PrivateData.TokenId).Error)
			require.Zero(t, token.UsedQuota)
			require.NoError(t, db.First(&loaded, task.Id).Error)
			require.Zero(t, loaded.Quota)
			require.Equal(t, model.TaskBillingDone, loaded.BillingState)
			require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
			var refundLogs int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", seed.UserId, model.LogTypeRefund).Count(&refundLogs).Error)
			require.EqualValues(t, 1, refundLogs)
		})
	}
}

func TestMidjourneyRefundRollsBackWithTokenFailure(t *testing.T) {
	db, seed := setupTaskLedgerTest(t)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	key, err := ReservePendingMidjourneyQuota(seed.UserId, seed.PrivateData.TokenId, 3000)
	require.NoError(t, err)
	task := &model.Midjourney{UserId: seed.UserId, Quota: 3000, BillingKey: key, TokenId: seed.PrivateData.TokenId, Status: "FAILURE", MjId: "mj-rollback"}
	require.NoError(t, task.Insert())
	require.NoError(t, db.Exec("CREATE TRIGGER mj_token_failure BEFORE UPDATE OF remain_quota ON tokens BEGIN SELECT RAISE(FAIL, 'mj token failure'); END").Error)
	_, err = SettleMidjourneyTaskBilling(context.Background(), task)
	require.ErrorContains(t, err, "mj token failure")
	require.Equal(t, 7000, getUserQuota(t, seed.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, seed.PrivateData.TokenId))
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationReserved, reservation.State)
	var stored model.Midjourney
	require.NoError(t, db.First(&stored, task.Id).Error)
	require.Equal(t, 3000, stored.Quota)
	require.Equal(t, model.TaskBillingPending, stored.BillingState)
	require.NoError(t, db.Exec("DROP TRIGGER mj_token_failure").Error)
	n, err := recoverMidjourneyTaskBilling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 10000, getUserQuota(t, seed.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, seed.PrivateData.TokenId))
}

func TestMidjourneyRecoveryCompletesLegacyWalletOnlyRefund(t *testing.T) {
	db, seed := setupTaskLedgerTest(t)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	key, err := ReservePendingTaskWalletQuota(seed.UserId, 3000)
	require.NoError(t, err)
	require.NoError(t, model.DecreaseTokenQuotaDirect(seed.PrivateData.TokenId, "", 3000))
	// Reconstruct a task written before reservation ownership/token metadata.
	task := &model.Midjourney{UserId: seed.UserId, Quota: 3000, BillingKey: key, TokenId: seed.PrivateData.TokenId,
		BillingSource: BillingSourceWallet, Status: "FAILURE", MjId: "mj-legacy-wallet-refund", Action: "IMAGINE",
		BillingState: model.TaskBillingPending, BillingQuota: 0}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, model.RefundWalletReservation(db, key, seed.UserId))
	require.Equal(t, 10000, getUserQuota(t, seed.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, seed.PrivateData.TokenId))
	processed, err := recoverMidjourneyTaskBilling(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	processed, err = recoverMidjourneyTaskBilling(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, processed)
	require.Equal(t, 10000, getUserQuota(t, seed.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, seed.PrivateData.TokenId))
}

func TestMidjourneyPendingRefundAndReservationAreAtomic(t *testing.T) {
	db, seed := setupTaskLedgerTest(t)
	require.NoError(t, db.Exec("CREATE TRIGGER mj_reserve_failure BEFORE UPDATE OF remain_quota ON tokens BEGIN SELECT RAISE(FAIL, 'mj reserve failure'); END").Error)
	_, err := ReservePendingMidjourneyQuota(seed.UserId, seed.PrivateData.TokenId, 3000)
	require.ErrorContains(t, err, "mj reserve failure")
	require.Equal(t, 10000, getUserQuota(t, seed.UserId))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletReservation{}))
	require.NoError(t, db.Exec("DROP TRIGGER mj_reserve_failure").Error)
	key, err := ReservePendingMidjourneyQuota(seed.UserId, seed.PrivateData.TokenId, 3000)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		require.NoError(t, RefundPendingMidjourneyQuota(key, seed.UserId))
	}
	require.Equal(t, 10000, getUserQuota(t, seed.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, seed.PrivateData.TokenId))
}
