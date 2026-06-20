package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/error_mask_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// ApplyErrorMask mutates *types.NewAPIError in place if any configured rule matches.
// First-match-wins. Safe to call with nil error or nil context. No-op when feature disabled.
func ApplyErrorMask(c *gin.Context, apiErr *types.NewAPIError) {
	if apiErr == nil {
		return
	}
	s := error_mask_setting.GetSetting()
	if !s.Enabled || len(s.Rules) == 0 {
		return
	}
	origMsg := apiErr.Error()
	origMsgLower := strings.ToLower(origMsg)
	for _, rule := range s.Rules {
		if rule.Pattern != "" && !strings.Contains(origMsgLower, strings.ToLower(rule.Pattern)) {
			continue
		}
		oai := apiErr.ToOpenAIError()
		newMsg := substituteErrorMaskPlaceholders(rule.Replacement, errorMaskPlaceholdersFromContext(c), origMsg,
			fmt.Sprintf("%v", oai.Code), oai.Type, oai.Param, apiErr.StatusCode)
		apiErr.SetMessage(newMsg)
		clearMaskedRelayMetadata(apiErr)
		if rule.Status >= 100 && rule.Status <= 599 {
			apiErr.StatusCode = rule.Status
		}
		return
	}
}

// ApplyErrorMaskToMessage applies the configured masking rules to a plain
// status/message pair. Used by middleware-stage aborts (distributor, auth)
// that write the error response before the relay controller is reached.
// Returns the possibly-rewritten pair; unchanged when disabled or no rule hits.
func ApplyErrorMaskToMessage(c *gin.Context, statusCode int, message string, errCode string, errType string) (int, string) {
	return applyErrorMaskToMessage(statusCode, message, errCode, errType, errorMaskPlaceholdersFromContext(c))
}

func ApplyErrorMaskToUserLogs(logs []*model.Log) {
	for _, log := range logs {
		if log != nil && log.Type == model.LogTypeError {
			statusCode, errCode, errType := errorMaskInfoFromLog(log)
			_, log.Content = applyErrorMaskToMessage(
				statusCode,
				log.Content,
				errCode,
				errType,
				errorMaskPlaceholdersFromLog(log),
			)
		}
	}
}

func applyErrorMaskToMessage(statusCode int, message string, errCode string, errType string, placeholders errorMaskPlaceholders) (int, string) {
	s := error_mask_setting.GetSetting()
	if !s.Enabled || len(s.Rules) == 0 {
		return statusCode, message
	}
	msgLower := strings.ToLower(message)
	for _, rule := range s.Rules {
		if rule.Pattern != "" && !strings.Contains(msgLower, strings.ToLower(rule.Pattern)) {
			continue
		}
		newMsg := substituteErrorMaskPlaceholders(rule.Replacement, placeholders, message, errCode, errType, "", statusCode)
		if rule.Status >= 100 && rule.Status <= 599 {
			statusCode = rule.Status
		}
		return statusCode, newMsg
	}
	return statusCode, message
}

func clearMaskedRelayMetadata(apiErr *types.NewAPIError) {
	if apiErr == nil {
		return
	}
	apiErr.Metadata = nil
	switch relayError := apiErr.RelayError.(type) {
	case types.OpenAIError:
		relayError.Metadata = nil
		apiErr.RelayError = relayError
	}
}

func errorMaskInfoFromLog(log *model.Log) (int, string, string) {
	if log == nil {
		return 0, "", "new_api_error"
	}
	statusCode := 0
	errCode := ""
	errType := "new_api_error"
	other, _ := common.StrToMap(log.Other)
	if v, ok := other["status_code"]; ok {
		statusCode = intFromInterface(v)
	}
	if v, ok := other["error_code"]; ok {
		errCode = fmt.Sprintf("%v", v)
	}
	if v, ok := other["error_type"]; ok {
		errType = fmt.Sprintf("%v", v)
	}
	return statusCode, errCode, errType
}

func intFromInterface(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		i, _ := strconv.Atoi(fmt.Sprintf("%v", v))
		return i
	}
}

type errorMaskPlaceholders struct {
	channelId   string
	channelName string
	model       string
	requestId   string
}

func errorMaskPlaceholdersFromContext(c *gin.Context) errorMaskPlaceholders {
	var placeholders errorMaskPlaceholders
	if c != nil {
		if v, ok := common.GetContextKey(c, constant.ContextKeyChannelId); ok {
			if id, ok2 := v.(int); ok2 {
				placeholders.channelId = strconv.Itoa(id)
			} else {
				placeholders.channelId = fmt.Sprintf("%v", v)
			}
		}
		placeholders.channelName = common.GetContextKeyString(c, constant.ContextKeyChannelName)
		placeholders.model = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		placeholders.requestId = c.GetString(common.RequestIdKey)
	}
	return placeholders
}

func errorMaskPlaceholdersFromLog(log *model.Log) errorMaskPlaceholders {
	var placeholders errorMaskPlaceholders
	if log == nil {
		return placeholders
	}
	placeholders.model = log.ModelName
	placeholders.requestId = log.RequestId
	if log.ChannelId != 0 {
		placeholders.channelId = strconv.Itoa(log.ChannelId)
	}
	other, _ := common.StrToMap(log.Other)
	if v, ok := other["channel_id"]; ok && placeholders.channelId == "" {
		placeholders.channelId = fmt.Sprintf("%v", v)
	}
	if v, ok := other["channel_name"]; ok {
		placeholders.channelName = fmt.Sprintf("%v", v)
	}
	return placeholders
}

func substituteErrorMaskPlaceholders(tpl string, placeholders errorMaskPlaceholders, origMsg, errCode, errType, errParam string, status int) string {
	repl := strings.NewReplacer(
		"{message}", origMsg,
		"{code}", errCode,
		"{type}", errType,
		"{param}", errParam,
		"{status}", strconv.Itoa(status),
		"{channel_id}", placeholders.channelId,
		"{channel_name}", placeholders.channelName,
		"{model}", placeholders.model,
		"{request_id}", placeholders.requestId,
	)
	return repl.Replace(tpl)
}
