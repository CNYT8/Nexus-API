package claude

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProtocolSyncGeminiToClaudePreservesToolsLimitsAndVisibleText(t *testing.T) {
	var request dto.GeminiChatRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"systemInstruction":{"parts":[{"text":"system policy"}]},
		"contents":[
			{"role":"user","parts":[{"text":"calculate"}]},
			{"role":"model","parts":[{"text":"private thought","thought":true},{"text":"working"},{"functionCall":{"id":"calc_1","name":"calculate","args":{"n":2}}}]},
			{"role":"user","parts":[{"functionResponse":{"id":"calc_1","name":"calculate","response":{"result":4}}}]}
		],
		"generationConfig":{"temperature":0,"topP":0,"topK":0,"maxOutputTokens":0,"stopSequences":["one","two"]},
		"tools":[{"functionDeclarations":[{"name":"calculate","description":"compute a square","parameters":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}]}]
	}`), &request))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/alias:streamGenerateContent", nil)
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4-6"}}
	converted, err := (&Adaptor{}).ConvertGeminiRequest(c, info, &request)
	require.NoError(t, err)
	claudeRequest, ok := converted.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.Equal(t, "claude-sonnet-4-6", claudeRequest.Model)
	require.NotNil(t, claudeRequest.Stream)
	require.True(t, *claudeRequest.Stream)
	require.NotNil(t, claudeRequest.MaxTokens)
	require.Zero(t, *claudeRequest.MaxTokens)
	require.NotNil(t, claudeRequest.Temperature)
	require.Zero(t, *claudeRequest.Temperature)
	require.NotNil(t, claudeRequest.TopP)
	require.Zero(t, *claudeRequest.TopP)
	require.NotNil(t, claudeRequest.TopK)
	require.Zero(t, *claudeRequest.TopK)
	require.Equal(t, []string{"one", "two"}, claudeRequest.StopSequences)
	system, err := common.Marshal(claudeRequest.System)
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"text","text":"system policy"}]`, string(system))
	tools, err := common.Marshal(claudeRequest.Tools)
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"calculate","description":"compute a square","input_schema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}]`, string(tools))
	require.Len(t, claudeRequest.Messages, 3)
	require.Equal(t, "calculate", claudeRequest.Messages[0].Content)
	assistant, err := claudeRequest.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, assistant, 2)
	require.Equal(t, "working", assistant[0].GetText())
	require.Equal(t, "tool_use", assistant[1].Type)
	require.Equal(t, "calc_1", assistant[1].Id)
	require.Equal(t, "calculate", assistant[1].Name)
	arguments, err := common.Marshal(assistant[1].Input)
	require.NoError(t, err)
	require.JSONEq(t, `{"n":2}`, string(arguments))
	result, err := claudeRequest.Messages[2].ParseContent()
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "tool_result", result[0].Type)
	require.Equal(t, "calc_1", result[0].ToolUseId)
	require.JSONEq(t, `{"result":4}`, result[0].Content.(string))
	encoded, err := common.Marshal(claudeRequest)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private thought", "Gemini thought history must not become visible Claude text")
}

func TestProtocolSyncGeminiToClaudeInlineMediaAndErrors(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/alias:generateContent", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4-6"}}
	adaptor := &Adaptor{}
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMB/axnsUQAAAAASUVORK5CYII="
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n%%EOF\n"))
	request := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{
		{Text: "inspect"},
		{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png}},
		{InlineData: &dto.GeminiInlineData{MimeType: "application/pdf", Data: pdf}},
		{InlineData: &dto.GeminiInlineData{MimeType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte("note"))}},
	}}}}
	converted, err := adaptor.ConvertGeminiRequest(c, info, request)
	require.NoError(t, err)
	claudeRequest, ok := converted.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.Len(t, claudeRequest.Messages, 1)
	parts, err := claudeRequest.Messages[0].ParseContent()
	require.NoError(t, err)
	require.Len(t, parts, 4)
	require.Equal(t, "inspect", parts[0].GetText())
	require.Equal(t, "image", parts[1].Type)
	require.Equal(t, "image/png", parts[1].Source.MediaType)
	require.Equal(t, png, parts[1].Source.Data)
	require.Equal(t, "document", parts[2].Type)
	require.Equal(t, "application/pdf", parts[2].Source.MediaType)
	require.Equal(t, pdf, parts[2].Source.Data)
	require.Equal(t, "note", parts[3].GetText())

	request.Contents[0].Parts = []dto.GeminiPart{{InlineData: &dto.GeminiInlineData{MimeType: "video/mp4", Data: "YWJj"}}}
	_, err = adaptor.ConvertGeminiRequest(c, info, request)
	require.ErrorContains(t, err, "contents[0].parts[0].inlineData")
	request.Contents[0].Parts = []dto.GeminiPart{{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "UklGRg=="}}}
	_, err = adaptor.ConvertGeminiRequest(c, info, request)
	require.ErrorContains(t, err, "Claude Messages cannot carry content of MIME type")
	_, err = adaptor.ConvertGeminiRequest(c, info, nil)
	require.ErrorContains(t, err, "request is nil")
}

func TestProtocolSyncClaudeNativeExtensionsSurviveAdaptor(t *testing.T) {
	const input = `{"model":"claude-sonnet-4-6","max_tokens":0,"messages":[{"role":"user","content":"hello","output_config":{"effort":"low"}}],"chat_template_kwargs":{"enabled":false},"output_config":{"effort":"high"}}`
	var request dto.ClaudeRequest
	require.NoError(t, json.Unmarshal([]byte(input), &request))
	converted, err := (&Adaptor{}).ConvertClaudeRequest(nil, nil, &request)
	require.NoError(t, err)
	require.Same(t, &request, converted)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))
}
