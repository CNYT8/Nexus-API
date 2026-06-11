package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
		newMsg := substituteErrorMaskPlaceholders(rule.Replacement, c, origMsg,
			fmt.Sprintf("%v", oai.Code), oai.Type, oai.Param, apiErr.StatusCode)
		apiErr.SetMessage(newMsg)
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
	s := error_mask_setting.GetSetting()
	if !s.Enabled || len(s.Rules) == 0 {
		return statusCode, message
	}
	msgLower := strings.ToLower(message)
	for _, rule := range s.Rules {
		if rule.Pattern != "" && !strings.Contains(msgLower, strings.ToLower(rule.Pattern)) {
			continue
		}
		newMsg := substituteErrorMaskPlaceholders(rule.Replacement, c, message, errCode, errType, "", statusCode)
		if rule.Status >= 100 && rule.Status <= 599 {
			statusCode = rule.Status
		}
		return statusCode, newMsg
	}
	return statusCode, message
}

func substituteErrorMaskPlaceholders(tpl string, c *gin.Context, origMsg, errCode, errType, errParam string, status int) string {
	var (
		channelId   string
		channelName string
		model       string
		requestId   string
	)
	if c != nil {
		if v, ok := common.GetContextKey(c, constant.ContextKeyChannelId); ok {
			if id, ok2 := v.(int); ok2 {
				channelId = strconv.Itoa(id)
			} else {
				channelId = fmt.Sprintf("%v", v)
			}
		}
		channelName = common.GetContextKeyString(c, constant.ContextKeyChannelName)
		model = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		requestId = c.GetString(common.RequestIdKey)
	}
	repl := strings.NewReplacer(
		"{message}", origMsg,
		"{code}", errCode,
		"{type}", errType,
		"{param}", errParam,
		"{status}", strconv.Itoa(status),
		"{channel_id}", channelId,
		"{channel_name}", channelName,
		"{model}", model,
		"{request_id}", requestId,
	)
	return repl.Replace(tpl)
}
