package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesCompactionJSONPreservesExtendedFields(t *testing.T) {
	const body = `{
		"model":"gpt-5.4",
		"input":[{"role":"user","content":"压缩已有上下文"}],
		"tools":[{"type":"function","name":"lookup","strict":false,"parameters":{"type":"object","properties":{"key":{"type":"string"}}}}],
		"reasoning":{"effort":"low","summary":"auto"},
		"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}},
		"parallel_tool_calls":false,
		"service_tier":"priority",
		"prompt_cache_retention":"24h",
		"max_output_tokens":0
	}`
	var request OpenAIResponsesCompactionRequest
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	require.NotNil(t, request.ParallelToolCalls)
	require.False(t, *request.ParallelToolCalls)
	require.NotNil(t, request.Reasoning)
	require.Equal(t, "low", request.Reasoning.Effort)
	require.Equal(t, "auto", request.Reasoning.Summary)
	require.Equal(t, "priority", request.ServiceTier)

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	var before, after map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &before))
	require.NoError(t, json.Unmarshal(encoded, &after))
	for _, field := range []string{
		"tools", "reasoning", "text", "parallel_tool_calls",
		"service_tier", "prompt_cache_retention", "max_output_tokens",
	} {
		t.Run(field, func(t *testing.T) {
			require.Contains(t, after, field, "JSON 解码为 compact DTO 后不得丢失字段")
			require.JSONEq(t, string(before[field]), string(after[field]))
		})
	}
}

func TestResponsesCompactionJSONMaxOutputTokensPresence(t *testing.T) {
	for _, tc := range []struct {
		name, body, expected string
		present              bool
	}{
		{name: "缺省", body: `{"model":"gpt-5.4","input":"hello"}`},
		{name: "显式零", body: `{"model":"gpt-5.4","input":"hello","max_output_tokens":0}`, expected: "0", present: true},
		{name: "正数", body: `{"model":"gpt-5.4","input":"hello","max_output_tokens":64}`, expected: "64", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request OpenAIResponsesCompactionRequest
			require.NoError(t, json.Unmarshal([]byte(tc.body), &request))
			encoded, err := json.Marshal(request)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			value, present := fields["max_output_tokens"]
			require.Equal(t, tc.present, present, "显式零与缺省必须区分")
			if tc.present {
				require.JSONEq(t, tc.expected, string(value))
			}
		})
	}
}
