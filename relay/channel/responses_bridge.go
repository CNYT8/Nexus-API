package channel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/openaicompat"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const ResponsesBridgeErrorWritten = "responses_bridge_error_written"
const responsesBridgeToolsKey = "responses_bridge_tools"

func ResponsesBridgeRequest(c *gin.Context, request *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	chat, tools, err := openaicompat.ResponsesRequestToChat(request)
	if err != nil {
		return nil, err
	}
	c.Set(responsesBridgeToolsKey, tools)
	return chat, nil
}

func NewResponsesBridge(c *gin.Context, info *relaycommon.RelayInfo, stream bool) *openaicompat.ResponsesOutputBridge {
	var tools openaicompat.ResponsesTools
	if value, exists := c.Get(responsesBridgeToolsKey); exists {
		tools, _ = value.(openaicompat.ResponsesTools)
	}
	var emit func(map[string]any) error
	if stream {
		emit = func(event map[string]any) error {
			data, err := json.Marshal(event)
			if err != nil {
				return err
			}
			helper.SetEventStreamHeaders(c)
			return helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event["type"].(string)}, string(data))
		}
	}
	return openaicompat.NewResponsesOutputBridge(helper.GetResponseID(c), info.UpstreamModelName, tools, emit)
}

// 原生转换器对无法表示的 file_id 会跳过，因此在派发前统一拦截。
func ValidateResponsesBridgeMedia(chat *dto.GeneralOpenAIRequest) error {
	for _, message := range chat.Messages {
		for _, part := range message.ParseContent() {
			if part.Type == dto.ContentTypeText {
				continue
			}
			if message.Role == "system" || message.Role == "developer" {
				return fmt.Errorf("system media cannot be represented by this protocol bridge")
			}
			if part.Type == dto.ContentTypeFile {
				file := part.GetFile()
				if file == nil || (file.FileData == "" && file.FileUrl == "") {
					return fmt.Errorf("provider file_id cannot be resolved across protocols; use file_data or file_url")
				}
			}
			if part.ToFileSource() == nil {
				return fmt.Errorf("unsupported or empty media %q", part.Type)
			}
		}
	}
	return nil
}

func ResponsesBridgeError(err error) *types.NewAPIError {
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
}

// 失败前已派发且可能产生用量，禁止外层重新选渠道导致重复计费。
func FailResponsesBridge(c *gin.Context, state *openaicompat.ResponsesOutputBridge, apiErr *types.NewAPIError, stream bool) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	types.ErrOptionWithSkipRetry()(apiErr)
	if stream {
		service.ApplyErrorMask(c, apiErr)
		_ = state.Fail(apiErr.ToOpenAIError())
		// 写出失败/客户端断连同样禁止外层在 SSE 后追加普通 JSON。
		c.Set(ResponsesBridgeErrorWritten, true)
	}
	return apiErr
}

func WriteResponsesBridge(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, value any) *types.NewAPIError {
	data, err := json.Marshal(value)
	if err != nil {
		return ResponsesBridgeError(err)
	}
	if !info.ThinkingProcessStrip {
		matcher := relaycommon.NewOutputSensitiveMatcherForConfig(info.OutputSensitiveConfig)
		cleaned, matched, _, err := relaycommon.SanitizeOutputSensitiveJSONWithThinkingPolicy(data, matcher, false)
		if err != nil {
			return ResponsesBridgeError(err)
		}
		if matched {
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "output_sensitive")
			if info.OutputSensitiveConfig.Action == "error" {
				return types.NewError(relaycommon.OutputSensitiveError(), types.ErrorCodeSensitiveWordsDetected, types.ErrOptionWithSkipRetry())
			}
		}
		data = cleaned
	}
	apiErr := service.WriteRelayJSON(c, resp, data, info)
	if apiErr != nil {
		types.ErrOptionWithSkipRetry()(apiErr)
	}
	return apiErr
}

func CompleteResponsesBridge(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, state *openaicompat.ResponsesOutputBridge, usage *dto.Usage, stream bool) *types.NewAPIError {
	state.SetUsage(usage)
	result, err := state.Complete()
	if err != nil {
		return FailResponsesBridge(c, state, ResponsesBridgeError(err), stream)
	}
	if stream {
		return nil
	}
	return WriteResponsesBridge(c, resp, info, result)
}

// ResponsesViaChatHandler 用于高级自定义的 Responses→Chat 路由。
func ResponsesViaChatHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	state := NewResponsesBridge(c, info, info.IsStream)
	usage := &dto.Usage{}
	if !info.IsStream {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return usage, ResponsesBridgeError(err)
		}
		var response dto.OpenAITextResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return usage, ResponsesBridgeError(err)
		}
		usage = &response.Usage
		if upstream := response.GetOpenAIError(); upstream != nil && upstream.Message != "" {
			return usage, types.WithOpenAIError(*upstream, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		}
		if len(response.Choices) == 0 {
			return usage, ResponsesBridgeError(fmt.Errorf("empty Chat response"))
		}
		if err := state.FeedFull(&response); err != nil {
			return usage, ResponsesBridgeError(err)
		}
		fillResponsesChatUsage(c, info, state, usage)
		return usage, CompleteResponsesBridge(c, resp, info, state, usage, false)
	}
	defer helper.FinalizeThinkingStream(c)
	var streamErr *types.NewAPIError
	finished := false
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var envelope dto.SimpleResponse
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			streamErr = ResponsesBridgeError(err)
			sr.Stop(streamErr)
			return
		}
		mergeResponsesUsage(usage, &envelope.Usage)
		if upstream := envelope.GetOpenAIError(); upstream != nil && upstream.Message != "" {
			streamErr = types.WithOpenAIError(*upstream, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			streamErr = ResponsesBridgeError(err)
			sr.Stop(streamErr)
			return
		}
		if chunk.IsFinished() {
			finished = true
		}
		if err := state.Feed(&chunk); err != nil {
			streamErr = ResponsesBridgeError(err)
			sr.Stop(streamErr)
		}
	})
	fillResponsesChatUsage(c, info, state, usage)
	state.SetUsage(usage)
	if streamErr == nil && (!finished || c.Request.Context().Err() != nil || info.StreamStatus == nil || !info.StreamStatus.IsNormalEnd() || info.StreamStatus.HasErrors()) {
		streamErr = ResponsesBridgeError(fmt.Errorf("Chat stream interrupted before a complete response"))
	}
	if streamErr != nil {
		return usage, FailResponsesBridge(c, state, streamErr, true)
	}
	return usage, CompleteResponsesBridge(c, resp, info, state, usage, true)
}

func fillResponsesChatUsage(c *gin.Context, info *relaycommon.RelayInfo, state *openaicompat.ResponsesOutputBridge, usage *dto.Usage) {
	if usage.PromptTokens == 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if state.HasOutput() && (usage.PromptTokens == 0 || usage.CompletionTokens == 0) {
		fallback := service.ResponseText2Usage(c, state.CountedText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		if usage.PromptTokens == 0 {
			usage.PromptTokens = fallback.PromptTokens
		}
		if usage.CompletionTokens == 0 {
			usage.CompletionTokens = fallback.CompletionTokens
		}
	}
	if total := usage.PromptTokens + usage.CompletionTokens; total > usage.TotalTokens {
		usage.TotalTokens = total
	}
}

func mergeResponsesUsage(dst, src *dto.Usage) {
	if src.PromptTokens > 0 {
		dst.PromptTokens = src.PromptTokens
	}
	if src.CompletionTokens > 0 {
		dst.CompletionTokens = src.CompletionTokens
	}
	if src.TotalTokens > 0 {
		dst.TotalTokens = src.TotalTokens
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.PromptCacheHitTokens > 0 {
		dst.PromptCacheHitTokens = src.PromptCacheHitTokens
	}
	if src.ClaudeCacheCreation5mTokens > 0 {
		dst.ClaudeCacheCreation5mTokens = src.ClaudeCacheCreation5mTokens
	}
	if src.ClaudeCacheCreation1hTokens > 0 {
		dst.ClaudeCacheCreation1hTokens = src.ClaudeCacheCreation1hTokens
	}
	dst.PromptTokensDetails.MergeNonZero(src.PromptTokensDetails)
	mergeResponsesOutputUsage(&dst.CompletionTokenDetails, src.CompletionTokenDetails)
	if src.ToolUsage != nil {
		if dst.ToolUsage == nil {
			dst.ToolUsage = map[string]int{}
		}
		for name, count := range src.ToolUsage {
			if count > 0 {
				dst.ToolUsage[name] = count
			}
		}
	}
	if src.InputTokensDetails != nil {
		if dst.InputTokensDetails == nil {
			dst.InputTokensDetails = &dto.InputTokenDetails{}
		}
		dst.InputTokensDetails.MergeNonZero(*src.InputTokensDetails)
		dst.PromptTokensDetails.MergeNonZero(*src.InputTokensDetails)
	}
	if src.OutputTokensDetails != nil {
		if dst.OutputTokensDetails == nil {
			dst.OutputTokensDetails = &dto.OutputTokenDetails{}
		}
		mergeResponsesOutputUsage(dst.OutputTokensDetails, *src.OutputTokensDetails)
		mergeResponsesOutputUsage(&dst.CompletionTokenDetails, *src.OutputTokensDetails)
	}
	if src.UsageSemantic != "" {
		dst.UsageSemantic = src.UsageSemantic
	}
	if src.UsageSource != "" {
		dst.UsageSource = src.UsageSource
	}
	if src.Cost != nil {
		dst.Cost = src.Cost
	}
}

func mergeResponsesOutputUsage(dst *dto.OutputTokenDetails, src dto.OutputTokenDetails) {
	if src.TextTokens > 0 {
		dst.TextTokens = src.TextTokens
	}
	if src.AudioTokens > 0 {
		dst.AudioTokens = src.AudioTokens
	}
	if src.ImageTokens > 0 {
		dst.ImageTokens = src.ImageTokens
	}
	if src.ReasoningTokens > 0 {
		dst.ReasoningTokens = src.ReasoningTokens
	}
}
