package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionDowngradeTerminationPolicies(t *testing.T) {
	cases := []struct {
		name          string
		upgrade       string
		target        string
		previous      string
		current       string
		otherUpgrade  string
		otherStatus   string
		otherExpired  bool
		expectedGroup string
	}{
		{name: "explicit_without_upgrade", target: "basic", current: "manual", expectedGroup: "basic"},
		{name: "explicit_overrides_legacy_snapshot", upgrade: "vip", target: " basic ", previous: "default", current: "manual", expectedGroup: "basic"},
		{name: "legacy_restores_previous", upgrade: "vip", previous: "default", current: "vip", expectedGroup: "default"},
		{name: "legacy_preserves_changed_group", upgrade: "vip", previous: "default", current: "manual", expectedGroup: "manual"},
		{name: "legacy_without_upgrade", previous: "default", current: "manual", expectedGroup: "manual"},
		{name: "legacy_without_previous", upgrade: "vip", current: "vip", expectedGroup: "vip"},
		{name: "explicit_protected_by_active_upgrade", target: "basic", current: "premium", otherUpgrade: "premium", otherStatus: "active", expectedGroup: "premium"},
		{name: "legacy_protected_by_active_upgrade", upgrade: "vip", previous: "default", current: "vip", otherUpgrade: "vip", otherStatus: "active", expectedGroup: "vip"},
		{name: "quota_only_plan_remains_active", target: "basic", current: "vip", otherStatus: "active", expectedGroup: "basic"},
		{name: "cancelled_plan_does_not_block", target: "basic", current: "vip", otherUpgrade: "premium", otherStatus: "cancelled", expectedGroup: "basic"},
		{name: "expired_plan_does_not_block", target: "basic", current: "vip", otherUpgrade: "premium", otherStatus: "active", otherExpired: true, expectedGroup: "basic"},
	}
	for _, action := range []string{"expire", "cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					setupSubscriptionPurchaseLimitTest(t)
					user := createPurchaseLimitUser(t, "downgrade-policy")
					require.NoError(t, DB.Model(user).Update("group", tc.current).Error)
					plan := createLimitedSubscriptionPlan(t, 0)
					now := GetDBTimestamp()
					endTime := now + 3600
					if action == "expire" {
						endTime = now - 60
					}
					sub := &UserSubscription{
						UserId: user.Id, PlanId: plan.Id, Status: "active",
						StartTime: now - 7200, EndTime: endTime,
						UpgradeGroup: tc.upgrade, DowngradeGroup: tc.target, PrevUserGroup: tc.previous,
						AllowWalletOverflow: common.GetPointer(false), AmountTotal: 100, AmountUsed: 23,
					}
					require.NoError(t, DB.Create(sub).Error)
					membership := &UserMembership{UserId: user.Id, TierId: "gold", Source: MembershipSourceManual}
					require.NoError(t, DB.Create(membership).Error)
					var other *UserSubscription
					if tc.otherStatus != "" {
						otherEnd := now + 7200
						if tc.otherExpired {
							otherEnd = now - 120
						}
						other = &UserSubscription{
							UserId: user.Id, PlanId: plan.Id, Status: tc.otherStatus,
							StartTime: now - 7200, EndTime: otherEnd, UpgradeGroup: tc.otherUpgrade,
							AllowWalletOverflow: common.GetPointer(false), AmountTotal: 200, AmountUsed: 41,
						}
						require.NoError(t, DB.Create(other).Error)
					}

					switch action {
					case "expire":
						count, err := ExpireDueSubscriptions(200)
						require.NoError(t, err)
						expectedCount := 1
						if tc.otherStatus == "active" && tc.otherExpired {
							expectedCount++
						}
						require.Equal(t, expectedCount, count)
					case "cancel":
						_, err := AdminInvalidateUserSubscription(sub.Id)
						require.NoError(t, err)
					case "delete":
						_, err := AdminDeleteUserSubscription(sub.Id)
						require.NoError(t, err)
					}

					var actualUser User
					require.NoError(t, DB.First(&actualUser, user.Id).Error)
					require.Equal(t, tc.expectedGroup, actualUser.Group)
					require.Equal(t, user.Quota, actualUser.Quota)
					var actualMembership UserMembership
					require.NoError(t, DB.First(&actualMembership, membership.Id).Error)
					require.Equal(t, *membership, actualMembership)
					var actualSub UserSubscription
					err := DB.First(&actualSub, sub.Id).Error
					if action == "delete" {
						require.ErrorIs(t, err, gorm.ErrRecordNotFound)
					} else {
						require.NoError(t, err)
						expectedStatus := "expired"
						if action == "cancel" {
							expectedStatus = "cancelled"
						}
						require.Equal(t, expectedStatus, actualSub.Status)
						require.Equal(t, sub.DowngradeGroup, actualSub.DowngradeGroup)
						require.Equal(t, sub.AllowWalletOverflow, actualSub.AllowWalletOverflow)
						require.Equal(t, sub.AmountUsed, actualSub.AmountUsed)
					}
					if other != nil {
						var actualOther UserSubscription
						require.NoError(t, DB.First(&actualOther, other.Id).Error)
						if action == "expire" && tc.otherExpired && tc.otherStatus == "active" {
							require.Equal(t, "expired", actualOther.Status)
							require.Equal(t, other.AllowWalletOverflow, actualOther.AllowWalletOverflow)
							require.Equal(t, other.AmountUsed, actualOther.AmountUsed)
						} else {
							require.Equal(t, *other, actualOther)
						}
					}
				})
			}
		})
	}
}

func TestSubscriptionExpiryUsesLatestPolicyAndInstanceSnapshot(t *testing.T) {
	setupSubscriptionPurchaseLimitTest(t)
	user := createPurchaseLimitUser(t, "expiry-snapshot")
	require.NoError(t, DB.Model(user).Update("group", "manual").Error)
	plan := createLimitedSubscriptionPlan(t, 0)
	plan.AllowWalletOverflow = common.GetPointer(false)
	plan.DowngradeGroup = "basic"
	var sub *UserSubscription
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		sub, err = CreateUserSubscriptionFromPlanTx(tx, user.Id, plan, "admin")
		return err
	}))
	// Later plan edits must not change the policy of an already issued instance.
	*plan.AllowWalletOverflow = true
	plan.DowngradeGroup = "changed"
	require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
		"allow_wallet_overflow": true, "downgrade_group": "changed",
	}).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	permitted, err := UserActiveSubscriptionsAllowWalletOverflow(user.Id)
	require.NoError(t, err)
	require.False(t, permitted)
	now := GetDBTimestamp()
	require.NoError(t, DB.Model(sub).Update("end_time", now-60).Error)
	// An older upgraded instance must not hide a newer explicit-only policy.
	require.NoError(t, DB.Create(&UserSubscription{
		UserId: user.Id, PlanId: plan.Id, Status: "expired", EndTime: now - 120,
		UpgradeGroup: "manual", PrevUserGroup: "old-group",
	}).Error)
	count, err := ExpireDueSubscriptions(200)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	var actualUser User
	require.NoError(t, DB.First(&actualUser, user.Id).Error)
	require.Equal(t, "basic", actualUser.Group)
	var actualSub UserSubscription
	require.NoError(t, DB.First(&actualSub, sub.Id).Error)
	require.Equal(t, "basic", actualSub.DowngradeGroup)
	require.NotNil(t, actualSub.AllowWalletOverflow)
	require.False(t, *actualSub.AllowWalletOverflow)
	permitted, err = UserActiveSubscriptionsAllowWalletOverflow(user.Id)
	require.NoError(t, err)
	require.True(t, permitted)
}
