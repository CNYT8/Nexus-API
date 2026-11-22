package relay

import (
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// 失败响应仍可能携带实际用量。结算屏障与禁止重试必须一起设置，
// 防止外层退款或再次向另一个渠道发起相同请求。
func settlePartialResponseUsage(c *gin.Context, info *relaycommon.RelayInfo, rawUsage any, responseErr *types.NewAPIError) {
	if responseErr == nil {
		return
	}
	usage, _ := rawUsage.(*dto.Usage)
	if hasObservedResponseUsage(usage) {
		types.ErrOptionWithSkipRetry()(responseErr)
		hasAudio := usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
		hasAudioPrice := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
		if hasAudio && hasAudioPrice {
			service.PostAudioConsumeQuota(c, info, usage, "")
		} else {
			service.PostTextConsumeQuota(c, info, usage, nil)
		}
	}
	// 已输出的流不能重试拼接第二个渠道的响应，即使用量帧尚未到达。
	if c.Writer != nil && c.Writer.Written() {
		types.ErrOptionWithSkipRetry()(responseErr)
	}
}

func hasObservedResponseUsage(usage *dto.Usage) bool {
	if usage == nil {
		return false
	}
	if usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 ||
		usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.PromptCacheHitTokens > 0 ||
		usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0 {
		return true
	}
	for _, count := range usage.ToolUsage {
		if count > 0 {
			return true
		}
	}
	if hasObservedInputUsage(usage.PromptTokensDetails) ||
		(usage.InputTokensDetails != nil && hasObservedInputUsage(*usage.InputTokensDetails)) {
		return true
	}
	return hasObservedOutputUsage(usage.CompletionTokenDetails) ||
		(usage.OutputTokensDetails != nil && hasObservedOutputUsage(*usage.OutputTokensDetails))
}

func hasObservedInputUsage(details dto.InputTokenDetails) bool {
	return details.CachedTokens > 0 || details.CacheCreationTokensTotal() > 0 ||
		details.TextTokens > 0 || details.AudioTokens > 0 || details.ImageTokens > 0 ||
		(details.CacheWriteTokensDetails != nil &&
			(details.CacheWriteTokensDetails.TTL30mTokens > 0 || details.CacheWriteTokensDetails.TTL12hTokens > 0))
}

func hasObservedOutputUsage(details dto.OutputTokenDetails) bool {
	return details.TextTokens > 0 || details.AudioTokens > 0 || details.ImageTokens > 0 || details.ReasoningTokens > 0
}
