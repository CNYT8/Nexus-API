package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPolicySnapshotKeepsLegacyWalletFallback(t *testing.T) {
	setupSubscriptionPurchaseLimitTest(t)
	user := createPurchaseLimitUser(t, "policy-snapshot")
	plan := createLimitedSubscriptionPlan(t, 0)
	allow := false
	plan.AllowWalletOverflow, plan.DowngradeGroup = &allow, "basic"
	sub, err := CreateUserSubscriptionFromPlanTx(DB, user.Id, plan, "test")
	require.NoError(t, err)
	require.False(t, *sub.AllowWalletOverflow)
	allow, plan.DowngradeGroup = true, "changed"
	require.False(t, *sub.AllowWalletOverflow)
	require.Equal(t, "basic", subscriptionDowngradeGroup(sub))
	permitted, err := UserActiveSubscriptionsAllowWalletOverflow(user.Id)
	require.NoError(t, err)
	require.False(t, permitted)
	require.NoError(t, DB.Model(sub).Update("end_time", time.Now().Unix()-1).Error)
	permitted, err = UserActiveSubscriptionsAllowWalletOverflow(user.Id)
	require.NoError(t, err)
	require.True(t, permitted)
	legacy := &UserSubscription{UserId: user.Id, PlanId: plan.Id, Status: "active", EndTime: time.Now().Unix() + 3600, PrevUserGroup: "previous"}
	require.NoError(t, DB.Create(legacy).Error)
	permitted, err = UserActiveSubscriptionsAllowWalletOverflow(user.Id)
	require.NoError(t, err)
	require.True(t, permitted)
	require.Equal(t, "previous", subscriptionDowngradeGroup(legacy))
	require.NoError(t, DB.Model(legacy).Update("allow_wallet_overflow", common.GetPointer(false)).Error)
	permitted, err = UserActiveSubscriptionsAllowWalletOverflow(user.Id)
	require.NoError(t, err)
	require.False(t, permitted)
}
