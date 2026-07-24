package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
)

// normalizeChannelSystemPromptUsage returns a settlement-only copy of usage
// with the channel-owned system prompt removed from input totals. The request
// sent to the upstream provider and the original usage object are unchanged.
//
// Providers do not report which input segment belongs to a channel prompt, so
// the removal is bounded by both the local prompt estimate and the upstream
// total. This keeps user-provided system messages billable and prevents a
// tokenizer mismatch from producing negative values.
func normalizeChannelSystemPromptUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) *dto.Usage {
	if usage == nil || relayInfo == nil || !relayInfo.HasChannelSystemPromptApplied() {
		return usage
	}

	systemPromptTokens := countChannelSystemPromptTokens(relayInfo.OriginModelName, relayInfo.ChannelSystemPrompt)
	if systemPromptTokens <= 0 {
		return usage
	}

	removable := systemPromptTokens
	originalPromptTokens := relayInfo.GetEstimatePromptTokens()
	upstreamPromptTokens := usage.PromptTokens
	if upstreamPromptTokens <= 0 {
		upstreamPromptTokens = usage.InputTokens
	}
	if originalPromptTokens > 0 {
		additionalPromptTokens := upstreamPromptTokens - originalPromptTokens
		if additionalPromptTokens <= 0 {
			return usage
		}
		if removable > additionalPromptTokens {
			removable = additionalPromptTokens
		}
	}
	if removable <= 0 {
		return usage
	}
	if textTokens := channelTextTokenLimit(usage); textTokens > 0 && removable > textTokens {
		removable = textTokens
	}
	if !isClaudeUsageSemanticForPrompt(relayInfo, usage) && usage.PromptTokens > 0 {
		preservedTokens := usage.PromptTokensDetails.CachedTokens
		if usage.PromptCacheHitTokens > preservedTokens {
			preservedTokens = usage.PromptCacheHitTokens
		}
		preservedTokens = addTokensUpTo(preservedTokens, usage.PromptTokensDetails.CacheCreationTokensTotal(), usage.PromptTokens)
		preservedTokens = addTokensUpTo(preservedTokens, usage.PromptTokensDetails.ImageTokens, usage.PromptTokens)
		preservedTokens = addTokensUpTo(preservedTokens, usage.PromptTokensDetails.AudioTokens, usage.PromptTokens)
		availableTextTokens := usage.PromptTokens - preservedTokens
		if availableTextTokens < removable {
			removable = maxNonNegative(availableTextTokens)
		}
	}
	if removable <= 0 {
		return usage
	}

	adjusted := *usage
	adjusted.PromptTokens = subtractTokens(adjusted.PromptTokens, removable)
	adjusted.InputTokens = subtractTokens(adjusted.InputTokens, removable)
	adjusted.TotalTokens = subtractTokens(adjusted.TotalTokens, removable)

	// Keep the value type and optional pointer independent from the upstream
	// object. Only explicit text-token fields are adjusted; cache/media fields
	// are provider-level aggregates and cannot be attributed safely.
	adjusted.PromptTokensDetails.TextTokens = subtractTokens(adjusted.PromptTokensDetails.TextTokens, removable)
	if usage.InputTokensDetails != nil {
		inputDetails := *usage.InputTokensDetails
		inputDetails.TextTokens = subtractTokens(inputDetails.TextTokens, removable)
		adjusted.InputTokensDetails = &inputDetails
	}

	return &adjusted
}

func channelTextTokenLimit(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	textTokens := usage.PromptTokensDetails.TextTokens
	if usage.InputTokensDetails != nil && usage.InputTokensDetails.TextTokens > textTokens {
		textTokens = usage.InputTokensDetails.TextTokens
	}
	return maxNonNegative(textTokens)
}

func isClaudeUsageSemanticForPrompt(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if usage != nil && usage.UsageSemantic == "anthropic" {
		return true
	}
	return relayInfo != nil && relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude
}

func maxNonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func addTokensUpTo(current, additional, limit int) int {
	current = maxNonNegative(current)
	additional = maxNonNegative(additional)
	limit = maxNonNegative(limit)
	if current >= limit || additional >= limit-current {
		return limit
	}
	return current + additional
}

func countChannelSystemPromptTokens(model, prompt string) int {
	if prompt == "" {
		return 0
	}
	if common.IsOpenAITextModel(model) && defaultTokenEncoder != nil {
		encoder := getTokenEncoder(model)
		if encoder != nil {
			return getTokenNum(encoder, prompt)
		}
	}
	return EstimateTokenByModel(model, prompt)
}

func subtractTokens(value, amount int) int {
	if value <= 0 {
		return 0
	}
	if amount <= 0 {
		return value
	}
	if amount >= value {
		return 0
	}
	return value - amount
}
