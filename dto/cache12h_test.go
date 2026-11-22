package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCache12hInputTokenDetails(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantTotal int
		want12h   int
	}{
		{"write alias", `{"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}`, 7000, 4000},
		{"cached creation alias", `{"cached_creation_tokens":7000,"cache_write_tokens_details":{"ttl_12h_tokens":4000}}`, 7000, 4000},
		{"creation input alias", `{"cache_creation_input_tokens":7000,"cache_write_tokens_details":{"ttl_12h_tokens":4000}}`, 7000, 4000},
		{"aliases use max not sum", `{"cache_write_tokens":5000,"cached_creation_tokens":6000,"cache_creation_input_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}`, 7000, 4000},
		{"missing ttl falls back", `{"cache_write_tokens":7000}`, 7000, 0},
		{"null ttl falls back", `{"cache_write_tokens":7000,"cache_write_tokens_details":null}`, 7000, 0},
		{"negative ttl clamps", `{"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_12h_tokens":-1}}`, 7000, 0},
		{"large ttl clamps", `{"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_12h_tokens":9000}}`, 7000, 7000},
		{"negative total clamps", `{"cache_write_tokens":-1,"cached_creation_tokens":-2,"cache_creation_input_tokens":-3,"cache_write_tokens_details":{"ttl_12h_tokens":4000}}`, 0, 0},
		{"ttl is not an extra total", `{"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}`, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var details InputTokenDetails
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &details))
			require.Equal(t, tc.wantTotal, details.CacheCreationTokensTotal())
			require.Equal(t, tc.want12h, details.CacheWrite12hTokens())
		})
	}
}

func TestCache12hCloneAndMerge(t *testing.T) {
	src := InputTokenDetails{
		CachedTokens:             4000,
		CachedCreationTokens:     6000,
		CacheWriteTokens:         5000,
		CacheCreationInputTokens: 7000,
		CacheWriteTokensDetails:  &CacheWriteTokenDetails{TTL30mTokens: 3000, TTL12hTokens: 4000},
		TextTokens:               1200,
		AudioTokens:              200,
		ImageTokens:              100,
	}
	clone := src.Clone()
	require.Equal(t, src, clone)
	require.NotSame(t, src.CacheWriteTokensDetails, clone.CacheWriteTokensDetails)
	clone.CacheWriteTokensDetails.TTL12hTokens = 1
	require.Equal(t, 4000, src.CacheWrite12hTokens())
	require.Nil(t, (InputTokenDetails{}).Clone().CacheWriteTokensDetails)

	var merged InputTokenDetails
	merged.MergeNonZero(src)
	merged.MergeNonZero(src)
	require.Equal(t, src, merged, "重复快照不能累加")
	require.NotSame(t, src.CacheWriteTokensDetails, merged.CacheWriteTokensDetails)
	src.CacheWriteTokensDetails.TTL12hTokens = 99
	require.Equal(t, 4000, merged.CacheWrite12hTokens())

	before := merged.Clone()
	merged.MergeNonZero(InputTokenDetails{CacheWriteTokensDetails: &CacheWriteTokenDetails{}})
	merged.MergeNonZero(InputTokenDetails{
		CachedTokens: -1, CachedCreationTokens: -1, CacheWriteTokens: -1, CacheCreationInputTokens: -1,
		TextTokens: -1, AudioTokens: -1, ImageTokens: -1,
		CacheWriteTokensDetails: &CacheWriteTokenDetails{TTL30mTokens: -1, TTL12hTokens: -1},
	})
	require.Equal(t, before, merged, "零值和负值不能抹掉已知用量")

	partial := InputTokenDetails{CacheWriteTokensDetails: &CacheWriteTokenDetails{TTL12hTokens: 2000}}
	merged.MergeNonZero(partial)
	require.Equal(t, 3000, merged.CacheWriteTokensDetails.TTL30mTokens)
	require.Equal(t, 2000, merged.CacheWrite12hTokens(), "后续正数覆盖而不是取最大值")
	partial.CacheWriteTokensDetails.TTL12hTokens = 1
	require.Equal(t, 2000, merged.CacheWrite12hTokens())

	var nilDetails *InputTokenDetails
	nilDetails.MergeNonZero(src)
}

func TestCache12hPromptCacheOptionsRoundTrip(t *testing.T) {
	const options = `{"ttl":"12h","future_option":true}`
	const raw = `{"model":"gpt-test","prompt_cache_options":` + options + `}`
	tests := []struct {
		name    string
		request any
	}{
		{"chat", &GeneralOpenAIRequest{}},
		{"responses", &OpenAIResponsesRequest{}},
		{"compact", &OpenAIResponsesCompactionRequest{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			empty, err := json.Marshal(tc.request)
			require.NoError(t, err)
			require.NotContains(t, string(empty), "prompt_cache_options")
			require.NoError(t, json.Unmarshal([]byte(raw), tc.request))
			encoded, err := json.Marshal(tc.request)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			require.JSONEq(t, options, string(fields["prompt_cache_options"]))
		})
	}
}
