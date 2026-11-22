package claude

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

func claudeResponsesFixture(t *testing.T, stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *Adaptor) {
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
		RequestURLPath: "/v1/responses", OriginModelName: "claude-sonnet-4", IsStream: stream, DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4"},
	}
	adaptor := &Adaptor{}
	adaptor.Init(info)
	return c, recorder, info, adaptor
}

func claudeResponsesRequest(stream bool) dto.OpenAIResponsesRequest {
	return dto.OpenAIResponsesRequest{
		Model: "claude-sonnet-4", Input: json.RawMessage(`"你好"`), Stream: &stream,
		Tools: json.RawMessage(`[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}},{"type":"custom","name":"run","format":{"type":"text"}}]`),
	}
}

func claudeResponsesHTTP(body string, stream bool) *http.Response {
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func claudeResponsesSSE(frames ...string) string {
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func claudeResponsesEvents(t *testing.T, body, terminal string) ([]gjson.Result, gjson.Result) {
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
	require.Equal(t, 1, count, "终态只能发送一次")
	return events, events[len(events)-1].Get("response")
}

func TestResponsesBridgeClaudeConvertKeepsFilesAndToolHistory(t *testing.T) {
	c, _, info, adaptor := claudeResponsesFixture(t, false)
	request := claudeResponsesRequest(false)
	request.Instructions = json.RawMessage(`"按文件回答"`)
	request.Input = json.RawMessage(`[
		{"role":"user","content":[{"type":"input_text","text":"读文件"},{"type":"input_file","filename":"note.pdf","file_data":"data:application/pdf;base64,JVBERi0xLjQK"},{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aWZkAAAAASUVORK5CYII="}]},
		{"type":"function_call","call_id":"call_old","name":"lookup","arguments":"{\"q\":\"值\"}"},
		{"type":"function_call_output","call_id":"call_old","output":"查询结果"}
	]`)
	request.ToolChoice = json.RawMessage(`{"type":"custom","name":"run"}`)
	request.Text = json.RawMessage(`{"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"answer":{"type":"string"}}},"strict":true}}`)
	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
	require.NoError(t, err)
	require.IsType(t, &dto.ClaudeRequest{}, converted)
	encoded, err := json.Marshal(converted)
	require.NoError(t, err)
	wire := gjson.ParseBytes(encoded)
	require.Equal(t, "按文件回答", wire.Get("system.0.text").String())
	parts := wire.Get("messages.0.content").Array()
	require.Len(t, parts, 3, "原生转换重建 Message 后仍须保留正文、文件和图片")
	require.Equal(t, "读文件", parts[0].Get("text").String())
	require.Equal(t, "document", parts[1].Get("type").String())
	require.Equal(t, "application/pdf", parts[1].Get("source.media_type").String())
	require.Equal(t, "JVBERi0xLjQK", parts[1].Get("source.data").String())
	require.Equal(t, "image", parts[2].Get("type").String())
	require.Equal(t, "image/png", parts[2].Get("source.media_type").String())
	require.NotEmpty(t, parts[2].Get("source.data").String())
	toolHistory := wire.Get(`messages.1.content.#(type=="tool_use")`)
	require.Equal(t, "call_old", toolHistory.Get("id").String())
	require.Equal(t, "值", toolHistory.Get("input.q").String())
	require.Equal(t, "tool_result", wire.Get("messages.2.content.0.type").String())
	require.Equal(t, "call_old", wire.Get("messages.2.content.0.tool_use_id").String())
	require.Equal(t, "查询结果", wire.Get("messages.2.content.0.content").String())
	require.Equal(t, "run", wire.Get("tool_choice.name").String())
	require.Equal(t, "string", wire.Get("tools.1.input_schema.properties.input.type").String())
	require.Equal(t, "json_schema", wire.Get("output_config.format.type").String())
	require.Equal(t, "string", wire.Get("output_config.format.schema.properties.answer.type").String())
}

const claudeResponsesUsage = `{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":5,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":2},"server_tool_use":{"web_search_requests":2}}`

func TestResponsesBridgeClaudeBodyToolsAndUsage(t *testing.T) {
	full := `{"type":"message","content":[{"type":"thinking","thinking":"内部分析"},{"type":"text","text":"先"},{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 中文"}},{"type":"text","text":"后"},{"type":"tool_use","id":"call_lookup","name":"lookup","input":{"q":"值"}}],"stop_reason":"tool_use","usage":` + claudeResponsesUsage + `}`
	streamBody := claudeResponsesSSE(
		`{"type":"message_start","message":{"id":"msg_a","role":"assistant","usage":`+claudeResponsesUsage+`}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"内部分析"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"先"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_run","name":"run","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"echo "}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"中文\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"text","text":"后"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"call_lookup","name":"lookup","input":{"q":"值"}}}`,
		`{"type":"content_block_stop","index":4}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	)
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式"
		}
		t.Run(name, func(t *testing.T) {
			c, recorder, info, adaptor := claudeResponsesFixture(t, stream)
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, claudeResponsesRequest(stream))
			require.NoError(t, err)
			body := full
			if stream {
				body = streamBody
			}
			rawUsage, apiErr := adaptor.DoResponse(c, claudeResponsesHTTP(body, stream), info)
			require.Nil(t, apiErr)
			usage, ok := rawUsage.(*dto.Usage)
			require.True(t, ok)
			require.Equal(t, 11, usage.PromptTokens, "结算保持 Anthropic 未缓存输入语义")
			require.Equal(t, "anthropic", usage.UsageSemantic)
			require.Equal(t, 7, usage.CompletionTokens)
			require.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 3, usage.PromptTokensDetails.CachedCreationTokens)
			require.Equal(t, 1, usage.ClaudeCacheCreation5mTokens)
			require.Equal(t, 2, usage.ClaudeCacheCreation1hTokens)
			require.Equal(t, 2, usage.ToolUsage["web_search"])
			result := gjson.Parse(recorder.Body.String())
			if stream {
				events, terminal := claudeResponsesEvents(t, recorder.Body.String(), "response.completed")
				result = terminal
				var text, input string
				var done []string
				for _, event := range events {
					switch event.Get("type").String() {
					case "response.output_text.delta":
						text += event.Get("delta").String()
					case "response.custom_tool_call_input.delta":
						input += event.Get("delta").String()
					case "response.output_item.done":
						done = append(done, event.Get("item.id").String())
					}
				}
				require.Equal(t, "先后", text)
				require.Equal(t, "echo 中文", input)
				require.Len(t, done, 5)
				for index, item := range result.Get("output").Array() {
					require.Equal(t, item.Get("id").String(), done[index])
				}
			}
			require.Equal(t, "completed", result.Get("status").String())
			require.Len(t, result.Get("output").Array(), 5)
			require.Equal(t, "内部分析", result.Get("output.0.summary.0.text").String())
			require.Equal(t, "先", result.Get("output.1.content.0.text").String())
			require.Equal(t, "custom_tool_call", result.Get("output.2.type").String())
			require.Equal(t, "call_run", result.Get("output.2.call_id").String())
			require.Equal(t, "echo 中文", result.Get("output.2.input").String())
			require.False(t, result.Get("output.2.arguments").Exists())
			require.Equal(t, "后", result.Get("output.3.content.0.text").String())
			require.Equal(t, "call_lookup", result.Get("output.4.call_id").String())
			require.JSONEq(t, `{"q":"值"}`, result.Get("output.4.arguments").String())
			require.EqualValues(t, 19, result.Get("usage.input_tokens").Int(), "客户端输入用量包含缓存读写")
			require.EqualValues(t, 7, result.Get("usage.output_tokens").Int())
			require.EqualValues(t, 26, result.Get("usage.total_tokens").Int())
			require.EqualValues(t, 5, result.Get("usage.input_tokens_details.cached_tokens").Int())
		})
	}
}

func TestResponsesBridgeClaudeTruncationAndFailureUsage(t *testing.T) {
	start := `{"type":"message_start","message":{"usage":` + claudeResponsesUsage + `}}`
	text := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"部分正文"}}`
	delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`
	for _, test := range []struct {
		name, body     string
		stream, failed bool
	}{
		{"非流截断", `{"content":[{"type":"text","text":"部分正文"}],"stop_reason":"max_tokens","usage":` + claudeResponsesUsage + `}`, false, false},
		{"流式截断", claudeResponsesSSE(start, text, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":7}}`, `{"type":"message_stop"}`), true, false},
		{"缺失终止帧", claudeResponsesSSE(start, text, `{"type":"content_block_stop","index":0}`, delta), true, true},
		{"流式上游错误", claudeResponsesSSE(start, text, `{"type":"content_block_stop","index":0}`, delta, `{"type":"error","error":{"type":"overloaded_error","message":"服务繁忙"},"usage":{"output_tokens":9}}`), true, true},
		{"非流上游错误", `{"type":"error","error":{"type":"overloaded_error","message":"服务繁忙"},"usage":` + claudeResponsesUsage + `}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, recorder, info, adaptor := claudeResponsesFixture(t, test.stream)
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, claudeResponsesRequest(test.stream))
			require.NoError(t, err)
			rawUsage, apiErr := adaptor.DoResponse(c, claudeResponsesHTTP(test.body, test.stream), info)
			usage, ok := rawUsage.(*dto.Usage)
			require.True(t, ok)
			require.Equal(t, 11, usage.PromptTokens)
			require.GreaterOrEqual(t, usage.CompletionTokens, 7)
			require.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 2, usage.ToolUsage["web_search"])
			if test.failed {
				require.NotNil(t, apiErr)
				require.True(t, types.IsSkipRetryError(apiErr))
				if test.stream {
					require.True(t, c.GetBool(channel.ResponsesBridgeErrorWritten))
					_, terminal := claudeResponsesEvents(t, recorder.Body.String(), "response.failed")
					require.EqualValues(t, 19, terminal.Get("usage.input_tokens").Int())
					require.EqualValues(t, usage.CompletionTokens, terminal.Get("usage.output_tokens").Int())
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
				_, result = claudeResponsesEvents(t, recorder.Body.String(), "response.incomplete")
			}
			require.Equal(t, "incomplete", result.Get("status").String())
			require.Equal(t, "max_output_tokens", result.Get("incomplete_details.reason").String())
			require.Equal(t, "部分正文", result.Get("output.0.content.0.text").String())
		})
	}
}

func TestResponsesBridgeClaudeThinkingAndSensitivePolicy(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "非流"
		if stream {
			name = "流式"
		}
		t.Run(name, func(t *testing.T) {
			c, recorder, info, adaptor := claudeResponsesFixture(t, stream)
			info.ThinkingProcessStrip = true
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, claudeResponsesRequest(stream))
			require.NoError(t, err)
			body := `{"content":[{"type":"thinking","thinking":"机密分析"},{"type":"text","text":"公开<think>机密内嵌</think>正文"},{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 公开工具"}}],"stop_reason":"tool_use","usage":` + claudeResponsesUsage + `}`
			if stream {
				body = claudeResponsesSSE(
					`{"type":"message_start","message":{"usage":`+claudeResponsesUsage+`}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"机密分析"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"公开<thi"}}`,
					`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"nk>机密内嵌</think>正文"}}`,
					`{"type":"content_block_stop","index":1}`,
					`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 公开工具"}}}`,
					`{"type":"content_block_stop","index":2}`,
					`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
					`{"type":"message_stop"}`,
				)
			}
			rawUsage, apiErr := adaptor.DoResponse(c, claudeResponsesHTTP(body, stream), info)
			require.Nil(t, apiErr, "已剥离的思考不参与可见输出检查，正常工具输入应通过")
			require.Equal(t, 7, rawUsage.(*dto.Usage).CompletionTokens)
			require.NotContains(t, recorder.Body.String(), "机密分析")
			require.NotContains(t, recorder.Body.String(), "机密内嵌")
			result := gjson.Parse(recorder.Body.String())
			if stream {
				_, result = claudeResponsesEvents(t, recorder.Body.String(), "response.completed")
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
			c, recorder, info, adaptor := claudeResponsesFixture(t, false)
			info.ThinkingProcessStrip = strip
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, claudeResponsesRequest(false))
			require.NoError(t, err)
			rawUsage, apiErr := adaptor.DoResponse(c, claudeResponsesHTTP(`{"content":[{"type":"text","text":"机密正文"}],"stop_reason":"end_turn","usage":`+claudeResponsesUsage+`}`, false), info)
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
			c, recorder, info, adaptor := claudeResponsesFixture(t, stream)
			info.ThinkingProcessStrip = true
			info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"机密"}}
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, claudeResponsesRequest(stream))
			require.NoError(t, err)
			body := `{"content":[{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 机密工具"}}],"stop_reason":"tool_use","usage":` + claudeResponsesUsage + `}`
			if stream {
				body = claudeResponsesSSE(
					`{"type":"message_start","message":{"usage":`+claudeResponsesUsage+`}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_run","name":"run","input":{"input":"echo 机密工具"}}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
					`{"type":"message_stop"}`,
				)
			}
			rawUsage, apiErr := adaptor.DoResponse(c, claudeResponsesHTTP(body, stream), info)
			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Equal(t, 11, rawUsage.(*dto.Usage).PromptTokens)
			require.NotContains(t, recorder.Body.String(), "机密工具")
		})
	}
}
