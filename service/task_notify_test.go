package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestNotifyTaskQuotaConsumedReloadsUserAndSkipsInvalidInput(t *testing.T) {
	db := setupWalletSettlementTestDB(t)
	var calls []int
	original := quotaNotifySender
	quotaNotifySender = func(info *relaycommon.RelayInfo) {
		require.Equal(t, 500, info.UserQuota)
		calls = append(calls, info.UserQuota)
	}
	t.Cleanup(func() { quotaNotifySender = original })

	user := model.User{Username: "notify-user", AffCode: "notify-aff", Quota: 500, Status: 1}
	require.NoError(t, db.Create(&user).Error)

	NotifyTaskQuotaConsumed(context.Background(), user.Id, 100)
	require.Equal(t, []int{500}, calls)

	NotifyTaskQuotaConsumed(context.Background(), user.Id, 0)
	NotifyTaskQuotaConsumed(context.Background(), 0, 100)
	NotifyTaskQuotaConsumed(context.Background(), user.Id+999, 100)
	require.Equal(t, []int{500}, calls)
}

func TestLedgerTaskCompletionNotifiesExactlyOnce(t *testing.T) {
	db, user, token := setupPendingTaskWalletSubmitTest(t)

	var calls []int
	original := quotaNotifySender
	quotaNotifySender = func(info *relaycommon.RelayInfo) {
		calls = append(calls, info.UserQuota)
	}
	t.Cleanup(func() { quotaNotifySender = original })

	key, err := ReservePendingTaskWalletQuota(user.Id, 3000)
	require.NoError(t, err)
	task := insertPendingTaskRow(t, db, user.Id, token.Id, 3000, key, model.TaskStatusSuccess)

	RecalculateTaskQuota(context.Background(), task, 3000, "notify test")
	require.Equal(t, []int{7000}, calls)

	RecalculateTaskQuota(context.Background(), task, 3000, "notify test")
	require.Equal(t, []int{7000}, calls)

	var settlementCount int64
	require.NoError(t, db.Model(&model.WalletConsumptionSettlement{}).Where("user_id = ?", user.Id).Count(&settlementCount).Error)
	require.EqualValues(t, 1, settlementCount)
	var rebates int64
	require.NoError(t, db.Model(&model.RebateRecord{}).Count(&rebates).Error)
	require.EqualValues(t, 1, rebates)
}
