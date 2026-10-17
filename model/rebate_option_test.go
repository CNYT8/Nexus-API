package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestRebateOptionUpdatesValidateBeforePersistAndApplyAtomically(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where("key LIKE ?", "rebate_setting.%").Delete(&Option{}).Error)

	oldSetting := operation_setting.GetRebateSetting()
	oldOptionMap := common.OptionMap
	t.Cleanup(func() {
		require.NoError(t, operation_setting.UpdateRebateSetting(oldSetting))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, DB.Where("key LIKE ?", "rebate_setting.%").Delete(&Option{}).Error)
	})

	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, operation_setting.UpdateRebateSetting(RebateSettingForTest()))

	require.NoError(t, UpdateOption("rebate_setting.rate_basis_points", "500"))
	asserted := operation_setting.GetRebateSetting()
	require.EqualValues(t, 500, asserted.RateBasisPoints)
	require.Equal(t, RebateSettingForTest().PayoutMode, asserted.PayoutMode)

	err := UpdateOption("rebate_setting.rate_basis_points", "10001")
	require.Error(t, err)
	var persisted Option
	require.NoError(t, DB.Where("key = ?", "rebate_setting.rate_basis_points").First(&persisted).Error)
	require.Equal(t, "500", persisted.Value)

	require.NoError(t, UpdateOption("rebate_setting.enabled", "true"))
	require.True(t, operation_setting.GetRebateSetting().Enabled)
}

func TestRebateOptionRejectsUnknownOrMalformedFields(t *testing.T) {
	require.Error(t, validateOptionBeforeSave("rebate_setting.unknown", "value"))
	require.Error(t, validateOptionBeforeSave("rebate_setting.enabled", "yes"))
}
