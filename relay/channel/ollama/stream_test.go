package ollama

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOllamaStreamTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	return c, w
}

func collectOllamaStreamChunks(t *testing.T, body string) []dto.ChatCompletionsStreamResponse {
	t.Helper()
	chunks := make([]dto.ChatCompletionsStreamResponse, 0)
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" || data == "" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, common.Unmarshal([]byte(data), &chunk))
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestOllamaChatHandlerNonStreamToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		raw    string
		wantID string
	}{
		{
			name:   "compact json per-line parse path",
			raw:    `{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_upstream","function":{"name":"get_weather","arguments":{"city":"Paris","days":0}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
			wantID: "call_0",
		},
		{
			name: "pretty json fallback parse path",
			raw: `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {
            "city": "Paris",
            "days": 0
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`,
			wantID: "call_0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

			var toolCalls []dto.ToolCallResponse
			require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
			require.Len(t, toolCalls, 1)
			assert.Equal(t, tt.wantID, toolCalls[0].ID)
			assert.Equal(t, "function", toolCalls[0].Type)
			assert.Equal(t, "get_weather", toolCalls[0].Function.Name)
			assert.Nil(t, toolCalls[0].Index)

			var args map[string]any
			require.NoError(t, common.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args))
			assert.Equal(t, "Paris", args["city"])
			assert.Equal(t, float64(0), args["days"])
		})
	}
}

func TestBuildOllamaStreamDelta(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		startIndex    int
		wantContent   string
		wantReasoning string
		wantToolCalls []struct {
			id        string
			name      string
			index     int
			arguments string
		}
		wantNextIndex int
		wantPayload   bool
	}{
		{
			name:        "chat content",
			raw:         `{"message":{"content":"hello"}}`,
			wantContent: "hello",
			wantPayload: true,
		},
		{
			name:        "generate content",
			raw:         `{"response":"hello"}`,
			wantContent: "hello",
			wantPayload: true,
		},
		{
			name:          "thinking json string",
			raw:           `{"message":{"thinking":"consider this"}}`,
			wantReasoning: "consider this",
			wantPayload:   true,
		},
		{
			name:          "thinking raw fallback",
			raw:           `{"message":{"thinking":{"step":"consider this"}}}`,
			wantReasoning: `{"step":"consider this"}`,
			wantPayload:   true,
		},
		{
			name:       "multiple tool calls",
			raw:        `{"message":{"tool_calls":[{"id":"call_upstream","function":{"name":"get_weather","arguments":{"city":"Paris"}}},{"function":{"name":"get_time","arguments":{"timezone":"UTC"}}}]}}`,
			startIndex: 2,
			wantToolCalls: []struct {
				id        string
				name      string
				index     int
				arguments string
			}{
				{id: "call_2", name: "get_weather", index: 2, arguments: `{"city":"Paris"}`},
				{id: "call_3", name: "get_time", index: 3, arguments: `{"timezone":"UTC"}`},
			},
			wantNextIndex: 4,
			wantPayload:   true,
		},
		{
			name:        "empty chunk has no payload",
			raw:         `{"message":{"content":"","thinking":null}}`,
			wantPayload: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk ollamaChatStreamChunk
			require.NoError(t, common.Unmarshal([]byte(tt.raw), &chunk))

			toolCallIndex := tt.startIndex
			delta, hasPayload := buildOllamaStreamDelta(&chunk, "response-id", 123, "model", &toolCallIndex)
			assert.Equal(t, tt.wantPayload, hasPayload)
			assert.Equal(t, "response-id", delta.Id)
			assert.Equal(t, "chat.completion.chunk", delta.Object)
			assert.Equal(t, int64(123), delta.Created)
			assert.Equal(t, "model", delta.Model)
			require.Len(t, delta.Choices, 1)
			assert.Equal(t, tt.wantContent, delta.Choices[0].Delta.GetContentString())
			assert.Equal(t, tt.wantReasoning, delta.Choices[0].Delta.GetReasoningContent())
			assert.Equal(t, tt.wantNextIndex, toolCallIndex)

			if tt.wantToolCalls == nil {
				assert.Empty(t, delta.Choices[0].Delta.ToolCalls)
				return
			}
			require.Len(t, delta.Choices[0].Delta.ToolCalls, len(tt.wantToolCalls))
			for i, want := range tt.wantToolCalls {
				got := delta.Choices[0].Delta.ToolCalls[i]
				assert.Equal(t, want.id, got.ID)
				assert.Equal(t, "function", got.Type)
				assert.Equal(t, want.name, got.Function.Name)
				assert.Equal(t, want.arguments, got.Function.Arguments)
				require.NotNil(t, got.Index)
				assert.Equal(t, want.index, *got.Index)
			}
		})
	}
}

func TestOllamaStreamHandlerDoneToolCalls(t *testing.T) {
	c, w := newOllamaStreamTestContext()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3-coder","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"北京"}}},{"function":{"name":"get_time","arguments":{"timezone":"Asia/Shanghai"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`)),
	}

	usage, apiErr := ollamaStreamHandler(c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
	}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.TotalTokens)

	chunks := collectOllamaStreamChunks(t, w.Body.String())
	var toolChunkIndex, stopChunkIndex int = -1, -1
	for i := range chunks {
		if len(chunks[i].Choices) == 0 {
			continue
		}
		if len(chunks[i].Choices[0].Delta.ToolCalls) > 0 {
			toolChunkIndex = i
		}
		if chunks[i].Choices[0].FinishReason != nil {
			stopChunkIndex = i
		}
	}
	require.NotEqual(t, -1, toolChunkIndex)
	require.NotEqual(t, -1, stopChunkIndex)
	assert.Less(t, toolChunkIndex, stopChunkIndex)

	toolCalls := chunks[toolChunkIndex].Choices[0].Delta.ToolCalls
	require.Len(t, toolCalls, 2)
	assert.Equal(t, "call_0", toolCalls[0].ID)
	assert.Equal(t, "call_1", toolCalls[1].ID)
	require.NotNil(t, toolCalls[0].Index)
	require.NotNil(t, toolCalls[1].Index)
	assert.Equal(t, 0, *toolCalls[0].Index)
	assert.Equal(t, 1, *toolCalls[1].Index)
	assert.Equal(t, constant.FinishReasonToolCalls, *chunks[stopChunkIndex].Choices[0].FinishReason)
	assert.Contains(t, w.Body.String(), "data: [DONE]")
}

func TestOllamaStreamHandlerDoneContentIsEmittedBeforeStop(t *testing.T) {
	c, w := newOllamaStreamTestContext()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"final answer"},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`)),
	}

	usage, apiErr := ollamaStreamHandler(c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
	}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.TotalTokens)

	body := w.Body.String()
	assert.Contains(t, body, `"content":"final answer"`)
	assert.Contains(t, body, `"finish_reason":"stop"`)
	contentIndex := strings.Index(body, `"content":"final answer"`)
	stopIndex := strings.Index(body, `"finish_reason":"stop"`)
	require.NotEqual(t, -1, contentIndex)
	require.NotEqual(t, -1, stopIndex)
	assert.Less(t, contentIndex, stopIndex)
	assert.Contains(t, body, "data: [DONE]")
}

func TestOllamaStreamHandlerDoneThinkingStripKeepsToolCalls(t *testing.T) {
	c, w := newOllamaStreamTestContext()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3-coder","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","thinking":"private chain","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`)),
	}

	usage, apiErr := ollamaStreamHandler(c, &relaycommon.RelayInfo{
		ChannelMeta:          &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
		ThinkingProcessStrip: true,
	}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.TotalTokens)

	body := w.Body.String()
	assert.NotContains(t, body, "private chain")
	assert.NotContains(t, body, "reasoning_content")

	chunks := collectOllamaStreamChunks(t, body)
	var toolChunkIndex, stopChunkIndex int = -1, -1
	for i := range chunks {
		if len(chunks[i].Choices) == 0 {
			continue
		}
		if len(chunks[i].Choices[0].Delta.ToolCalls) > 0 {
			toolChunkIndex = i
		}
		if chunks[i].Choices[0].FinishReason != nil {
			stopChunkIndex = i
		}
	}
	require.NotEqual(t, -1, toolChunkIndex)
	require.NotEqual(t, -1, stopChunkIndex)
	assert.Less(t, toolChunkIndex, stopChunkIndex)
	assert.Equal(t, constant.FinishReasonToolCalls, *chunks[stopChunkIndex].Choices[0].FinishReason)
}

func TestOllamaStreamHandlerDoneSensitiveWordKeepsUsage(t *testing.T) {
	c, w := newOllamaStreamTestContext()

	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
		RelayFormat:  types.RelayFormatOpenAI,
		StreamStatus: relaycommon.NewStreamStatus(),
		OutputSensitiveConfig: setting.OutputSensitiveConfig{
			Enabled:      true,
			Action:       "truncate",
			MatchPercent: 100,
			Patterns:     []string{"secret"},
		},
	}
	body := strings.Join([]string{
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"hello"},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:01Z","message":{"role":"assistant","content":"top secret final"},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	// The done frame is scanned too; billing usage must survive a policy hit.
	assert.Equal(t, 5, usage.PromptTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
	assert.Equal(t, 12, usage.TotalTokens)

	responseBody := w.Body.String()
	assert.Contains(t, responseBody, `"content":"hello"`)
	assert.NotContains(t, responseBody, "top secret final")
	assert.NotContains(t, responseBody, `"finish_reason":"stop"`)
	assert.Equal(t, relaycommon.StreamEndReasonOutputSensitive, info.StreamStatus.EndReason)
	assert.Contains(t, responseBody, "data: [DONE]")
}
