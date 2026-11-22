package dto

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestProtocolSyncChatMessageToolsOmitOnlyAbsentContent(t *testing.T) {
	const input = `{"model":"gpt-5","thinking_budget":0,"messages":[
		{"role":"tool","tool_call_id":"load_1","tools":[{"type":"function","function":{"name":"lookup"}}]},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
		{"role":"tool","content":"","tools":[]},
		{"role":"user","content":[{"type":"text","text":"hello"}]},
		{"role":"tool","content":[],"tools":[]}
	]}`
	var request GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(input), &request))
	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.NotContains(t, payload, "thinking_budget")
	var messages []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload["messages"], &messages))
	require.Len(t, messages, 5)
	require.NotContains(t, messages[0], "content")
	require.JSONEq(t, `[{"type":"function","function":{"name":"lookup"}}]`, string(messages[0]["tools"]))
	require.JSONEq(t, `"load_1"`, string(messages[0]["tool_call_id"]))
	require.JSONEq(t, `null`, string(messages[1]["content"]))
	require.NotEmpty(t, messages[1]["tool_calls"])
	require.JSONEq(t, `""`, string(messages[2]["content"]))
	require.JSONEq(t, `[{"type":"text","text":"hello"}]`, string(messages[3]["content"]))
	require.JSONEq(t, `[]`, string(messages[4]["content"]))
	require.Nil(t, request.Messages[0].Content)
	require.Equal(t, json.RawMessage(`0`), request.ThinkingBudget, "marshal must not mutate the request")

	request.Model = "qwen-plus"
	encoded, err = common.Marshal(request)
	require.NoError(t, err)
	payload = nil
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.JSONEq(t, `0`, string(payload["thinking_budget"]))

	ordinary, err := common.Marshal(GeneralOpenAIRequest{Messages: []Message{{Role: "assistant"}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"messages":[{"role":"assistant","content":null}]}`, string(ordinary))
}

func TestProtocolSyncResponsesProviderExtensionsRoundTrip(t *testing.T) {
	const input = `{"model":"kimi-k3","input":"hello","max_output_tokens":0,"temperature":0,"top_p":0,
		"moderation":{"enabled":false},"frequency_penalty":0,"presence_penalty":0,
		"client_metadata":{"session":"abc"},"chat_template_kwargs":{"enable_thinking":false},
		"top_k":0,"min_p":0,"repetition_penalty":0,"stop":[],"cache_salt":"",
		"reasoning":{"effort":"high","summary":"auto","mode":"enabled","context":{"budget":0}},
		"prompt_cache_options":{"ttl":"12h"}}`
	var request OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(input), &request))
	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))

	const explicitNull = `{"model":"kimi-k3","moderation":null,"reasoning":{"mode":false,"context":0}}`
	request = OpenAIResponsesRequest{}
	require.NoError(t, common.Unmarshal([]byte(explicitNull), &request))
	encoded, err = common.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, explicitNull, string(encoded))

	encoded, err = common.Marshal(OpenAIResponsesRequest{Model: "kimi-k3"})
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"kimi-k3"}`, string(encoded))
}

func TestProtocolSyncGeminiStreamFunctionCallArgumentsPresence(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		t.Run("value="+value, func(t *testing.T) {
			config := `{"mode":"AUTO"}`
			if value != "" {
				config = `{"mode":"AUTO","streamFunctionCallArguments":` + value + `}`
			}
			input := `{"contents":[],"generationConfig":{},"toolConfig":{"functionCallingConfig":` + config + `}}`
			var request GeminiChatRequest
			require.NoError(t, common.Unmarshal([]byte(input), &request))
			require.NotNil(t, request.ToolConfig)
			require.NotNil(t, request.ToolConfig.FunctionCallingConfig)
			flag := request.ToolConfig.FunctionCallingConfig.StreamFunctionCallArguments
			if value == "" {
				require.Nil(t, flag)
			} else {
				require.NotNil(t, flag)
				require.Equal(t, value == "true", *flag)
			}
			encoded, err := common.Marshal(request)
			require.NoError(t, err)
			require.JSONEq(t, input, string(encoded))
		})
	}
}

func TestProtocolSyncClaudeMessageAndTemplateExtensionsRoundTrip(t *testing.T) {
	const input = `{"model":"claude-sonnet-4-6","messages":[
		{"role":"user","content":"hello","output_config":{"effort":"low","budget":0}},
		{"role":"assistant","content":"ready","output_config":false}],
		"output_config":{"effort":"high"},"chat_template_kwargs":{"enable_thinking":false,"limit":0},
		"thinking":{"type":"enabled","budget_tokens":1024,"block_binding":false}}`
	var request ClaudeRequest
	require.NoError(t, common.Unmarshal([]byte(input), &request))
	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))

	encoded, err = common.Marshal(ClaudeRequest{Model: "claude-sonnet-4-6", Messages: []ClaudeMessage{{Role: "user", Content: "hello"}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`, string(encoded))
}
