package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/openaicompat"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func convertResponsesToGemini(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	chat, err := channel.ResponsesBridgeRequest(c, &request)
	if err != nil {
		return nil, err
	}
	if err := channel.ValidateResponsesBridgeMedia(chat); err != nil {
		return nil, err
	}
	for index := range chat.Tools {
		tool := &chat.Tools[index]
		switch tool.Type {
		case "", "function":
		case "web_search", "web_search_preview":
			if len(tool.Extra) != 0 {
				return nil, fmt.Errorf("Gemini web search does not support Responses tool options")
			}
			tool.Type, tool.Function.Name = "function", "googleSearch"
		default:
			return nil, fmt.Errorf("Gemini Responses bridge does not support hosted tool %q", tool.Type)
		}
	}
	return a.ConvertOpenAIRequest(c, info, chat)
}

func geminiResponsesError(c *gin.Context, raw []byte, response *dto.GeminiChatResponse) *types.NewAPIError {
	var envelope dto.SimpleResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return channel.ResponsesBridgeError(err)
	}
	if upstream := envelope.GetOpenAIError(); upstream != nil && upstream.Message != "" {
		return types.WithOpenAIError(*upstream, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if response.PromptFeedback != nil && response.PromptFeedback.BlockReason != nil {
		reason := *response.PromptFeedback.BlockReason
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "gemini_block_reason="+reason)
		return types.NewOpenAIError(fmt.Errorf("request blocked by Gemini API: %s", reason), types.ErrorCodePromptBlocked, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return nil
}

// 每个 part 独立经过 Nexus 原生转换器，避免把同帧正文合并到 reasoning。
func feedGeminiResponses(c *gin.Context, state *openaicompat.ResponsesOutputBridge, response *dto.GeminiChatResponse, callIndices map[string]int) error {
	for _, candidate := range response.Candidates {
		if candidate.Index != 0 {
			return fmt.Errorf("Responses bridge accepts one Gemini candidate")
		}
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil && part.FunctionCall.Arguments == nil {
				part.FunctionCall.Arguments = map[string]any{}
			}
			single := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Role: candidate.Content.Role, Parts: []dto.GeminiPart{part}}}}}
			chat := responseGeminiChat2OpenAI(c, single)
			for _, choice := range chat.Choices {
				text := choice.Message.StringContent()
				if part.Text == "\n" && !part.Thought {
					text = "\n"
				}
				delta := dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text, ReasoningContent: choice.Message.ReasoningContent, Reasoning: choice.Message.Reasoning, Refusal: choice.Message.Refusal, Annotations: choice.Message.Annotations}
				for _, call := range choice.Message.ParseToolCalls() {
					index, exists := callIndices[call.ID]
					if !exists {
						index = len(callIndices)
						callIndices[call.ID] = index
					}
					delta.ToolCalls = append(delta.ToolCalls, dto.ToolCallResponse{Index: &index, ID: call.ID, Type: call.Type, Function: dto.FunctionResponse{Name: call.Function.Name, Arguments: call.Function.Arguments}})
				}
				if err := state.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta}}}); err != nil {
					return err
				}
			}
		}
		if candidate.FinishReason != nil && *candidate.FinishReason != "" {
			terminal := responseGeminiChat2OpenAI(c, &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{FinishReason: candidate.FinishReason}}})
			finish := terminal.Choices[0].FinishReason
			if err := state.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finish}}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func fillGeminiResponsesUsage(c *gin.Context, info *relaycommon.RelayInfo, state *openaicompat.ResponsesOutputBridge, usage *dto.Usage) {
	if usage.CompletionTokens == 0 && state.HasOutput() {
		fallback := service.ResponseText2Usage(c, state.CountedText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		usage.CompletionTokens = fallback.CompletionTokens
	}
	if total := usage.PromptTokens + usage.CompletionTokens; total > usage.TotalTokens {
		usage.TotalTokens = total
	}
}

func GeminiResponsesHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	usage := &dto.Usage{}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	var response dto.GeminiChatResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	*usage = buildUsageFromGeminiMetadata(response.UsageMetadata, info.GetEstimatePromptTokens())
	if apiErr := geminiResponsesError(c, raw, &response); apiErr != nil {
		return usage, apiErr
	}
	if len(response.Candidates) == 0 {
		return usage, types.NewOpenAIError(fmt.Errorf("empty response from Gemini API"), types.ErrorCodeEmptyResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	partialCalls := make(partialFunctionCalls)
	if err := partialCalls.prepare(&response); err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	if len(partialCalls) != 0 {
		return usage, channel.ResponsesBridgeError(fmt.Errorf("Gemini response contains an incomplete function call"))
	}
	state := channel.NewResponsesBridge(c, info, false)
	if err := feedGeminiResponses(c, state, &response, map[string]int{}); err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	fillGeminiResponsesUsage(c, info, state, usage)
	return usage, channel.CompleteResponsesBridge(c, resp, info, state, usage, false)
}

func GeminiResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer helper.FinalizeThinkingStream(c)
	state := channel.NewResponsesBridge(c, info, true)
	partialCalls := make(partialFunctionCalls)
	callIndices := map[string]int{}
	var conversionErr *types.NewAPIError
	finished := false
	usage, streamErr := geminiStreamHandler(c, info, resp, func(data string, response *dto.GeminiChatResponse) bool {
		if conversionErr = geminiResponsesError(c, []byte(data), response); conversionErr != nil {
			return false
		}
		if err := partialCalls.prepare(response); err != nil {
			conversionErr = channel.ResponsesBridgeError(err)
			return false
		}
		for _, candidate := range response.Candidates {
			if candidate.FinishReason != nil && *candidate.FinishReason != "" {
				finished = true
			}
		}
		if err := feedGeminiResponses(c, state, response, callIndices); err != nil {
			conversionErr = channel.ResponsesBridgeError(err)
			return false
		}
		return true
	})
	if usage == nil {
		usage = &dto.Usage{}
	}
	fillGeminiResponsesUsage(c, info, state, usage)
	state.SetUsage(usage)
	if conversionErr != nil {
		streamErr = conversionErr
	}
	if streamErr == nil && len(partialCalls) != 0 {
		streamErr = channel.ResponsesBridgeError(fmt.Errorf("Gemini stream ended with an incomplete function call"))
	}
	if streamErr == nil && !finished {
		streamErr = channel.ResponsesBridgeError(fmt.Errorf("Gemini stream interrupted before finishReason"))
	}
	if streamErr != nil {
		return usage, channel.FailResponsesBridge(c, state, streamErr, true)
	}
	return usage, channel.CompleteResponsesBridge(c, resp, info, state, usage, true)
}
