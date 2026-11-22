package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestResponsesBridgeRequestKeepsMediaAndToolHistory(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model:        "claude-test",
		Instructions: json.RawMessage(`"系统要求"`),
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"读文件"},{"type":"input_file","filename":"note.txt","file_data":"data:text/plain;base64,aGk="}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"计划"}]},
			{"type":"function_call","call_id":"call_a","name":"lookup","arguments":"{\"q\":\"值\"}"},
			{"type":"custom_tool_call","call_id":"call_b","name":"run","input":"echo 中文"},
			{"type":"function_call_output","call_id":"call_a","output":[{"type":"input_text","text":"结果"},{"type":"input_image","image_url":"data:image/png;base64,aGk="}]},
			{"type":"custom_tool_call_output","call_id":"call_b","output":"完成"}
		]`),
		Tools: json.RawMessage(`[
			{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}},"strict":false},
			{"type":"custom","name":"run","format":{"type":"text"}}
		]`),
		ToolChoice:        json.RawMessage(`{"type":"custom","name":"run"}`),
		ParallelToolCalls: json.RawMessage(`false`),
		Text:              json.RawMessage(`{"format":{"type":"json_schema","name":"result","schema":{"type":"object"},"strict":true}}`),
	}
	chat, tools, err := ResponsesRequestToChat(&request)
	require.NoError(t, err)
	require.Len(t, chat.Messages, 6)
	require.Equal(t, "系统要求", chat.Messages[0].StringContent())
	parts := chat.Messages[1].ParseContent()
	require.Len(t, parts, 2)
	require.Equal(t, "读文件", parts[0].Text)
	require.Equal(t, "note.txt", parts[1].GetFile().FileName)
	require.Equal(t, "data:text/plain;base64,aGk=", parts[1].GetFile().FileData)
	require.Equal(t, "计划", *chat.Messages[2].ReasoningContent)
	calls := chat.Messages[2].ParseToolCalls()
	require.Len(t, calls, 2)
	require.JSONEq(t, `{"q":"值"}`, calls[0].Function.Arguments)
	require.JSONEq(t, `{"input":"echo 中文"}`, calls[1].Function.Arguments)
	require.Equal(t, "call_a", chat.Messages[3].ToolCallId)
	require.Equal(t, "结果", chat.Messages[3].StringContent())
	require.Equal(t, "call_b", chat.Messages[4].ToolCallId)
	require.Equal(t, "user", chat.Messages[5].Role)
	require.Equal(t, "image_url", chat.Messages[5].ParseContent()[0].Type)
	require.Equal(t, ResponsesTools{"lookup": false, "run": true}, tools)
	require.NotNil(t, chat.ParallelTooCalls)
	require.False(t, *chat.ParallelTooCalls)
	require.Equal(t, map[string]any{"type": "function", "function": map[string]any{"name": "run"}}, chat.ToolChoice)
	require.JSONEq(t, `{"name":"result","schema":{"type":"object"},"strict":true}`, string(chat.ResponseFormat.JsonSchema))
	encoded, err := json.Marshal(chat)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"file_data":"data:text/plain;base64,aGk="`)
}

func TestResponsesBridgeRequestRejectsUnrepresentableData(t *testing.T) {
	for name, patch := range map[string]func(*dto.OpenAIResponsesRequest){
		"preset":       func(r *dto.OpenAIResponsesRequest) { r.Preset = json.RawMessage(`"research"`) },
		"conversation": func(r *dto.OpenAIResponsesRequest) { r.Conversation = json.RawMessage(`"conv_1"`) },
		"previous":     func(r *dto.OpenAIResponsesRequest) { r.PreviousResponseID = "resp_old" },
		"encrypted": func(r *dto.OpenAIResponsesRequest) {
			r.Input = json.RawMessage(`[{"type":"reasoning","encrypted_content":"secret"}]`)
		},
		"arguments": func(r *dto.OpenAIResponsesRequest) {
			r.Input = json.RawMessage(`[{"type":"function_call","call_id":"c","name":"f","arguments":"[1]"}]`)
		},
		"grammar": func(r *dto.OpenAIResponsesRequest) {
			r.Tools = json.RawMessage(`[{"type":"custom","name":"f","format":{"type":"grammar","definition":"x"}}]`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := dto.OpenAIResponsesRequest{Input: json.RawMessage(`"问题"`)}
			patch(&request)
			_, _, err := ResponsesRequestToChat(&request)
			require.Error(t, err)
		})
	}
}

// 逐字节切分同时覆盖反斜线、中文 UTF-8 和 UTF-16 代理对，检查客户端看到的增量。
func TestResponsesBridgeCustomInputStreamsDecodedCharacters(t *testing.T) {
	var events []map[string]any
	bridge := NewResponsesOutputBridge("resp_test", "model", ResponsesTools{"run": true}, func(event map[string]any) error {
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		var snapshot map[string]any
		if err := json.Unmarshal(encoded, &snapshot); err != nil {
			return err
		}
		events = append(events, snapshot)
		return nil
	})
	index := 0
	require.NoError(t, bridge.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_1", Function: dto.FunctionResponse{Name: "run"}}}}}}}))
	wrapped := `{"input":"a\n中\ud83d\ude00\"\\"}`
	for i := range wrapped {
		_ = i
	}
	for i := 0; i < len(wrapped); i++ {
		require.NoError(t, bridge.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Arguments: wrapped[i : i+1]}}}}}}}))
	}
	result, err := bridge.Complete()
	require.NoError(t, err)
	items := result["output"].([]map[string]any)
	require.Len(t, items, 1)
	want := "a\n中😀\"\\"
	require.Equal(t, "custom_tool_call", items[0]["type"])
	require.Equal(t, want, items[0]["input"])
	require.NotContains(t, items[0], "arguments")
	var deltas strings.Builder
	for index, event := range events {
		require.Equal(t, float64(index), event["sequence_number"])
		if event["type"] == "response.custom_tool_call_input.delta" {
			deltas.WriteString(event["delta"].(string))
		}
	}
	require.Equal(t, want, deltas.String())
	require.Equal(t, "response.created", events[0]["type"])
	require.Equal(t, "response.completed", events[len(events)-1]["type"])
}

func TestResponsesBridgeOrderTruncationAndFailureUsage(t *testing.T) {
	bridge := NewResponsesOutputBridge("resp_order", "model", nil, nil)
	first, last := "先", "后"
	index := 0
	for _, delta := range []dto.ChatCompletionsStreamResponseChoiceDelta{
		{Content: &first},
		{ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_1", Function: dto.FunctionResponse{Name: "f", Arguments: "{\"q\":"}}}},
		{Content: &last},
	} {
		require.NoError(t, bridge.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta}}}))
	}
	length := "length"
	require.NoError(t, bridge.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &length}}}))
	result, err := bridge.Complete()
	require.NoError(t, err)
	require.Equal(t, "incomplete", result["status"])
	items := result["output"].([]map[string]any)
	require.Len(t, items, 3)
	require.Equal(t, "先", items[0]["content"].([]map[string]any)[0]["text"])
	require.Equal(t, "{\"q\":", items[1]["arguments"])
	require.Equal(t, "后", items[2]["content"].([]map[string]any)[0]["text"])

	var terminal map[string]any
	failed := NewResponsesOutputBridge("resp_failed", "model", nil, func(event map[string]any) error {
		terminal = event
		return nil
	})
	usage := &dto.Usage{PromptTokens: 11, CompletionTokens: 3, TotalTokens: 14, ToolUsage: map[string]int{"web_search": 1}, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 7}, CompletionTokenDetails: dto.OutputTokenDetails{AudioTokens: 2}}
	failed.SetUsage(usage)
	require.NoError(t, failed.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &first}}}}))
	require.NoError(t, failed.Fail(map[string]any{"message": "上游中断", "code": "overloaded_error"}))
	require.Equal(t, "response.failed", terminal["type"])
	response := terminal["response"].(map[string]any)
	require.Equal(t, "failed", response["status"])
	seen := response["usage"].(*dto.Usage)
	require.Equal(t, 11, seen.InputTokens)
	require.Equal(t, 7, seen.InputTokensDetails.CachedTokens)
	require.Equal(t, 2, seen.OutputTokensDetails.AudioTokens)
	require.Equal(t, 1, seen.ToolUsage["web_search"])
	require.Equal(t, "先", response["output"].([]map[string]any)[0]["content"].([]map[string]any)[0]["text"])
}
