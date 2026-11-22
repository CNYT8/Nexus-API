package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestCache12hChatRequestOptions(t *testing.T) {
	for _, options := range []json.RawMessage{nil, json.RawMessage(`{"ttl":"12h","future_option":true}`)} {
		req := &dto.GeneralOpenAIRequest{
			Model:              "gpt-test",
			Messages:           []dto.Message{{Role: "user", Content: "hello"}},
			PromptCacheOptions: options,
		}
		converted, err := ChatCompletionsRequestToResponsesRequest(req)
		require.NoError(t, err)
		require.Equal(t, options, converted.PromptCacheOptions)
		encoded, err := json.Marshal(converted)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &fields))
		if options == nil {
			require.NotContains(t, fields, "prompt_cache_options")
		} else {
			require.JSONEq(t, string(options), string(fields["prompt_cache_options"]))
		}
	}
}

func TestCache12hResponsesUsageClone(t *testing.T) {
	for _, source := range []string{"input_tokens_details", "prompt_tokens_details"} {
		t.Run(source, func(t *testing.T) {
			details := dto.InputTokenDetails{
				CachedTokens:             4000,
				CachedCreationTokens:     6000,
				CacheWriteTokens:         5000,
				CacheCreationInputTokens: 7000,
				CacheWriteTokensDetails:  &dto.CacheWriteTokenDetails{TTL30mTokens: 3000, TTL12hTokens: 4000},
				TextTokens:               1200,
				ImageTokens:              100,
				AudioTokens:              200,
			}
			upstream := &dto.OpenAIResponsesResponse{
				Model: "gpt-test",
				Usage: &dto.Usage{InputTokens: 12000, OutputTokens: 500, TotalTokens: 12500},
			}
			if source == "input_tokens_details" {
				upstream.Usage.InputTokensDetails = &details
			} else {
				upstream.Usage.PromptTokensDetails = details
			}
			chat, usage, err := ResponsesResponseToChatCompletionsResponse(upstream, "chat-test")
			require.NoError(t, err)
			require.Equal(t, 12000, usage.PromptTokens)
			require.Equal(t, 500, usage.CompletionTokens)
			require.Equal(t, 12500, usage.TotalTokens)
			require.Equal(t, details, usage.PromptTokensDetails)
			require.Equal(t, 7000, usage.PromptTokensDetails.CacheCreationTokensTotal())
			require.Equal(t, 4000, chat.Usage.PromptTokensDetails.CacheWrite12hTokens())
			require.NotSame(t, details.CacheWriteTokensDetails, usage.PromptTokensDetails.CacheWriteTokensDetails)
			usage.PromptTokensDetails.CacheWriteTokensDetails.TTL12hTokens = 1
			require.Equal(t, 4000, details.CacheWrite12hTokens())
		})
	}
}
