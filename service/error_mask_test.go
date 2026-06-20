package service

import (
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestApplyErrorMaskToUserLogsMasksOnlyMatchingErrorLogs(t *testing.T) {
	withErrorMaskConfig(t, true, `[{"pattern":"upstream quota exhausted","replacement":"masked upstream error {status} {code} {type}","status":0}]`)

	logs := []*model.Log{
		{
			Type:    model.LogTypeError,
			Content: "status_code=429, upstream quota exhausted for channel",
			Other:   `{"status_code":429,"error_code":"bad_response_status_code","error_type":"openai_error"}`,
		},
		{
			Type:    model.LogTypeError,
			Content: "status_code=500, upstream timeout",
		},
		{
			Type:    model.LogTypeConsume,
			Content: "status_code=429, upstream quota exhausted for channel",
		},
	}

	ApplyErrorMaskToUserLogs(logs)

	require.Equal(t, "masked upstream error 429 bad_response_status_code openai_error", logs[0].Content)
	require.Equal(t, "status_code=500, upstream timeout", logs[1].Content)
	require.Equal(t, "status_code=429, upstream quota exhausted for channel", logs[2].Content)
}

func withErrorMaskConfig(t *testing.T, enabled bool, rules string) {
	t.Helper()

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "error_mask_setting.") {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"error_mask_setting.enabled": strconv.FormatBool(enabled),
		"error_mask_setting.rules":   rules,
	}))
}
