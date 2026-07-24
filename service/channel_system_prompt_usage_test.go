package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

func TestNormalizeChannelSystemPromptUsageRemovesOnlyAddedPromptTokens(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4.1",
	}
	relayInfo.SetEstimatePromptTokens(100)
	relayInfo.MarkChannelSystemPromptApplied("Use concise answers for this channel.")

	usage := &dto.Usage{
		PromptTokens:     100 + countChannelSystemPromptTokens(relayInfo.OriginModelName, relayInfo.ChannelSystemPrompt),
		CompletionTokens: 25,
		TotalTokens:      125 + countChannelSystemPromptTokens(relayInfo.OriginModelName, relayInfo.ChannelSystemPrompt),
		InputTokens:      100 + countChannelSystemPromptTokens(relayInfo.OriginModelName, relayInfo.ChannelSystemPrompt),
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens: 100 + countChannelSystemPromptTokens(relayInfo.OriginModelName, relayInfo.ChannelSystemPrompt),
		},
	}
	original := *usage

	adjusted := normalizeChannelSystemPromptUsage(relayInfo, usage)

	require.Equal(t, 100, adjusted.PromptTokens)
	require.Equal(t, 25, adjusted.CompletionTokens)
	require.Equal(t, 125, adjusted.TotalTokens)
	require.Equal(t, 100, adjusted.InputTokens)
	require.Equal(t, 100, adjusted.PromptTokensDetails.TextTokens)
	require.Equal(t, original, *usage)
	require.NotSame(t, usage, adjusted)
}

func TestNormalizeChannelSystemPromptUsageDoesNotRemoveUserSystemPrompt(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 20, TotalTokens: 20}
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4.1",
	}

	adjusted := normalizeChannelSystemPromptUsage(relayInfo, usage)

	require.Same(t, usage, adjusted)
}

func TestNormalizeChannelSystemPromptUsageDoesNotOverDeduct(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4.1",
	}
	relayInfo.SetEstimatePromptTokens(100)
	relayInfo.MarkChannelSystemPromptApplied("A channel prompt")
	usage := &dto.Usage{PromptTokens: 90, TotalTokens: 90}

	adjusted := normalizeChannelSystemPromptUsage(relayInfo, usage)

	require.Same(t, usage, adjusted)
}

func TestNormalizeChannelSystemPromptUsageSaturatesAndClonesDetails(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{OriginModelName: "gpt-4.1"}
	relayInfo.MarkChannelSystemPromptApplied("A very long channel system prompt that exceeds the reported input")
	details := &dto.InputTokenDetails{TextTokens: 3, CachedTokens: 2}
	usage := &dto.Usage{
		PromptTokens:         3,
		TotalTokens:          3,
		InputTokens:          3,
		PromptCacheHitTokens: 2,
		InputTokensDetails:   details,
	}

	adjusted := normalizeChannelSystemPromptUsage(relayInfo, usage)

	require.Equal(t, 2, adjusted.PromptTokens)
	require.Equal(t, 2, adjusted.TotalTokens)
	require.Equal(t, 2, adjusted.InputTokens)
	require.Equal(t, 2, adjusted.InputTokensDetails.TextTokens)
	require.Equal(t, 2, adjusted.InputTokensDetails.CachedTokens)
	require.Same(t, details, usage.InputTokensDetails)
	require.NotSame(t, details, adjusted.InputTokensDetails)
}

func TestNormalizeChannelSystemPromptUsageKeepsProviderFormatData(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{OriginModelName: "claude-3-7-sonnet", FinalRequestRelayFormat: types.RelayFormatClaude}
	relayInfo.MarkChannelSystemPromptApplied("Answer in Chinese")
	usage := &dto.Usage{
		PromptTokens: 50,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         10,
			CachedCreationTokens: 5,
			AudioTokens:          2,
		},
	}

	adjusted := normalizeChannelSystemPromptUsage(relayInfo, usage)

	require.Equal(t, usage.PromptTokensDetails.CachedTokens, adjusted.PromptTokensDetails.CachedTokens)
	require.Equal(t, usage.PromptTokensDetails.CachedCreationTokens, adjusted.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, usage.PromptTokensDetails.AudioTokens, adjusted.PromptTokensDetails.AudioTokens)
}

func TestNormalizeChannelSystemPromptUsagePreservesNonTextInputFloor(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{OriginModelName: "gpt-4.1"}
	relayInfo.MarkChannelSystemPromptApplied("A long channel prompt that can exceed the available text tokens")
	usage := &dto.Usage{
		PromptTokens: 20,
		TotalTokens:  20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         10,
			CachedCreationTokens: 2,
			ImageTokens:          5,
			AudioTokens:          3,
		},
	}

	adjusted := normalizeChannelSystemPromptUsage(relayInfo, usage)

	require.Same(t, usage, adjusted)
	require.Equal(t, 20, adjusted.PromptTokens)
}
