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

func TestApplyErrorMaskHigherWeightWins(t *testing.T) {
	withErrorMaskConfig(t, true, `[
		{"pattern":"upstream","replacement":"low priority mask","status":400,"weight":1},
		{"pattern":"quota exhausted","replacement":"high priority mask","status":503,"weight":10}
	]`)

	apiErr := types.NewOpenAIError(errors.New("upstream quota exhausted"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)

	ApplyErrorMask(nil, apiErr)

	require.Equal(t, "high priority mask", apiErr.Error())
	require.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)

	statusCode, message := ApplyErrorMaskToMessage(
		nil,
		http.StatusTooManyRequests,
		"upstream quota exhausted",
		"rate_limit_exceeded",
		"rate_limit_error",
	)
	require.Equal(t, http.StatusServiceUnavailable, statusCode)
	require.Equal(t, "high priority mask", message)
}

func TestApplyErrorMaskEqualWeightKeepsConfiguredOrder(t *testing.T) {
	withErrorMaskConfig(t, true, `[
		{"pattern":"upstream","replacement":"first mask","status":0,"weight":7},
		{"pattern":"quota exhausted","replacement":"second mask","status":0,"weight":7}
	]`)

	apiErr := types.NewOpenAIError(errors.New("upstream quota exhausted"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)

	ApplyErrorMask(nil, apiErr)

	require.Equal(t, "first mask", apiErr.Error())
}

func TestApplyErrorMaskUpstreamOnlySkipsLocalSiteErrors(t *testing.T) {
	withErrorMaskConfig(t, true, `[
		{"pattern":"额度不足","replacement":"当前模型暂不可用，请稍后再试","status":503,"weight":10,"upstream_only":true}
	]`)

	// Local user quota error initiated by this site must NOT be masked when upstream_only is true.
	localQuotaErr := types.NewErrorWithStatusCode(
		errors.New("用户额度不足, 剩余额度: $0.00"),
		types.ErrorCodeInsufficientUserQuota,
		http.StatusForbidden,
	)
	ApplyErrorMask(nil, localQuotaErr)
	require.Equal(t, "用户额度不足, 剩余额度: $0.00", localQuotaErr.Error())
	require.Equal(t, http.StatusForbidden, localQuotaErr.StatusCode)

	// Local middleware abort message must NOT be masked when upstream_only is true.
	statusCode, msg := ApplyErrorMaskToMessage(nil, http.StatusForbidden, "用户额度不足", string(types.ErrorCodeInsufficientUserQuota), "new_api_error")
	require.Equal(t, http.StatusForbidden, statusCode)
	require.Equal(t, "用户额度不足", msg)

	// Upstream channel quota error with identical wording MUST be masked when upstream_only is true.
	upstreamQuotaErr := types.WithOpenAIError(types.OpenAIError{
		Message: "用户额度不足, 剩余额度: $0.00",
		Type:    "new_api_error",
		Code:    "insufficient_user_quota",
	}, http.StatusForbidden)
	ApplyErrorMask(nil, upstreamQuotaErr)
	require.Equal(t, "当前模型暂不可用，请稍后再试", upstreamQuotaErr.Error())
	require.Equal(t, http.StatusServiceUnavailable, upstreamQuotaErr.StatusCode)

	// User error logs: local quota error on a selected channel must NOT be masked when upstream_only is true.
	localQuotaLog := &model.Log{
		Type:      model.LogTypeError,
		ChannelId: 12,
		Content:   "用户额度不足, 剩余额度: $0.00",
		Other:     `{"error_code":"insufficient_user_quota","error_type":"new_api_error","status_code":403}`,
	}
	upstreamQuotaLog := &model.Log{
		Type:      model.LogTypeError,
		ChannelId: 12,
		Content:   "用户额度不足, 剩余额度: $0.00",
		Other:     `{"error_code":"insufficient_user_quota","error_type":"openai_error","status_code":403}`,
	}
	ApplyErrorMaskToUserLogs([]*model.Log{localQuotaLog, upstreamQuotaLog})
	require.Equal(t, "用户额度不足, 剩余额度: $0.00", localQuotaLog.Content)
	require.Equal(t, "当前模型暂不可用，请稍后再试", upstreamQuotaLog.Content)
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
