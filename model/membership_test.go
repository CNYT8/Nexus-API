package model

import (
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
