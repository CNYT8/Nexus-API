package openai

import (
	"encoding/json"
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
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func runCache12hResponse(t *testing.T, path, body string) (*dto.Usage, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		StartTime:          time.Now(),
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	info.SetEstimatePromptTokens(100)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	switch path {
	case "json":
		usage, apiErr = OaiResponsesHandler(c, info, resp)
	case "sse":
		usage, apiErr = OaiResponsesStreamHandler(c, info, resp)
	case "compact":
		usage, apiErr = OaiResponsesCompactionHandler(c, resp)
	case "chat_json":
		usage, apiErr = OaiResponsesToChatHandler(c, info, resp)
	case "chat_sse":
		usage, apiErr = OaiResponsesToChatStreamHandler(c, info, resp)
	default:
		t.Fatalf("未知测试路径 %q", path)
	}
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return usage, recorder.Body.String()
}

func cache12hStreamBody(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		body.WriteString("data: " + event + "\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

func TestCache12hResponsePaths(t *testing.T) {
	cases := []struct {
		name    string
		field   string
		details string
		want12h int
	}{
		{"input details", "input_tokens_details", `,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}`, 4000},
		{"prompt details fallback", "prompt_tokens_details", `,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}`, 4000},
		{"no ttl", "input_tokens_details", ``, 0},
		{"negative ttl", "input_tokens_details", `,"cache_write_tokens_details":{"ttl_12h_tokens":-1}`, 0},
		{"large ttl", "input_tokens_details", `,"cache_write_tokens_details":{"ttl_12h_tokens":9000}`, 7000},
	}
	for _, path := range []string{"json", "sse", "compact", "chat_json", "chat_sse"} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"id":"resp-test","model":"gpt-4o","status":"completed","unknown_field":true,"output":[],"usage":{"input_tokens":12000,"output_tokens":500,"total_tokens":12500,%q:{"cached_tokens":4000,"cache_write_tokens":5000,"cached_creation_tokens":6000,"cache_creation_input_tokens":7000%s}}}`, tc.field, tc.details)
				event := `{"type":"response.completed","response":` + body + `}`
				input := body
				if path == "sse" || path == "chat_sse" {
					input = cache12hStreamBody(event)
				}
				usage, output := runCache12hResponse(t, path, input)
				require.Equal(t, 12000, usage.PromptTokens)
				require.Equal(t, 500, usage.CompletionTokens)
				require.Equal(t, 12500, usage.TotalTokens)
				require.Equal(t, 4000, usage.PromptTokensDetails.CachedTokens)
				require.Equal(t, 5000, usage.PromptTokensDetails.CacheWriteTokens)
				require.Equal(t, 6000, usage.PromptTokensDetails.CachedCreationTokens)
				require.Equal(t, 7000, usage.PromptTokensDetails.CacheCreationInputTokens)
				require.Equal(t, 7000, usage.PromptTokensDetails.CacheCreationTokensTotal())
				require.Equal(t, tc.want12h, usage.PromptTokensDetails.CacheWrite12hTokens())
				switch path {
				case "json", "compact":
					require.Equal(t, body, output, "响应必须保持原样，包括未知字段和未经限幅的 TTL")
				case "sse":
					require.Contains(t, output, "data: "+event, "流式事件内容必须保持原样")
				case "chat_json":
					var chat dto.OpenAITextResponse
					require.NoError(t, json.Unmarshal([]byte(output), &chat))
					require.Equal(t, usage.PromptTokensDetails, chat.Usage.PromptTokensDetails)
				case "chat_sse":
					foundUsage := false
					for _, line := range strings.Split(output, "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						var chunk struct {
							Usage *dto.Usage `json:"usage"`
						}
						require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk))
						if chunk.Usage != nil && chunk.Usage.TotalTokens > 0 {
							foundUsage = true
							require.Equal(t, usage.PromptTokensDetails, chunk.Usage.PromptTokensDetails)
						}
					}
					require.True(t, foundUsage, "转换流末尾须返回完整用量")
				}
			})
		}
	}
}

func TestCache12hStreamSnapshotMerge(t *testing.T) {
	// Partial snapshots precede the terminal event; response.completed ends
	// scanning and cannot be used as an intermediate usage update.
	first := `{"type":"response.in_progress","response":{"status":"in_progress","usage":{"input_tokens":12000,"output_tokens":500,"total_tokens":12500,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000}}}}}`
	partial := `{"type":"response.in_progress","response":{"status":"in_progress","usage":{"input_tokens_details":{"cache_write_tokens_details":{"ttl_12h_tokens":4000}}}}}`
	zero := `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0,"cache_write_tokens_details":{"ttl_30m_tokens":0,"ttl_12h_tokens":0}}}}}`
	for _, path := range []string{"sse", "chat_sse"} {
		t.Run(path, func(t *testing.T) {
			usage, _ := runCache12hResponse(t, path, cache12hStreamBody(first, partial, partial, zero))
			require.Equal(t, 12000, usage.PromptTokens)
			require.Equal(t, 500, usage.CompletionTokens)
			require.Equal(t, 12500, usage.TotalTokens)
			require.Equal(t, 4000, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 7000, usage.PromptTokensDetails.CacheCreationTokensTotal())
			require.Equal(t, 3000, usage.PromptTokensDetails.CacheWriteTokensDetails.TTL30mTokens)
			require.Equal(t, 4000, usage.PromptTokensDetails.CacheWrite12hTokens())
		})
	}
}

func TestCache12hMissingUsageKeepsFallback(t *testing.T) {
	for _, path := range []string{"json", "sse", "compact", "chat_json", "chat_sse"} {
		t.Run(path, func(t *testing.T) {
			body := `{"id":"resp-test","model":"gpt-4o","status":"completed","output":[]}`
			if path == "sse" || path == "chat_sse" {
				body = cache12hStreamBody(`{"type":"response.completed","response":` + body + `}`)
			}
			usage, _ := runCache12hResponse(t, path, body)
			wantPrompt := 100
			if path == "json" || path == "compact" {
				wantPrompt = 0
			}
			require.Equal(t, wantPrompt, usage.PromptTokens)
			require.Equal(t, 0, usage.CompletionTokens)
			require.Equal(t, wantPrompt, usage.TotalTokens)
			require.Equal(t, 0, usage.PromptTokensDetails.CacheCreationTokensTotal())
			require.Equal(t, 0, usage.PromptTokensDetails.CacheWrite12hTokens())
		})
	}
}
