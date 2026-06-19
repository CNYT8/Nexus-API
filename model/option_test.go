package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
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
