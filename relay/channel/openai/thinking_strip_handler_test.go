package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 直接调用真实 handler，覆盖转换、计量、scanner 和最终 HTTP/SSE writer。
// 不依赖本次之外的 cc12h 测试脚手架，也不启动服务或访问网络。
func runThinkingHandler(t *testing.T, source string, format types.RelayFormat, stream, strip bool, body string, config setting.OutputSensitiveConfig) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, *dto.Usage, *types.NewAPIError) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		StartTime:             time.Now(),
		RelayFormat:           format,
		ShouldIncludeUsage:    true,
		ThinkingProcessStrip:  strip,
		OutputSensitiveConfig: config,
		ChannelMeta:           &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
		ClaudeConvertInfo:     &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
	}
	info.SetEstimatePromptTokens(11)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	resp.Header.Set("X-Upstream-Test", "retained")
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	switch source {
	case "chat":
		if stream {
			usage, apiErr = openai.OaiStreamHandler(c, info, resp)
		} else {
			usage, apiErr = openai.OpenaiHandler(c, info, resp)
		}
	case "responses":
		if format == types.RelayFormatOpenAIResponses {
			if stream {
				usage, apiErr = openai.OaiResponsesStreamHandler(c, info, resp)
			} else {
				usage, apiErr = openai.OaiResponsesHandler(c, info, resp)
			}
		} else if stream {
			usage, apiErr = openai.OaiResponsesToChatStreamHandler(c, info, resp)
		} else {
			usage, apiErr = openai.OaiResponsesToChatHandler(c, info, resp)
		}
	case "claude":
		if stream {
			usage, apiErr = claude.ClaudeStreamHandler(c, resp, info)
		} else {
			usage, apiErr = claude.ClaudeHandler(c, resp, info)
		}
	case "gemini":
		if format == types.RelayFormatGemini {
			if stream {
				usage, apiErr = gemini.GeminiTextGenerationStreamHandler(c, info, resp)
			} else {
				usage, apiErr = gemini.GeminiTextGenerationHandler(c, info, resp)
			}
		} else if stream {
			usage, apiErr = gemini.GeminiChatStreamHandler(c, info, resp)
		} else {
			usage, apiErr = gemini.GeminiChatHandler(c, info, resp)
		}
	default:
		t.Fatalf("未知上游 %q", source)
	}
	return recorder, info, usage, apiErr
}

const thinkingChatJSON = `{"id":"chat-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"HIDDEN_REASONING","content":"<think>HIDDEN_TAG</think>visible"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"completion_tokens_details":{"reasoning_tokens":3}},"extension":{"number":9007199254740993,"thinking":"metadata"}}`
const thinkingResponsesJSON = `{"id":"resp-1","object":"response","model":"gpt-4o","status":"completed","output":[{"id":"r1","type":"reasoning","summary":[{"type":"summary_text","text":"HIDDEN_REASONING"}],"encrypted_content":"HIDDEN_SIGNATURE"},{"id":"m1","type":"message","role":"assistant","content":[{"type":"output_text","text":"<think>HIDDEN_TAG</think>visible"}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"output_tokens_details":{"reasoning_tokens":3}}}`
const thinkingClaudeJSON = `{"id":"msg-1","type":"message","role":"assistant","model":"gpt-4o","content":[{"type":"thinking","thinking":"HIDDEN_REASONING","signature":"HIDDEN_SIGNATURE"},{"type":"text","text":"<think>HIDDEN_TAG</think>visible"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7}}`
const thinkingGeminiJSON = `{"responseId":"gem-1","modelVersion":"gpt-4o","candidates":[{"index":0,"content":{"role":"model","parts":[{"thought":true,"text":"HIDDEN_REASONING","thoughtSignature":"HIDDEN_SIGNATURE"},{"text":"<think>HIDDEN_TAG</think>visible"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"thoughtsTokenCount":3,"totalTokenCount":18}}`

func thinkingSSE(events ...string) string {
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

func thinkingHandlerEvents(t *testing.T, output string) []gjson.Result {
	t.Helper()
	var events []gjson.Result
	for _, line := range strings.Split(output, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		require.True(t, json.Valid([]byte(data)), data)
		events = append(events, gjson.Parse(data))
	}
	return events
}

func thinkingHandlerBody(source string, stream bool) string {
	if !stream {
		return map[string]string{"chat": thinkingChatJSON, "responses": thinkingResponsesJSON, "claude": thinkingClaudeJSON, "gemini": thinkingGeminiJSON}[source]
	}
	switch source {
	case "chat":
		return thinkingSSE(
			`{"id":"chat-1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"HIDDEN_REASONING","content":"<thi"},"finish_reason":null}]}`,
			`{"id":"chat-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"nk>HIDDEN_TAG</think>visible"},"finish_reason":null}]}`,
			`{"id":"chat-1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`, "[DONE]")
	case "responses":
		return thinkingSSE(
			`{"type":"response.created","response":{"id":"resp-1","model":"gpt-4o","status":"in_progress"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"r1","type":"reasoning","summary":[]}}`,
			`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"HIDDEN_REASONING"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"r1","type":"reasoning","summary":[{"type":"summary_text","text":"HIDDEN_REASONING"}]}}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"id":"m1","type":"message","role":"assistant","content":[]}}`,
			`{"type":"response.output_text.delta","item_id":"m1","output_index":1,"content_index":0,"sequence_number":6,"delta":"<thi"}`,
			`{"type":"response.output_text.delta","item_id":"m1","output_index":1,"content_index":0,"sequence_number":7,"delta":"nk>HIDDEN_TAG</think>visible"}`,
			`{"type":"response.completed","response":`+thinkingResponsesJSON+`}`, "[DONE]")
	case "claude":
		return thinkingSSE(
			`{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"gpt-4o","content":[],"usage":{"input_tokens":11,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"HIDDEN_REASONING"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"HIDDEN_SIGNATURE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"<thi"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"nk>HIDDEN_TAG</think>visible"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`)
	case "gemini":
		return thinkingSSE(
			`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"thought":true,"text":"HIDDEN_REASONING"},{"text":"<thi"}]}}]}`,
			`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"nk>HIDDEN_TAG</think>visible"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"thoughtsTokenCount":3,"totalTokenCount":18}}`)
	}
	return ""
}

func TestThinkingStripHandlerMatrix(t *testing.T) {
	cases := []struct {
		source string
		format types.RelayFormat
	}{
		{"chat", types.RelayFormatOpenAI}, {"chat", types.RelayFormatClaude}, {"chat", types.RelayFormatGemini},
		{"responses", types.RelayFormatOpenAIResponses}, {"responses", types.RelayFormatOpenAI}, {"responses", types.RelayFormatClaude}, {"responses", types.RelayFormatGemini},
		{"claude", types.RelayFormatClaude}, {"claude", types.RelayFormatOpenAI},
		{"gemini", types.RelayFormatGemini}, {"gemini", types.RelayFormatOpenAI}, {"gemini", types.RelayFormatClaude},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(tc.source+"/"+string(tc.format)+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				// 隐藏内容中的敏感词应被剥离，不应阻断实际可见正文。
				config := setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"HIDDEN_REASONING", "HIDDEN_TAG"}}
				recorder, info, usage, apiErr := runThinkingHandler(t, tc.source, tc.format, stream, true, thinkingHandlerBody(tc.source, stream), config)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				require.Equal(t, 11, usage.PromptTokens)
				require.Equal(t, 7, usage.CompletionTokens)
				require.Equal(t, 18, usage.TotalTokens)
				output := recorder.Body.String()
				require.Contains(t, output, "visible")
				require.NotContains(t, output, "HIDDEN_")
				require.NotContains(t, output, "output_sensitive")
				if stream {
					require.NotEqual(t, relaycommon.StreamEndReasonOutputSensitive, info.StreamStatus.EndReason)
					switch tc.format {
					case types.RelayFormatOpenAI:
						require.Contains(t, output, "[DONE]")
					case types.RelayFormatClaude:
						require.Contains(t, output, "message_stop")
					case types.RelayFormatOpenAIResponses:
						require.Contains(t, output, "response.completed")
					case types.RelayFormatGemini:
						require.Contains(t, output, "finishReason")
					}
				} else {
					require.True(t, json.Valid(recorder.Body.Bytes()))
					require.Equal(t, strconv.Itoa(recorder.Body.Len()), recorder.Header().Get("Content-Length"))
					require.Equal(t, "retained", recorder.Header().Get("X-Upstream-Test"))
				}
			})
		}
	}
}

func TestThinkingStripResponsesClaudeCompletion(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run("completed="+strconv.FormatBool(completed), func(t *testing.T) {
			body := thinkingSSE(
				`{"type":"response.created","response":{"id":"resp-1","model":"gpt-4o","status":"in_progress"}}`,
				`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"content_index":0,"delta":"visible<thi"}`)
			if completed {
				body += thinkingSSE(`{"type":"response.completed","response":` + thinkingResponsesJSON + `}`)
			}
			recorder, _, usage, apiErr := runThinkingHandler(t, "responses", types.RelayFormatClaude, true, true, body, setting.OutputSensitiveConfig{})
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			events := thinkingHandlerEvents(t, recorder.Body.String())
			var stops, deltas int
			var text strings.Builder
			for _, event := range events {
				switch event.Get("type").String() {
				case "content_block_delta":
					text.WriteString(event.Get("delta.text").String())
				case "message_delta":
					deltas++
					require.Equal(t, "end_turn", event.Get("delta.stop_reason").String())
					require.Equal(t, int64(usage.PromptTokens), event.Get("usage.input_tokens").Int())
					require.Equal(t, int64(usage.CompletionTokens), event.Get("usage.output_tokens").Int())
				case "message_stop":
					stops++
				}
			}
			require.Equal(t, "visible<thi", text.String())
			require.Equal(t, 1, deltas)
			require.Equal(t, 1, stops)
			require.GreaterOrEqual(t, len(events), 3)
			require.Equal(t, "content_block_stop", events[len(events)-3].Get("type").String())
			require.Equal(t, "message_delta", events[len(events)-2].Get("type").String())
			require.Equal(t, "message_stop", events[len(events)-1].Get("type").String())
		})
	}
}

func TestThinkingStripNativeJSONDisabledAndPrecision(t *testing.T) {
	for source, format := range map[string]types.RelayFormat{"chat": types.RelayFormatOpenAI, "responses": types.RelayFormatOpenAIResponses, "claude": types.RelayFormatClaude, "gemini": types.RelayFormatGemini} {
		t.Run(source, func(t *testing.T) {
			body := thinkingHandlerBody(source, false)
			recorder, _, _, apiErr := runThinkingHandler(t, source, format, false, false, body, setting.OutputSensitiveConfig{})
			require.Nil(t, apiErr)
			require.Equal(t, body, recorder.Body.String())
		})
	}
	recorder, _, _, apiErr := runThinkingHandler(t, "chat", types.RelayFormatOpenAI, false, true, thinkingChatJSON, setting.OutputSensitiveConfig{})
	require.Nil(t, apiErr)
	require.Equal(t, "9007199254740993", gjson.Get(recorder.Body.String(), "extension.number").Raw)
	require.Equal(t, "metadata", gjson.Get(recorder.Body.String(), "extension.thinking").String())
	require.JSONEq(t, gjson.Get(thinkingChatJSON, "usage").Raw, gjson.Get(recorder.Body.String(), "usage").Raw)
}

func TestThinkingStripNativeStreamDisabled(t *testing.T) {
	for source, format := range map[string]types.RelayFormat{"chat": types.RelayFormatOpenAI, "responses": types.RelayFormatOpenAIResponses, "claude": types.RelayFormatClaude, "gemini": types.RelayFormatGemini} {
		t.Run(source, func(t *testing.T) {
			body := thinkingHandlerBody(source, true)
			recorder, _, usage, apiErr := runThinkingHandler(t, source, format, true, false, body, setting.OutputSensitiveConfig{})
			require.Nil(t, apiErr)
			require.Equal(t, 18, usage.TotalTokens)
			require.Contains(t, recorder.Body.String(), "HIDDEN_REASONING")
			require.Contains(t, recorder.Body.String(), "HIDDEN_TAG")
			config := setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"HIDDEN_REASONING"}}
			rejected, info, _, _ := runThinkingHandler(t, source, format, true, false, body, config)
			require.Equal(t, relaycommon.StreamEndReasonOutputSensitive, info.StreamStatus.EndReason)
			require.NotContains(t, rejected.Body.String(), "HIDDEN_REASONING")
		})
	}
}

func TestThinkingStripHandlerContentBlockSensitive(t *testing.T) {
	body := `{"id":"msg1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"HIDDEN"},{"type":"text","text":"prefix secret"},{"type":"text","text":"phrase suffix"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7}}`
	for _, action := range []string{"error", "truncate"} {
		t.Run(action, func(t *testing.T) {
			config := setting.OutputSensitiveConfig{Enabled: true, Action: action, MatchPercent: 100, Patterns: []string{"secretphrase"}}
			recorder, _, usage, apiErr := runThinkingHandler(t, "claude", types.RelayFormatClaude, false, true, body, config)
			if action == "error" {
				require.NotNil(t, apiErr)
				require.Empty(t, recorder.Body.String())
				return
			}
			require.Nil(t, apiErr)
			require.Equal(t, 18, usage.TotalTokens)
			require.Equal(t, "prefix ", gjson.Get(recorder.Body.String(), "content.0.text").String())
			require.Empty(t, gjson.Get(recorder.Body.String(), "content.1.text").String())
			require.Equal(t, int64(7), gjson.Get(recorder.Body.String(), "usage.output_tokens").Int())
		})
	}
}

func TestThinkingStripHandlerToolsAndSignatures(t *testing.T) {
	cases := []struct {
		source string
		format types.RelayFormat
		body   string
		path   string
	}{
		{"chat", types.RelayFormatOpenAI, `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"HIDDEN_REASONING","tool_calls":[{"id":"call1","type":"function","signature":"tool_signature","function":{"name":"lookup","arguments":"{\"thinking\":\"keep\",\"signature\":\"arg_sig\",\"n\":9007199254740993}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`, "choices.0.message.tool_calls.0.function.arguments"},
		{"responses", types.RelayFormatOpenAIResponses, `{"object":"response","status":"completed","output":[{"type":"reasoning","summary":[{"text":"HIDDEN_REASONING"}]},{"id":"fc1","call_id":"call1","type":"function_call","name":"lookup","signature":"tool_signature","arguments":"{\"thinking\":\"keep\",\"n\":9007199254740993}"}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`, "output.0.arguments"},
		{"claude", types.RelayFormatClaude, `{"id":"msg1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"HIDDEN_REASONING","signature":"HIDDEN_SIGNATURE"},{"type":"tool_use","id":"call1","name":"lookup","signature":"tool_signature","input":{"thinking":"keep","type":"reasoning","thought":true,"signature":"arg_sig","n":9007199254740993}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":7}}`, "content.0.input"},
		{"gemini", types.RelayFormatGemini, `{"candidates":[{"index":0,"content":{"parts":[{"thought":true,"text":"HIDDEN_REASONING","thoughtSignature":"tool_signature","functionCall":{"name":"lookup","args":{"thinking":"keep","type":"reasoning","thought":true,"signature":"arg_sig","n":9007199254740993}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"thoughtsTokenCount":3,"totalTokenCount":18}}`, "candidates.0.content.parts.0.functionCall.args"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			recorder, _, usage, apiErr := runThinkingHandler(t, tc.source, tc.format, false, true, tc.body, setting.OutputSensitiveConfig{})
			require.Nil(t, apiErr)
			require.Equal(t, 18, usage.TotalTokens)
			output := recorder.Body.String()
			require.NotContains(t, output, "HIDDEN_")
			require.Contains(t, output, "tool_signature")
			args := gjson.Get(output, tc.path)
			if args.Type == gjson.String {
				args = gjson.Parse(args.String())
			}
			require.Equal(t, "keep", args.Get("thinking").String())
			require.Equal(t, "9007199254740993", args.Get("n").Raw)
			config := setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"keep"}}
			rejected, _, _, policyErr := runThinkingHandler(t, tc.source, tc.format, false, true, tc.body, config)
			require.NotNil(t, policyErr, "工具参数中的 thinking 必须参与扫描")
			require.Empty(t, rejected.Body.String())
		})
	}
}

func TestThinkingStripHandlerVisibleSensitive(t *testing.T) {
	for _, action := range []string{"truncate", "error"} {
		for _, stream := range []bool{false, true} {
			t.Run(action+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				config := setting.OutputSensitiveConfig{Enabled: true, Action: action, MatchPercent: 100, Patterns: []string{"visible"}}
				recorder, info, _, apiErr := runThinkingHandler(t, "chat", types.RelayFormatOpenAI, stream, true, thinkingHandlerBody("chat", stream), config)
				require.NotContains(t, recorder.Body.String(), "visible")
				if stream {
					require.Equal(t, relaycommon.StreamEndReasonOutputSensitive, info.StreamStatus.EndReason)
					if action == "error" {
						require.Equal(t, 1, strings.Count(recorder.Body.String(), "sensitive_words_detected"))
						require.NotContains(t, recorder.Body.String(), "[DONE]")
						events := thinkingHandlerEvents(t, recorder.Body.String())
						require.NotEmpty(t, events)
						terminal := events[len(events)-1]
						require.Equal(t, "sensitive_words_detected", terminal.Get("error.code").String())
						require.Equal(t, "invalid_request_error", terminal.Get("error.type").String())
						require.True(t, info.StreamStatus.HasErrors())
					} else {
						require.Equal(t, 1, strings.Count(recorder.Body.String(), "[DONE]"))
					}
				} else if action == "error" {
					require.NotNil(t, apiErr)
					require.Empty(t, recorder.Body.String())
				} else {
					require.Nil(t, apiErr)
					require.Equal(t, int64(18), gjson.Get(recorder.Body.String(), "usage.total_tokens").Int())
				}
			})
		}
	}
}
