package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func geminiResponsesFixture(t *testing.T, stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *Adaptor) {
	t.Helper()
	if stream {
		previous := constant.StreamingTimeout
		constant.StreamingTimeout = 300
		t.Cleanup(func() { constant.StreamingTimeout = previous })
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses,
		RequestURLPath: "/v1/responses", OriginModelName: "gemini-2.5-flash", IsStream: stream, DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: "gemini-2.5-flash"},
	}
	adaptor := &Adaptor{}
	adaptor.Init(info)
	return c, recorder, info, adaptor
}

func geminiResponsesRequest(stream bool) dto.OpenAIResponsesRequest {
	return dto.OpenAIResponsesRequest{
		Model: "gemini-2.5-flash", Input: json.RawMessage(`"你好"`), Stream: &stream,
		Tools: json.RawMessage(`[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}},{"type":"custom","name":"run","format":{"type":"text"}}]`),
	}
}

func geminiResponsesHTTP(body string, stream bool) *http.Response {
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func geminiResponsesSSE(frames ...string) string {
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func geminiResponsesEvents(t *testing.T, body, terminal string) ([]gjson.Result, gjson.Result) {
	t.Helper()
	var events []gjson.Result
	previous := int64(-1)
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		require.True(t, json.Valid([]byte(payload)), "Responses 事件必须是 JSON：%s", payload)
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
	return events, events[len(events)-1].Get("response")
}

func TestResponsesBridgeGeminiConvertKeepsFilesAndToolHistory(t *testing.T) {
	c, _, info, adaptor := geminiResponsesFixture(t, false)
	request := geminiResponsesRequest(false)
	request.Instructions = json.RawMessage(`"按文件回答"`)
	request.Input = json.RawMessage(`[
		{"role":"user","content":[{"type":"input_text","text":"读文件"},{"type":"input_file","filename":"note.pdf","file_data":"data:application/pdf;base64,JVBERi0xLjQK"}]},
		{"type":"function_call","call_id":"call_old","name":"lookup","arguments":"{\"q\":\"值\"}"},
		{"type":"function_call_output","call_id":"call_old","output":"{\"answer\":\"查询结果\"}"}
	]`)
	request.ToolChoice = json.RawMessage(`{"type":"custom","name":"run"}`)
	request.Text = json.RawMessage(`{"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"answer":{"type":"string"}}}}}`)
	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
	require.NoError(t, err)
	require.IsType(t, &dto.GeminiChatRequest{}, converted)
	encoded, err := json.Marshal(converted)
	require.NoError(t, err)
	wire := gjson.ParseBytes(encoded)
	require.Equal(t, "按文件回答", wire.Get("systemInstruction.parts.0.text").String())
	require.Equal(t, "读文件", wire.Get("contents.0.parts.0.text").String())
	require.Equal(t, "application/pdf", wire.Get("contents.0.parts.1.inlineData.mimeType").String())
	require.Equal(t, "JVBERi0xLjQK", wire.Get("contents.0.parts.1.inlineData.data").String())
	require.Equal(t, "model", wire.Get("contents.1.role").String())
	require.Equal(t, "lookup", wire.Get("contents.1.parts.0.functionCall.name").String())
	require.Equal(t, "值", wire.Get("contents.1.parts.0.functionCall.args.q").String())
	require.Equal(t, "lookup", wire.Get("contents.2.parts.0.functionResponse.name").String())
	require.Equal(t, "查询结果", wire.Get("contents.2.parts.0.functionResponse.response.answer").String())
	require.Equal(t, "run", wire.Get("toolConfig.functionCallingConfig.allowedFunctionNames.0").String())
	require.Equal(t, "ANY", wire.Get("toolConfig.functionCallingConfig.mode").String())
	require.Equal(t, "STRING", wire.Get("tools.0.functionDeclarations.1.parameters.properties.input.type").String())
	require.Equal(t, "application/json", wire.Get("generationConfig.responseMimeType").String())
	require.Equal(t, "string", wire.Get("generationConfig.responseSchema.properties.answer.type").String())
}

const geminiResponsesUsage = `{"promptTokenCount":11,"toolUsePromptTokenCount":2,"candidatesTokenCount":7,"thoughtsTokenCount":3,"totalTokenCount":23,"cachedContentTokenCount":5,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":4},{"modality":"TEXT","tokenCount":7}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":2}],"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":2},{"modality":"TEXT","tokenCount":5}]}`

func requireGeminiResponsesUsage(t *testing.T, usage *dto.Usage) {
	t.Helper()
	require.NotNil(t, usage)
	require.Equal(t, 13, usage.PromptTokens)
	require.Equal(t, 10, usage.CompletionTokens)
	require.Equal(t, 23, usage.TotalTokens)
	require.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 4, usage.PromptTokensDetails.AudioTokens)
	require.Equal(t, 9, usage.PromptTokensDetails.TextTokens)
	require.Equal(t, 3, usage.CompletionTokenDetails.ReasoningTokens)
	require.Equal(t, 2, usage.CompletionTokenDetails.AudioTokens)
}

func TestResponsesBridgeGeminiBodyToolsAndContinuation(t *testing.T) {
	full := `{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"内部分析"},{"text":"先"},{"text":"\n"},{"functionCall":{"id":"call_run","name":"run","args":{"input":"echo 中文"}}},{"text":"后"},{"functionCall":{"id":"call_lookup","name":"lookup","args":{"q":"值"}}}]},"finishReason":"STOP"}],"usageMetadata":` + geminiResponsesUsage + `}`
	streamBody := geminiResponsesSSE(
		`{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"内部分析"},{"text":"先"},{"text":"\n"}]}}],"usageMetadata":`+geminiResponsesUsage+`}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_run","name":"run","willContinue":true,"partialArgs":[{"jsonPath":"$.input","stringValue":"echo "}]}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_run","name":"run","willContinue":false,"partialArgs":[{"jsonPath":"$.input","stringValue":"中文"}]}},{"text":"后"},{"functionCall":{"id":"call_lookup","name":"lookup","args":{"q":"值"}}}]}}]}`,
		`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"candidatesTokenCount":7,"thoughtsTokenCount":3,"totalTokenCount":23}}`,
	)
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式续传"
		}
		t.Run(name, func(t *testing.T) {
			c, recorder, info, adaptor := geminiResponsesFixture(t, stream)
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, geminiResponsesRequest(stream))
			require.NoError(t, err)
			body := full
			if stream {
				body = streamBody
			}
			rawUsage, apiErr := adaptor.DoResponse(c, geminiResponsesHTTP(body, stream), info)
			require.Nil(t, apiErr)
			usage, ok := rawUsage.(*dto.Usage)
			require.True(t, ok)
			requireGeminiResponsesUsage(t, usage)
			result := gjson.Parse(recorder.Body.String())
			if stream {
				events, terminal := geminiResponsesEvents(t, recorder.Body.String(), "response.completed")
				result = terminal
				var text, input string
				var toolIDs []string
				for _, event := range events {
					switch event.Get("type").String() {
					case "response.output_text.delta":
						text += event.Get("delta").String()
					case "response.custom_tool_call_input.delta":
						input += event.Get("delta").String()
					case "response.output_item.done":
						if id := event.Get("item.call_id").String(); id != "" {
							toolIDs = append(toolIDs, id)
						}
					}
				}
				require.Equal(t, "先\n后", text, "单独换行 part 不能丢失")
				require.Equal(t, "echo 中文", input)
				require.Equal(t, []string{"call_run", "call_lookup"}, toolIDs, "续传只能产生一个完整工具调用")
			}
			require.Equal(t, "completed", result.Get("status").String())
			require.Len(t, result.Get("output").Array(), 5)
			require.Equal(t, "内部分析", result.Get("output.0.summary.0.text").String())
			require.Equal(t, "先\n", result.Get("output.1.content.0.text").String())
			require.Equal(t, "custom_tool_call", result.Get("output.2.type").String())
			require.Equal(t, "call_run", result.Get("output.2.call_id").String())
			require.Equal(t, "echo 中文", result.Get("output.2.input").String())
			require.False(t, result.Get("output.2.arguments").Exists())
			require.Equal(t, "后", result.Get("output.3.content.0.text").String())
			require.Equal(t, "call_lookup", result.Get("output.4.call_id").String())
			require.JSONEq(t, `{"q":"值"}`, result.Get("output.4.arguments").String())
			require.EqualValues(t, 13, result.Get("usage.input_tokens").Int())
			require.EqualValues(t, 10, result.Get("usage.output_tokens").Int())
			require.EqualValues(t, 3, result.Get("usage.output_tokens_details.reasoning_tokens").Int())
			require.EqualValues(t, 2, result.Get("usage.output_tokens_details.audio_tokens").Int())
			require.EqualValues(t, 5, result.Get("usage.input_tokens_details.cached_tokens").Int())
		})
	}
}

func TestResponsesBridgeGeminiTruncationAndFailureUsage(t *testing.T) {
	partial := `{"candidates":[{"content":{"role":"model","parts":[{"text":"部分正文"}]}}],"usageMetadata":` + geminiResponsesUsage + `}`
	unfinishedCall := `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_a","name":"run","willContinue":true,"partialArgs":[{"jsonPath":"$.input","stringValue":"echo "}]}}]}}]}`
	for _, test := range []struct {
		name, body     string
		stream, failed bool
	}{
		{"非流截断", `{"candidates":[{"content":{"role":"model","parts":[{"text":"部分正文"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":` + geminiResponsesUsage + `}`, false, false},
		{"流式截断", geminiResponsesSSE(partial, `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`), true, false},
		{"缺少终止原因", geminiResponsesSSE(partial), true, true},
		{"工具续传中断", geminiResponsesSSE(partial, unfinishedCall, `{"candidates":[{"finishReason":"STOP"}]}`), true, true},
		{"流式上游错误", geminiResponsesSSE(partial, `{"error":{"code":503,"message":"服务繁忙","status":"UNAVAILABLE"}}`), true, true},
		{"非流上游错误", `{"error":{"code":503,"message":"服务繁忙","status":"UNAVAILABLE"},"usageMetadata":` + geminiResponsesUsage + `}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, recorder, info, adaptor := geminiResponsesFixture(t, test.stream)
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, geminiResponsesRequest(test.stream))
			require.NoError(t, err)
			rawUsage, apiErr := adaptor.DoResponse(c, geminiResponsesHTTP(test.body, test.stream), info)
			usage, ok := rawUsage.(*dto.Usage)
			require.True(t, ok)
			requireGeminiResponsesUsage(t, usage)
			if test.failed {
				require.NotNil(t, apiErr)
				require.True(t, types.IsSkipRetryError(apiErr))
				if test.stream {
					require.True(t, c.GetBool(channel.ResponsesBridgeErrorWritten))
					_, terminal := geminiResponsesEvents(t, recorder.Body.String(), "response.failed")
					require.EqualValues(t, 13, terminal.Get("usage.input_tokens").Int())
					require.EqualValues(t, 10, terminal.Get("usage.output_tokens").Int())
					require.EqualValues(t, 2, terminal.Get("usage.output_tokens_details.audio_tokens").Int())
					require.Equal(t, "部分正文", terminal.Get("output.0.content.0.text").String())
					require.NotContains(t, recorder.Body.String(), "response.completed")
				} else {
					require.Empty(t, recorder.Body.String())
				}
				return
			}
			require.Nil(t, apiErr)
			result := gjson.Parse(recorder.Body.String())
			if test.stream {
				_, result = geminiResponsesEvents(t, recorder.Body.String(), "response.incomplete")
			}
			require.Equal(t, "incomplete", result.Get("status").String())
			require.Equal(t, "max_output_tokens", result.Get("incomplete_details.reason").String())
			require.Equal(t, "部分正文", result.Get("output.0.content.0.text").String())
		})
	}
}

func TestResponsesBridgeGeminiThinkingAndSensitivePolicy(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式"
		}
		t.Run(name, func(t *testing.T) {
			c, recorder, info, adaptor := geminiResponsesFixture(t, stream)
			info.ThinkingProcessStrip = true
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, geminiResponsesRequest(stream))
			require.NoError(t, err)
			body := `{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"机密分析"},{"text":"公开<thi"},{"text":"nk>机密内嵌</think>正文"},{"functionCall":{"id":"call_run","name":"run","args":{"input":"echo 公开工具"}}}]},"finishReason":"STOP"}],"usageMetadata":` + geminiResponsesUsage + `}`
			if stream {
				body = geminiResponsesSSE(body)
			}
			rawUsage, apiErr := adaptor.DoResponse(c, geminiResponsesHTTP(body, stream), info)
			require.Nil(t, apiErr)
			requireGeminiResponsesUsage(t, rawUsage.(*dto.Usage))
			require.NotContains(t, recorder.Body.String(), "机密分析")
			require.NotContains(t, recorder.Body.String(), "机密内嵌")
			result := gjson.Parse(recorder.Body.String())
			if stream {
				_, result = geminiResponsesEvents(t, recorder.Body.String(), "response.completed")
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
			c, recorder, info, adaptor := geminiResponsesFixture(t, false)
			info.ThinkingProcessStrip = strip
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, geminiResponsesRequest(false))
			require.NoError(t, err)
			rawUsage, apiErr := adaptor.DoResponse(c, geminiResponsesHTTP(`{"candidates":[{"content":{"role":"model","parts":[{"text":"机密正文"}]},"finishReason":"STOP"}],"usageMetadata":`+geminiResponsesUsage+`}`, false), info)
			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			requireGeminiResponsesUsage(t, rawUsage.(*dto.Usage))
			require.Empty(t, recorder.Body.String())
		})
	}
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式"
		}
		t.Run("拦截工具参数/"+name, func(t *testing.T) {
			c, recorder, info, adaptor := geminiResponsesFixture(t, stream)
			info.ThinkingProcessStrip = true
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, geminiResponsesRequest(stream))
			require.NoError(t, err)
			body := `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_run","name":"run","args":{"input":"echo 机密工具"}}}]},"finishReason":"STOP"}],"usageMetadata":` + geminiResponsesUsage + `}`
			if stream {
				body = geminiResponsesSSE(body)
			}
			rawUsage, apiErr := adaptor.DoResponse(c, geminiResponsesHTTP(body, stream), info)
			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			requireGeminiResponsesUsage(t, rawUsage.(*dto.Usage))
			require.NotContains(t, recorder.Body.String(), "机密工具")
		})
	}
}
