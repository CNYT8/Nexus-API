package advancedcustom

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func advancedResponsesFixture(t *testing.T, converter, model, upstream string, stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *Adaptor) {
	t.Helper()
	if stream {
		previous := constant.StreamingTimeout
		constant.StreamingTimeout = 300
		t.Cleanup(func() { constant.StreamingTimeout = previous })
	}
	config := &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
		IncomingPath: "/v1/responses", UpstreamPath: upstream, Converter: converter,
	}}}
	require.NoError(t, config.Validate(), "配置层必须接受 Responses 转换器及其入口")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?client=1", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	info := advancedCustomRelayInfo(config)
	info.RequestURLPath = "/v1/responses?client=1"
	info.RelayFormat, info.RelayMode = types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses
	info.OriginModelName, info.UpstreamModelName = model, model
	info.IsStream, info.DisablePing = stream, true
	adaptor := &Adaptor{}
	adaptor.Init(info)
	return c, recorder, info, adaptor
}

func advancedResponsesRequest(model string, stream bool) dto.OpenAIResponsesRequest {
	return dto.OpenAIResponsesRequest{
		Model: model, Stream: &stream, Instructions: json.RawMessage(`"按要求回答"`), Input: json.RawMessage(`"你好"`),
		Tools: json.RawMessage(`[{"type":"custom","name":"run","format":{"type":"text"}}]`),
	}
}

func advancedResponsesHTTP(body string, stream bool) *http.Response {
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func advancedResponsesSSE(frames ...string) string {
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func advancedResponsesTerminal(t *testing.T, body, terminal string) gjson.Result {
	t.Helper()
	var events []gjson.Result
	previous := int64(-1)
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		require.True(t, json.Valid([]byte(payload)), "不得返回 Chat 的 [DONE] 或原生协议帧：%s", payload)
		event := gjson.Parse(payload)
		require.True(t, event.Get("sequence_number").Exists(), payload)
		require.Greater(t, event.Get("sequence_number").Int(), previous)
		previous = event.Get("sequence_number").Int()
		events = append(events, event)
	}
	require.NotEmpty(t, events)
	require.Equal(t, "response.created", events[0].Get("type").String())
	require.Equal(t, terminal, events[len(events)-1].Get("type").String())
	count := 0
	for _, event := range events {
		if event.Get("type").String() == terminal {
			count++
		}
	}
	require.Equal(t, 1, count)
	return events[len(events)-1].Get("response")
}

func TestResponsesBridgeAdvancedCustomRegisteredConverters(t *testing.T) {
	for _, test := range []struct {
		name, converter, model, upstream string
		full                             string
		frames                           []string
	}{
		{
			name: "Chat", converter: "openai_responses_to_openai_chat_completions", model: "gpt-4o-mini", upstream: "/proxy/v1/chat/completions?existing=1",
			full: `{"choices":[{"message":{"role":"assistant","content":"正文","tool_calls":[{"id":"call_run","type":"function","function":{"name":"run","arguments":"{\"input\":\"echo 中文\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`,
			frames: []string{
				`{"choices":[{"index":0,"delta":{"content":"正文"}}]}`,
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_run","type":"function","function":{"name":"run","arguments":"{\"input\":\"echo "}}]}}]}`,
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"中文\"}"}}]},"finish_reason":"tool_calls"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`,
				`[DONE]`,
			},
		},
		{
			name: "Claude", converter: "openai_responses_to_anthropic_messages", model: "claude-sonnet-4", upstream: "/proxy/v1/messages?existing=1",
			full: `{"content":[{"type":"text","text":"正文"},{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 中文"}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":7}}`,
			frames: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":7}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"正文"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 中文"}}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
				`{"type":"message_stop"}`,
			},
		},
		{
			name: "Gemini", converter: "openai_responses_to_gemini_generate_content", model: "gemini-2.5-flash", upstream: "/proxy/v1beta/models/{model}:generateContent?existing=1",
			full: `{"candidates":[{"content":{"role":"model","parts":[{"text":"正文"},{"functionCall":{"id":"call_run","name":"run","args":{"input":"echo 中文"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7,"totalTokenCount":18}}`,
			frames: []string{
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"正文"},{"functionCall":{"id":"call_run","name":"run","args":{"input":"echo 中文"}}}]}}]}`,
				`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7,"totalTokenCount":18}}`,
			},
		},
	} {
		for _, stream := range []bool{false, true} {
			mode := "非流"
			if stream {
				mode = "流式"
			}
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				c, recorder, info, adaptor := advancedResponsesFixture(t, test.converter, test.model, test.upstream, stream)
				converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, advancedResponsesRequest(test.model, stream))
				require.NoError(t, err)
				encoded, err := json.Marshal(converted)
				require.NoError(t, err)
				wire := gjson.ParseBytes(encoded)
				switch test.name {
				case "Chat":
					require.IsType(t, &dto.GeneralOpenAIRequest{}, converted)
					require.Equal(t, "按要求回答", wire.Get("messages.0.content").String())
					require.Equal(t, "你好", wire.Get("messages.1.content").String())
					require.Equal(t, "string", wire.Get("tools.0.function.parameters.properties.input.type").String())
				case "Claude":
					require.IsType(t, &dto.ClaudeRequest{}, converted)
					require.Equal(t, "按要求回答", wire.Get("system.0.text").String())
					require.Equal(t, "你好", wire.Get("messages.0.content").String())
					require.Equal(t, "string", wire.Get("tools.0.input_schema.properties.input.type").String())
				case "Gemini":
					require.IsType(t, &dto.GeminiChatRequest{}, converted)
					require.Equal(t, "按要求回答", wire.Get("systemInstruction.parts.0.text").String())
					require.Equal(t, "你好", wire.Get("contents.0.parts.0.text").String())
					require.Equal(t, "STRING", wire.Get("tools.0.functionDeclarations.0.parameters.properties.input.type").String())
				}
				requestURL, err := adaptor.GetRequestURL(info)
				require.NoError(t, err)
				parsed, err := url.Parse(requestURL)
				require.NoError(t, err)
				require.Equal(t, "fallback.example", parsed.Host)
				require.Equal(t, "1", parsed.Query().Get("existing"))
				require.Empty(t, parsed.Query().Get("client"))
				wantPath := strings.Split(strings.ReplaceAll(test.upstream, "{model}", test.model), "?")[0]
				if test.name == "Gemini" && stream {
					wantPath = "/proxy/v1beta/models/gemini-2.5-flash:streamGenerateContent"
					require.Equal(t, "sse", parsed.Query().Get("alt"))
				} else {
					require.Empty(t, parsed.Query().Get("alt"))
				}
				require.Equal(t, wantPath, parsed.Path)
				header := http.Header{}
				require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
				require.Equal(t, "Bearer sk-test", header.Get("Authorization"))
				if test.name == "Claude" {
					require.Equal(t, "2023-06-01", header.Get("anthropic-version"))
					c.Request.Header.Set("anthropic-version", "2023-01-01")
					require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
					require.Equal(t, "2023-01-01", header.Get("anthropic-version"))
				} else {
					require.Empty(t, header.Get("anthropic-version"))
				}
				body := test.full
				if stream {
					body = advancedResponsesSSE(test.frames...)
				}
				rawUsage, apiErr := adaptor.DoResponse(c, advancedResponsesHTTP(body, stream), info)
				require.Nil(t, apiErr)
				usage, ok := rawUsage.(*dto.Usage)
				require.True(t, ok)
				require.Equal(t, 11, usage.PromptTokens)
				require.Equal(t, 7, usage.CompletionTokens)
				result := gjson.Parse(recorder.Body.String())
				if stream {
					result = advancedResponsesTerminal(t, recorder.Body.String(), "response.completed")
					require.Contains(t, recorder.Body.String(), "response.output_text.delta")
					require.Contains(t, recorder.Body.String(), "response.custom_tool_call_input.delta")
				}
				require.Equal(t, "response", result.Get("object").String())
				require.Equal(t, "completed", result.Get("status").String())
				require.Len(t, result.Get("output").Array(), 2)
				require.Equal(t, "正文", result.Get("output.0.content.0.text").String())
				require.Equal(t, "custom_tool_call", result.Get("output.1.type").String())
				require.Equal(t, "call_run", result.Get("output.1.call_id").String())
				require.Equal(t, "echo 中文", result.Get("output.1.input").String())
				require.EqualValues(t, 11, result.Get("usage.input_tokens").Int())
				require.EqualValues(t, 7, result.Get("usage.output_tokens").Int())
			})
		}
	}
}

const advancedResponsesChatUsage = `{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":5,"audio_tokens":3},"completion_tokens_details":{"reasoning_tokens":2,"audio_tokens":1}}`

func TestResponsesBridgeAdvancedCustomChatTruncationAndFailureUsage(t *testing.T) {
	partial := `{"choices":[{"index":0,"delta":{"content":"部分正文"}}],"usage":` + advancedResponsesChatUsage + `}`
	for _, test := range []struct {
		name, body     string
		stream, failed bool
	}{
		{"非流截断", `{"choices":[{"message":{"role":"assistant","content":"部分正文"},"finish_reason":"length"}],"usage":` + advancedResponsesChatUsage + `}`, false, false},
		{"流式截断", advancedResponsesSSE(partial, `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, `[DONE]`), true, false},
		{"缺少结束原因", advancedResponsesSSE(partial, `[DONE]`), true, true},
		{"流式上游错误", advancedResponsesSSE(partial, `{"error":{"message":"服务繁忙","type":"server_error","code":"overloaded"},"usage":{"completion_tokens":9,"total_tokens":20}}`), true, true},
		{"非流上游错误", `{"error":{"message":"服务繁忙","type":"server_error","code":"overloaded"},"usage":` + advancedResponsesChatUsage + `}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, recorder, info, adaptor := advancedResponsesFixture(t, "openai_responses_to_openai_chat_completions", "gpt-4o-mini", "/v1/chat/completions", test.stream)
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, advancedResponsesRequest("gpt-4o-mini", test.stream))
			require.NoError(t, err)
			rawUsage, apiErr := adaptor.DoResponse(c, advancedResponsesHTTP(test.body, test.stream), info)
			usage, ok := rawUsage.(*dto.Usage)
			require.True(t, ok)
			require.Equal(t, 11, usage.PromptTokens)
			require.GreaterOrEqual(t, usage.CompletionTokens, 7)
			require.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 3, usage.PromptTokensDetails.AudioTokens)
			require.Equal(t, 2, usage.CompletionTokenDetails.ReasoningTokens)
			require.Equal(t, 1, usage.CompletionTokenDetails.AudioTokens)
			if test.failed {
				require.NotNil(t, apiErr)
				require.True(t, types.IsSkipRetryError(apiErr))
				if test.stream {
					require.True(t, c.GetBool(channel.ResponsesBridgeErrorWritten))
					result := advancedResponsesTerminal(t, recorder.Body.String(), "response.failed")
					require.Equal(t, "部分正文", result.Get("output.0.content.0.text").String())
					require.EqualValues(t, 11, result.Get("usage.input_tokens").Int())
					require.EqualValues(t, usage.CompletionTokens, result.Get("usage.output_tokens").Int())
					require.EqualValues(t, 5, result.Get("usage.input_tokens_details.cached_tokens").Int())
					require.EqualValues(t, 1, result.Get("usage.output_tokens_details.audio_tokens").Int())
					require.NotContains(t, recorder.Body.String(), "response.completed")
				} else {
					require.Empty(t, recorder.Body.String())
				}
				return
			}
			require.Nil(t, apiErr)
			result := gjson.Parse(recorder.Body.String())
			if test.stream {
				result = advancedResponsesTerminal(t, recorder.Body.String(), "response.incomplete")
			}
			require.Equal(t, "incomplete", result.Get("status").String())
			require.Equal(t, "max_output_tokens", result.Get("incomplete_details.reason").String())
			require.Equal(t, "部分正文", result.Get("output.0.content.0.text").String())
		})
	}
}

func TestResponsesBridgeAdvancedCustomChatThinkingAndSensitivePolicy(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式"
		}
		t.Run(name, func(t *testing.T) {
			c, recorder, info, adaptor := advancedResponsesFixture(t, "openai_responses_to_openai_chat_completions", "gpt-4o-mini", "/v1/chat/completions", stream)
			info.ThinkingProcessStrip = true
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, advancedResponsesRequest("gpt-4o-mini", stream))
			require.NoError(t, err)
			body := `{"choices":[{"message":{"role":"assistant","reasoning_content":"机密分析","content":"公开<think>机密内嵌</think>正文","tool_calls":[{"id":"call_run","type":"function","function":{"name":"run","arguments":"{\"input\":\"echo 公开工具\"}"}}]},"finish_reason":"tool_calls"}],"usage":` + advancedResponsesChatUsage + `}`
			if stream {
				body = advancedResponsesSSE(
					`{"choices":[{"index":0,"delta":{"reasoning_content":"机密分析","content":"公开<thi"}}]}`,
					`{"choices":[{"index":0,"delta":{"content":"nk>机密内嵌</think>正文"}}]}`,
					`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_run","type":"function","function":{"name":"run","arguments":"{\"input\":\"echo 公开工具\"}"}}]},"finish_reason":"tool_calls"}]}`,
					`{"choices":[],"usage":`+advancedResponsesChatUsage+`}`, `[DONE]`,
				)
			}
			rawUsage, apiErr := adaptor.DoResponse(c, advancedResponsesHTTP(body, stream), info)
			require.Nil(t, apiErr)
			require.Equal(t, 7, rawUsage.(*dto.Usage).CompletionTokens)
			require.NotContains(t, recorder.Body.String(), "机密分析")
			require.NotContains(t, recorder.Body.String(), "机密内嵌")
			result := gjson.Parse(recorder.Body.String())
			if stream {
				result = advancedResponsesTerminal(t, recorder.Body.String(), "response.completed")
			}
			require.Len(t, result.Get("output").Array(), 2)
			require.Equal(t, "公开正文", result.Get("output.0.content.0.text").String())
			require.Equal(t, "echo 公开工具", result.Get("output.1.input").String())
		})
	}
	for _, strip := range []bool{false, true} {
		name := "保留思考"
		if strip {
			name = "剥离思考"
		}
		t.Run("拦截可见正文/"+name, func(t *testing.T) {
			c, recorder, info, adaptor := advancedResponsesFixture(t, "openai_responses_to_openai_chat_completions", "gpt-4o-mini", "/v1/chat/completions", false)
			info.ThinkingProcessStrip = strip
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, advancedResponsesRequest("gpt-4o-mini", false))
			require.NoError(t, err)
			rawUsage, apiErr := adaptor.DoResponse(c, advancedResponsesHTTP(`{"choices":[{"message":{"content":"机密正文"},"finish_reason":"stop"}],"usage":`+advancedResponsesChatUsage+`}`, false), info)
			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Equal(t, 11, rawUsage.(*dto.Usage).PromptTokens)
			require.Equal(t, 7, rawUsage.(*dto.Usage).CompletionTokens)
			require.Empty(t, recorder.Body.String())
		})
	}
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式"
		}
		t.Run("拦截工具参数/"+name, func(t *testing.T) {
			c, recorder, info, adaptor := advancedResponsesFixture(t, "openai_responses_to_openai_chat_completions", "gpt-4o-mini", "/v1/chat/completions", stream)
			info.ThinkingProcessStrip = true
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, advancedResponsesRequest("gpt-4o-mini", stream))
			require.NoError(t, err)
			body := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_run","type":"function","function":{"name":"run","arguments":"{\"input\":\"echo 机密工具\"}"}}]},"finish_reason":"tool_calls"}],"usage":` + advancedResponsesChatUsage + `}`
			if stream {
				body = advancedResponsesSSE(
					`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_run","type":"function","function":{"name":"run","arguments":"{\"input\":\"echo 机密工具\"}"}}]},"finish_reason":"tool_calls"}]}`,
					`{"choices":[],"usage":`+advancedResponsesChatUsage+`}`, `[DONE]`,
				)
			}
			_, apiErr := adaptor.DoResponse(c, advancedResponsesHTTP(body, stream), info)
			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.NotContains(t, recorder.Body.String(), "机密工具")
		})
	}
}
