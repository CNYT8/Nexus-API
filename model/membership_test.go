package model

import (
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	membership_setting "github.com/QuantumNous/new-api/setting/membership_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetMembershipForTest(t *testing.T) {
	t.Helper()
	oldQuotaPerUnit := common.QuotaPerUnit
	setting := membership_setting.GetMembershipSetting()
	setting.Enabled = false
	setting.Tiers = nil
	userMembershipCache = sync.Map{}
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		setting := membership_setting.GetMembershipSetting()
		setting.Enabled = false
		setting.Tiers = nil
		userMembershipCache = sync.Map{}
	})
}

func insertMembershipTopUpForTest(t *testing.T, userId int, provider string, amount int64, money float64) {
	t.Helper()
	require.NoError(t, DB.Create(&TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           money,
		TradeNo:         provider + "-membership-test",
		PaymentMethod:   provider,
		PaymentProvider: provider,
		Status:          common.TopUpStatusSuccess,
		CreateTime:      common.GetTimestamp(),
	}).Error)
}

func TestMembershipDisabledKeepsGroupRatio(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)

	ratio, info := ApplyMembershipDiscount(1, "default", 1.5)

	assert.Equal(t, 1.5, ratio)
	assert.False(t, info.Applied)
}

func TestNormalizeMembershipRatioRemovesFloatNoise(t *testing.T) {
	groupRatio := 0.8
	discount := 0.7
	assert.Equal(t, "0.56", strconv.FormatFloat(normalizeMembershipRatio(groupRatio*discount), 'f', -1, 64))

	groupRatio = 0.1
	discount = 0.5
	assert.Equal(t, "0.05", strconv.FormatFloat(normalizeMembershipRatio(groupRatio*discount), 'f', -1, 64))
	assert.Equal(t, "1.000001", strconv.FormatFloat(normalizeMembershipRatio(1.000001), 'f', -1, 64))
}

func TestGetUserCumulativeTopUpAmount(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	common.QuotaPerUnit = 100

	userId := 501
	insertMembershipTopUpForTest(t, userId, PaymentProviderEpay, 10, 8)
	insertMembershipTopUpForTest(t, userId, PaymentProviderStripe, 50, 20)
	insertMembershipTopUpForTest(t, userId, PaymentProviderCreem, 300, 30)
	require.NoError(t, DB.Create(&TopUp{
		UserId:          userId,
		Amount:          999,
		Money:           999,
		TradeNo:         "pending-membership-test",
		PaymentMethod:   PaymentProviderEpay,
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
		CreateTime:      common.GetTimestamp(),
	}).Error)

	total, err := GetUserCumulativeTopUpAmount(userId)

	require.NoError(t, err)
	assert.InDelta(t, 33, total, 0.0001)
}

func TestAdminQuotaGrantCountsTowardCumulativeTopUpAmount(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	common.QuotaPerUnit = 100

	user := &User{
		Username: "membership_admin_grant",
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, DB.Create(user).Error)
	insertMembershipTopUpForTest(t, user.Id, PaymentProviderEpay, 10, 10)

	require.NoError(t, IncreaseUserQuotaWithMembershipGrant(user.Id, 250, 1))

	total, err := GetUserCumulativeTopUpAmount(user.Id)
	require.NoError(t, err)
	assert.InDelta(t, 12.5, total, 0.0001)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 250, refreshed.Quota)

	var grant MembershipQuotaGrant
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&grant).Error)
	assert.Equal(t, 1, grant.OperatorId)
	assert.Equal(t, 250, grant.Quota)
	assert.InDelta(t, 2.5, grant.Amount, 0.0001)
}

func TestAdminQuotaGrantTriggersMembershipAutoUpgrade(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	common.QuotaPerUnit = 100
	setting := membership_setting.GetMembershipSetting()
	setting.Enabled = true
	require.NoError(t, membership_setting.UpdateTiersByJSONString(`[
		{"id":"plus","name":"Plus","threshold_amount":2,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":false,"all_group_discount":1,"group_discounts":[]}
	]`))

	user := &User{
		Username: "membership_admin_upgrade",
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, IncreaseUserQuotaWithMembershipGrant(user.Id, 250, 1))

	tierId, source := GetUserMembershipTier(user.Id)
	assert.Equal(t, "plus", tierId)
	assert.Equal(t, MembershipSourceAuto, source)
}

func TestAdminQuotaGrantRollsBackForMissingUser(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	common.QuotaPerUnit = 100

	err := IncreaseUserQuotaWithMembershipGrant(99999, 250, 1)
	require.Error(t, err)

	var count int64
	require.NoError(t, DB.Model(&MembershipQuotaGrant{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestValidateMembershipTierId(t *testing.T) {
	resetMembershipForTest(t)
	require.NoError(t, membership_setting.UpdateTiersByJSONString(`[
		{"id":"bronze","name":"Bronze","threshold_amount":10,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":false,"all_group_discount":1,"group_discounts":[]}
	]`))

	assert.NoError(t, ValidateMembershipTierId(""))
	assert.NoError(t, ValidateMembershipTierId(" bronze "))
	assert.Error(t, ValidateMembershipTierId("missing"))
}

func TestMembershipAutoUpgradeDoesNotDowngradeManualTier(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	common.QuotaPerUnit = 100
	setting := membership_setting.GetMembershipSetting()
	setting.Enabled = true
	require.NoError(t, membership_setting.UpdateTiersByJSONString(`[
		{"id":"bronze","name":"Bronze","threshold_amount":10,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":false,"all_group_discount":1,"group_discounts":[]},
		{"id":"gold","name":"Gold","threshold_amount":1000,"auto_upgrade_enabled":true,"enabled":true,"sort_order":2,"discount_all_groups":true,"all_group_discount":0.8,"group_discounts":[]}
	]`))

	userId := 502
	require.NoError(t, DB.Create(&User{
		Id:       userId,
		Username: "membership_user",
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
	insertMembershipTopUpForTest(t, userId, PaymentProviderEpay, 20, 20)

	require.NoError(t, AutoUpgradeUserMembership(userId))
	tierId, source := GetUserMembershipTier(userId)
	assert.Equal(t, "bronze", tierId)
	assert.Equal(t, MembershipSourceAuto, source)

	require.NoError(t, SetUserMembershipTier(userId, "gold", MembershipSourceManual))
	require.NoError(t, AutoUpgradeUserMembership(userId))

	tierId, source = GetUserMembershipTier(userId)
	assert.Equal(t, "gold", tierId)
	assert.Equal(t, MembershipSourceManual, source)

	ratio, info := ApplyMembershipDiscount(userId, "default", 2)
	assert.True(t, info.Applied)
	assert.Equal(t, "gold", info.TierId)
	assert.InDelta(t, 1.6, ratio, 0.0001)
}

func TestMembershipDiscountCanOverrideGroupRatio(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	setting := membership_setting.GetMembershipSetting()
	setting.Enabled = true
	require.NoError(t, membership_setting.UpdateTiersByJSONString(`[
		{"id":"gold","name":"Gold","threshold_amount":0,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":true,"all_group_discount":0.7,"all_group_stack_ratio":false,"group_discounts":[]}
	]`))

	userId := 503
	require.NoError(t, DB.Create(&User{
		Id:       userId,
		Username: "membership_override_user",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "membership_override_user",
	}).Error)
	require.NoError(t, SetUserMembershipTier(userId, "gold", MembershipSourceManual))

	ratio, info := ApplyMembershipDiscount(userId, "default", 0.8)

	assert.True(t, info.Applied)
	assert.False(t, info.StackGroupRatio)
	assert.InDelta(t, 0.7, ratio, 0.0001)
}

func TestMembershipGroupDiscountCanStackGroupRatio(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	setting := membership_setting.GetMembershipSetting()
	setting.Enabled = true
	require.NoError(t, membership_setting.UpdateTiersByJSONString(`[
		{"id":"gold","name":"Gold","threshold_amount":0,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":false,"all_group_discount":1,"group_discounts":[{"group":"vip","discount":0.7,"stack_group_ratio":true}]}
	]`))

	userId := 504
	require.NoError(t, DB.Create(&User{
		Id:       userId,
		Username: "membership_stack_user",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "membership_stack_user",
	}).Error)
	require.NoError(t, SetUserMembershipTier(userId, "gold", MembershipSourceManual))

	ratio, info := ApplyMembershipDiscount(userId, "vip", 0.8)

	assert.True(t, info.Applied)
	assert.True(t, info.StackGroupRatio)
	assert.InDelta(t, 0.56, ratio, 0.0001)
}

func TestSearchUsersByMembershipTier(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)

	goldUser := &User{
		Username: "member_search_gold",
		Password: "password123",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "member_search_gold",
	}
	silverUser := &User{
		Username: "member_search_silver",
		Password: "password123",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "member_search_silver",
	}
	require.NoError(t, DB.Create(goldUser).Error)
	require.NoError(t, DB.Create(silverUser).Error)
	require.NoError(t, DB.Create(&UserMembership{
		UserId: goldUser.Id,
		TierId: "gold",
		Source: MembershipSourceManual,
	}).Error)
	require.NoError(t, DB.Create(&UserMembership{
		UserId: silverUser.Id,
		TierId: "silver",
		Source: MembershipSourceManual,
	}).Error)

	users, total, err := SearchUsers("member_search_", "", "gold", nil, nil, 0, 10)

	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Equal(t, goldUser.Id, users[0].Id)
}
