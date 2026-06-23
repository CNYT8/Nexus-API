package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestStripChannelRestrictedAdminLogFields(t *testing.T) {
	logs := []*Log{
		{
			ChannelId:   7,
			ChannelName: "private-channel",
			Other: common.MapToJsonStr(map[string]interface{}{
				"channel_id":          7,
				"channel_name":        "private-channel",
				"channel_type":        1,
				"is_model_mapped":     true,
				"upstream_model_name": "upstream-model",
				"admin_info": map[string]interface{}{
					"use_channel":         []int{7, 8},
					"channel_affinity":    map[string]interface{}{"key": "value"},
					"is_multi_key":        true,
					"multi_key_index":     3,
					"upstream_model_name": "upstream-model",
				},
			}),
		},
	}

	StripChannelRestrictedAdminLogFields(logs)

	require.Equal(t, 0, logs[0].ChannelId)
	require.Empty(t, logs[0].ChannelName)
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	require.NotContains(t, other, "channel_id")
	require.NotContains(t, other, "channel_name")
	require.NotContains(t, other, "channel_type")
	require.NotContains(t, other, "is_model_mapped")
	require.NotContains(t, other, "upstream_model_name")

	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, true, adminInfo["is_multi_key"])
	require.Equal(t, float64(3), adminInfo["multi_key_index"])
	require.NotContains(t, adminInfo, "use_channel")
	require.NotContains(t, adminInfo, "channel_affinity")
	require.NotContains(t, adminInfo, "upstream_model_name")
}
