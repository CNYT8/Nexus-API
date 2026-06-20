package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/types"
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

func TestApplyErrorMaskUpdatesOpenAIRelayErrorMessage(t *testing.T) {
	withErrorMaskConfig(t, true, `[{"pattern":"upstream quota exhausted","replacement":"masked upstream error","status":0}]`)

	apiErr := types.NewOpenAIError(errors.New("upstream quota exhausted for channel"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)

	ApplyErrorMask(nil, apiErr)

	require.Equal(t, "masked upstream error", apiErr.Error())
	require.Equal(t, "masked upstream error", apiErr.ToOpenAIError().Message)
}

func TestApplyErrorMaskClearsOpenAIRelayErrorMetadata(t *testing.T) {
	withErrorMaskConfig(t, true, `[{"pattern":"upstream quota exhausted","replacement":"masked upstream error","status":0}]`)

	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message:  "upstream quota exhausted for channel",
		Type:     "rate_limit_error",
		Code:     "rate_limit_exceeded",
		Metadata: json.RawMessage(`{"raw":"upstream quota exhausted for channel"}`),
	}, http.StatusTooManyRequests)

	ApplyErrorMask(nil, apiErr)
	openaiErr := apiErr.ToOpenAIError()

	require.Equal(t, "masked upstream error", openaiErr.Message)
	require.Empty(t, openaiErr.Metadata)
}

func TestApplyErrorMaskUpdatesClaudeRelayErrorMessage(t *testing.T) {
	withErrorMaskConfig(t, true, `[{"pattern":"claude upstream overload","replacement":"masked claude error","status":0}]`)

	apiErr := types.WithClaudeError(types.ClaudeError{
		Message: "claude upstream overload",
		Type:    "overloaded_error",
	}, http.StatusServiceUnavailable)

	ApplyErrorMask(nil, apiErr)

	require.Equal(t, "masked claude error", apiErr.Error())
	require.Equal(t, "masked claude error", apiErr.ToClaudeError().Message)
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
