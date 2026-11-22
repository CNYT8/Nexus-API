package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestChannelOpsAutomaticDisablePolicySources(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	originalDisable, originalRetries := common.AutomaticDisableChannelEnabled, common.RetryTimes
	common.RetryTimes = 3
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.AutomaticDisableChannelEnabled, common.RetryTimes = originalDisable, originalRetries
		common.OptionMapRWMutex.Unlock()
	})
	for _, tc := range []struct {
		global  bool
		query   string
		enabled bool
		source  string
	}{
		{false, "", false, "global"},
		{true, "", true, "global"},
		{false, "auto_ban=true", false, "global"},
		{false, "auto_ban=false", false, "global"},
		{true, "auto_ban=true", true, "global_and_channel"},
		{true, "auto_ban=false", false, "channel"},
		{true, "auto_ban=0", false, "channel"},
	} {
		common.OptionMapRWMutex.Lock()
		common.AutomaticDisableChannelEnabled = tc.global
		common.OptionMapRWMutex.Unlock()
		response := invokeLogPolicyHandler(GetChannelOps, 27, common.RoleRootUser, tc.query)
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				RetryTimes int `json:"retry_times"`
				Policy     struct {
					AutomaticDisable bool   `json:"automatic_disable"`
					Source           string `json:"source"`
				} `json:"request_policy"`
			} `json:"data"`
		}
		require.Equal(t, http.StatusOK, response.Code)
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		require.Equal(t, 3, result.Data.RetryTimes)
		require.Equal(t, tc.enabled, result.Data.Policy.AutomaticDisable)
		require.Equal(t, tc.source, result.Data.Policy.Source)
	}
	response := invokeLogPolicyHandler(GetChannelOps, 27, common.RoleRootUser, "auto_ban=invalid")
	var invalid struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &invalid))
	require.False(t, invalid.Success)
}

func TestChannelDefaultBaseURLsAdminOnlyAndNonempty(t *testing.T) {
	require.Equal(t, http.StatusForbidden, invokeLogPolicyHandler(GetChannelDefaultBaseURLs, 0, common.RoleRootUser, "").Code)
	require.Equal(t, http.StatusForbidden, invokeLogPolicyHandler(GetChannelDefaultBaseURLs, 27, common.RoleCommonUser, "").Code)
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		response := invokeLogPolicyHandler(GetChannelDefaultBaseURLs, 27, role, "")
		var result struct {
			Success bool           `json:"success"`
			Data    map[int]string `json:"data"`
		}
		require.Equal(t, http.StatusOK, response.Code)
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		count := 0
		for channelType, baseURL := range constant.ChannelBaseURLs {
			if baseURL != "" {
				count++
				require.Equal(t, baseURL, result.Data[channelType])
			} else {
				require.NotContains(t, result.Data, channelType)
			}
		}
		require.Len(t, result.Data, count)
	}
}
