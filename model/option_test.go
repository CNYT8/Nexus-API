package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
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

	require.NoError(t, updateOptionMap("checkin_setting.stage_rules", `[{"request_threshold":5,"allow_checkin":true,"min_quota":1000,"max_quota":2000}]`))
	require.Len(t, setting.StageRules, 1)

	require.NoError(t, updateOptionMap("checkin_setting.stage_rules", ""))
	assert.Empty(t, setting.StageRules)
}
