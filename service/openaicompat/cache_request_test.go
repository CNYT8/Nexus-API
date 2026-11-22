package openaicompat

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestChatToResponsesPreservesPromptCacheFields(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fields    string
		key       string
		retention string
		options   string
	}{
		{name: "ordinary request"},
		{
			name:   "escaped key",
			fields: `,"prompt_cache_key":"cache-\"quoted\"\\path\n雪<>"`,
			key:    `"cache-\"quoted\"\\path\n雪<>"`,
		},
		{
			name:      "key and retention with existing cache options",
			fields:    `,"prompt_cache_key":"session-1","prompt_cache_retention":"24h","prompt_cache_options":{"ttl":"12h","future_option":{"enabled":false,"limit":0,"sequence":9007199254740993}}`,
			key:       `"session-1"`,
			retention: `"24h"`,
			options:   `{"ttl":"12h","future_option":{"enabled":false,"limit":0,"sequence":9007199254740993}}`,
		},
		{
			name:      "retention without key",
			fields:    `,"prompt_cache_retention":"in_memory"`,
			retention: `"in_memory"`,
		},
		{
			name:      "opaque retention extensions",
			fields:    `,"prompt_cache_key":"session-2","prompt_cache_retention":{"ttl":"future","enabled":false,"sequence":9007199254740993,"rules":[0,{"nested":true}]}`,
			key:       `"session-2"`,
			retention: `{"ttl":"future","enabled":false,"sequence":9007199254740993,"rules":[0,{"nested":true}]}`,
		},
		{
			name:      "explicit null raw fields",
			fields:    `,"prompt_cache_key":"session-3","prompt_cache_retention":null,"prompt_cache_options":null`,
			key:       `"session-3"`,
			retention: `null`,
			options:   `null`,
		},
		{
			name:      "empty key and zero retention",
			fields:    `,"prompt_cache_key":"","prompt_cache_retention":0`,
			retention: `0`,
		},
		{
			name:      "false retention remains present",
			fields:    `,"prompt_cache_retention":false`,
			retention: `false`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
					raw := fmt.Sprintf(`{
						"model":"gpt-test",
						"messages":[{"role":"system","content":"Be concise."},{"role":"developer","content":"Use plain text."},{"role":"user","content":"hello"}],
						"stream":%t,"temperature":0,"top_p":0.5,"max_tokens":64,"max_completion_tokens":96,
						"store":false,"metadata":{"source":"cache-test"}%s
					}`, stream, tc.fields)
					var incoming dto.GeneralOpenAIRequest
					require.NoError(t, common.Unmarshal([]byte(raw), &incoming))
					request, err := common.DeepCopy(&incoming)
					require.NoError(t, err)
					before, err := common.Marshal(request)
					require.NoError(t, err)
					converted, err := ChatCompletionsRequestToResponsesRequest(request)
					require.NoError(t, err)
					encoded, err := common.Marshal(converted)
					require.NoError(t, err)
					var out map[string]json.RawMessage
					require.NoError(t, common.Unmarshal(encoded, &out))

					require.JSONEq(t, `"gpt-test"`, string(out["model"]))
					require.JSONEq(t, `[{"role":"user","content":"hello"}]`, string(out["input"]))
					require.JSONEq(t, `"Be concise.\n\nUse plain text."`, string(out["instructions"]))
					require.JSONEq(t, fmt.Sprintf("%t", stream), string(out["stream"]))
					require.JSONEq(t, `0`, string(out["temperature"]))
					require.JSONEq(t, `0.5`, string(out["top_p"]))
					require.JSONEq(t, `96`, string(out["max_output_tokens"]))
					require.JSONEq(t, `false`, string(out["store"]))
					require.JSONEq(t, `{"source":"cache-test"}`, string(out["metadata"]))
					require.NotContains(t, out, "messages")
					for field, expected := range map[string]string{
						"prompt_cache_key":       tc.key,
						"prompt_cache_retention": tc.retention,
						"prompt_cache_options":   tc.options,
					} {
						if expected == "" {
							require.NotContains(t, out, field)
						} else {
							require.JSONEq(t, expected, string(out[field]), field)
						}
					}
					if tc.name == "opaque retention extensions" {
						require.Contains(t, string(out["prompt_cache_retention"]), "9007199254740993")
					}
					if tc.options != "" && tc.options != "null" {
						require.Contains(t, string(out["prompt_cache_options"]), "9007199254740993")
					}
					after, err := common.Marshal(request)
					require.NoError(t, err)
					require.Equal(t, before, after, "conversion must not mutate the chat request")
				})
			}
		})
	}
}
