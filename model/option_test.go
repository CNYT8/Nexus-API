package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	error_mask_setting "github.com/QuantumNous/new-api/setting/error_mask_setting"
	membership_setting "github.com/QuantumNous/new-api/setting/membership_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionMapDefaultRecordIpLogForced(t *testing.T) {
	oldOptionMap := common.OptionMap
	oldForced := common.DefaultRecordIpLogForced
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		common.DefaultRecordIpLogForced = oldForced
	})

	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	common.DefaultRecordIpLogForced = false

	require.NoError(t, updateOptionMap("DefaultRecordIpLogForced", "true"))
	assert.True(t, common.DefaultRecordIpLogForced)

	common.OptionMapRWMutex.RLock()
	assert.Equal(t, "true", common.OptionMap["DefaultRecordIpLogForced"])
	common.OptionMapRWMutex.RUnlock()

	require.NoError(t, updateOptionMap("DefaultRecordIpLogForced", "false"))
	assert.False(t, common.DefaultRecordIpLogForced)
}

func TestUpdateOptionMapClearsCheckinStageRules(t *testing.T) {
	oldOptionMap := common.OptionMap
	setting := operation_setting.GetCheckinSetting()
	oldRules := setting.StageRules
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		setting.StageRules = oldRules
	})

	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()

	require.NoError(t, updateOptionMap("checkin_setting.stage_rules", `[{"request_threshold":5,"amount_threshold":100,"allow_checkin":true,"min_quota":1000,"max_quota":2000}]`))
	require.Len(t, setting.StageRules, 1)
	assert.Equal(t, 100, setting.StageRules[0].AmountThreshold)

	require.NoError(t, updateOptionMap("checkin_setting.stage_rules", ""))
	assert.Empty(t, setting.StageRules)
}

func TestUpdateOptionRejectsInvalidCheckinStageAmountThreshold(t *testing.T) {
	require.Error(t, updateOptionMap("checkin_setting.stage_rules", `[{"amount_threshold":-1,"allow_checkin":true,"min_quota":1000,"max_quota":2000}]`))
}

func TestUpdateOptionRejectsInvalidMembershipTiersBeforePersist(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where("key = ?", "membership_setting.tiers").Delete(&Option{}).Error)

	oldOptionMap := common.OptionMap
	setting := membership_setting.GetMembershipSetting()
	oldTiers := setting.Tiers
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		setting.Tiers = oldTiers
		DB.Where("key = ?", "membership_setting.tiers").Delete(&Option{})
	})

	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()

	validValue := `[{"id":"bronze","name":"Bronze","threshold_amount":10,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":false,"all_group_discount":1,"group_discounts":[]}]`
	require.NoError(t, UpdateOption("membership_setting.tiers", validValue))

	invalidValue := `[{"id":"bronze","name":"Bronze"},{"id":"bronze","name":"Duplicate"}]`
	require.Error(t, UpdateOption("membership_setting.tiers", invalidValue))
	require.Error(t, updateOptionMap("membership_setting.tiers", invalidValue))

	var option Option
	require.NoError(t, DB.Where("key = ?", "membership_setting.tiers").First(&option).Error)
	assert.Equal(t, validValue, option.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, validValue, common.OptionMap["membership_setting.tiers"])
	common.OptionMapRWMutex.RUnlock()

	tiers := membership_setting.GetTiers()
	require.Len(t, tiers, 1)
	assert.Equal(t, "bronze", tiers[0].Id)
}

func TestUpdateOptionRejectsInvalidErrorMaskRulesBeforePersist(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where("key = ?", "error_mask_setting.rules").Delete(&Option{}).Error)

	oldOptionMap := common.OptionMap
	oldRules := error_mask_setting.RulesJSONString()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, error_mask_setting.UpdateRulesByJSONString(oldRules))
		DB.Where("key = ?", "error_mask_setting.rules").Delete(&Option{})
	})

	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()

	validValue := `[{"status":429,"pattern":"quota","replacement":"masked error"}]`
	require.NoError(t, UpdateOption("error_mask_setting.rules", validValue))

	invalidValue := `[{"status":99,"pattern":"quota","replacement":"bad status"}]`
	require.Error(t, UpdateOption("error_mask_setting.rules", invalidValue))
	require.Error(t, updateOptionMap("error_mask_setting.rules", invalidValue))

	var option Option
	require.NoError(t, DB.Where("key = ?", "error_mask_setting.rules").First(&option).Error)
	assert.Equal(t, validValue, option.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, validValue, common.OptionMap["error_mask_setting.rules"])
	common.OptionMapRWMutex.RUnlock()

	rules := error_mask_setting.GetSetting().Rules
	require.Len(t, rules, 1)
	assert.Equal(t, 429, rules[0].Status)
	assert.Equal(t, "masked error", rules[0].Replacement)
}
