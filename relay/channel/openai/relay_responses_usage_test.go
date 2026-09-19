package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func runOaiResponsesStreamUsageTest(t *testing.T, events ...string) (*dto.Usage, *types.NewAPIError, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = previousTimeout
	})

	var body strings.Builder
	for _, event := range events {
		body.WriteString("event: response\n")
		body.WriteString("data: ")
		body.WriteString(event)
		body.WriteString("\n\n")
	}
	body.WriteString("data: [DONE]\n\n")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
			dto.BuildInToolWebSearchPreview: {},
			dto.BuildInToolFileSearch:       {},
		}},
		StartTime: time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}
	info.SetEstimatePromptTokens(100)

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body.String()))}
	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
	if c.GetBool("responses_tool_call") {
		other := service.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 0)
		require.Equal(t, true, other["tool_calls"], "tool-only responses must be excluded from empty-response refunds")
	}
	return usage, apiErr, info
}

func TestOaiResponsesStreamHandlerMissingUsageEstimation(t *testing.T) {
	const model = "gpt-4o"
	const summary = "Inspect the repository before editing."
	const arguments = `{"command":["bash","-lc","ls"]}`
	const refusal = "I cannot help with that."
	const terminalText = "final answer"

	created := `{"type":"response.created","response":{"status":"in_progress"}}`

	tests := []struct {
		name           string
		events         []string
		wantPrompt     int
		wantCompletion int
		wantFailure    bool
	}{
		{
			name: "tool call arguments delta feeds the estimate",
			events: []string{
				created,
				fmt.Sprintf(`{"type":"response.function_call_arguments.delta","delta":%q}`, arguments),
			},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken(arguments, model),
		},
		{
			name: "reasoning deltas feed the estimate",
			events: []string{
				created,
				fmt.Sprintf(`{"type":"response.reasoning_summary_text.delta","delta":%q}`, summary),
				fmt.Sprintf(`{"type":"response.reasoning_text.delta","delta":%q}`, terminalText),
			},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken(summary+terminalText, model),
		},
		{
			name: "refusal delta feeds the estimate",
			events: []string{
				created,
				fmt.Sprintf(`{"type":"response.refusal.delta","delta":%q}`, refusal),
			},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken(refusal, model),
		},
		{
			name: "created only then disconnect bills the prompt",
			events: []string{
				created,
			},
			wantPrompt: 100,
		},
		{
			name: "incomplete without usage bills the prompt",
			events: []string{
				created,
				`{"type":"response.incomplete","response":{"status":"incomplete"}}`,
			},
			wantPrompt: 100,
		},
		{
			name: "completed without usage estimates from terminal output",
			events: []string{
				`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer"}]}]}}`,
			},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken(terminalText, model),
		},
		{
			name:        "response.failed status blocks prompt estimation",
			wantFailure: true,
			events: []string{
				created,
				`{"type":"response.failed","response":{"status":"failed"}}`,
			},
		},
		{
			name:        "flat error event blocks prompt estimation",
			wantFailure: true,
			events: []string{
				created,
				`{"type":"error","message":"upstream failure"}`,
			},
		},
		{
			name:           "terminal arguments reasoning and refusal",
			events:         []string{`{"type":"response.done","response":{"output":[{"type":"function_call","arguments":"args"},{"type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]},{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}}`},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken("argsreasonno", model),
		},
		{
			name:           "item done without deltas",
			events:         []string{`{"type":"response.output_item.done","item":{"type":"function_call","arguments":"args"}}`},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken("args", model),
		},
		{
			name:           "terminal snapshot not counted twice",
			events:         []string{`{"type":"response.output_text.delta","delta":"final answer"}`, `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"final answer"}]}]}}`},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken("final answer", model),
		},
		{
			name:           "explicit failure after output retains usage",
			events:         []string{created, `{"type":"response.output_text.delta","delta":"final answer"}`, `{"type":"response.failed","response":{"status":"failed"}}`},
			wantPrompt:     100,
			wantCompletion: service.CountTextToken("final answer", model),
		},
		{
			name:        "failed status under done event",
			events:      []string{`{"type":"response.done","response":{"status":"failed"}}`},
			wantFailure: true,
		},
		{
			name:       "total-only usage is not discarded",
			events:     []string{`{"type":"response.completed","response":{"usage":{"total_tokens":200}}}`},
			wantPrompt: 200,
		},
		{
			name:           "total and input recover output",
			events:         []string{`{"type":"response.completed","response":{"usage":{"input_tokens":100,"total_tokens":200}}}`},
			wantPrompt:     100,
			wantCompletion: 100,
		},
		{
			name: "no upstream events bills nothing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			usage, apiErr, info := runOaiResponsesStreamUsageTest(t, tc.events...)
			if tc.wantFailure {
				require.NotNil(t, apiErr)
				require.Nil(t, usage)
				require.True(t, info.StreamStatus.HasErrors())
				return
			}
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			require.Equal(t, tc.wantPrompt, usage.PromptTokens)
			require.Equal(t, tc.wantCompletion, usage.CompletionTokens)
			require.Equal(t, tc.wantPrompt+tc.wantCompletion, usage.TotalTokens)
			if strings.Contains(tc.name, "explicit failure after") {
				require.True(t, info.StreamStatus.HasErrors())
				info.IsStream = true
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				other := service.GenerateTextOtherInfo(ctx, info, 1, 1, 1, 0, 1, 0, 0)
				require.Equal(t, "error", other["stream_status"].(map[string]interface{})["status"], "failed streams must be excluded by empty-response compensation")
			}
		})
	}
}

func TestResponsesTerminalToolsDoNotDoubleBill(t *testing.T) {
	for _, itemDone := range []bool{false, true} {
		events := []string{}
		if itemDone {
			events = append(events, `{"type":"response.output_item.done","item":{"type":"web_search_call"}}`, `{"type":"response.output_item.done","item":{"type":"file_search_call"}}`)
		}
		events = append(events, `{"type":"response.completed","response":{"output":[{"type":"web_search_call"},{"type":"file_search_call"}]}}`)
		_, apiErr, info := runOaiResponsesStreamUsageTest(t, events...)
		require.Nil(t, apiErr)
		require.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount)
		require.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch].CallCount)
	}
}

func TestOaiResponsesStreamHandlerNonZeroUsageWins(t *testing.T) {
	terminalWithUsage := `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":200,"output_tokens":50,"total_tokens":250}}}`
	usage, apiErr, _ := runOaiResponsesStreamUsageTest(t, terminalWithUsage)
	require.Nil(t, apiErr)
	require.Equal(t, 200, usage.PromptTokens)
	require.Equal(t, 50, usage.CompletionTokens)
	require.Equal(t, 250, usage.TotalTokens)

	created := `{"type":"response.created","response":{"status":"in_progress"}}`
	partialUsage := `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":200}}}`
	usage, apiErr, _ = runOaiResponsesStreamUsageTest(t, created, partialUsage)
	require.Nil(t, apiErr)
	require.Equal(t, 200, usage.PromptTokens)
	require.Equal(t, 0, usage.CompletionTokens)
	require.Equal(t, 200, usage.TotalTokens)
}
