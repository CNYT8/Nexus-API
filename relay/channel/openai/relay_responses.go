package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	if matcher := relaycommon.NewOutputSensitiveMatcherForConfig(info.OutputSensitiveConfig); !info.ThinkingProcessStrip && matcher != nil {
		cleaned, matched, _, scanErr := relaycommon.SanitizeOutputSensitiveJSONWithThinkingPolicy(responseBody, matcher, info.ThinkingProcessStrip)
		if scanErr != nil {
			return nil, types.NewOpenAIError(scanErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if matched {
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "output_sensitive")
			if info.OutputSensitiveConfig.Action == "error" {
				return nil, types.NewError(relaycommon.OutputSensitiveError(), types.ErrorCodeSensitiveWordsDetected, types.ErrOptionWithSkipRetry())
			}
			responseBody = cleaned
		}
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	usage := dto.Usage{}
	applyResponsesStreamUsage(&usage, responsesResponse.Usage)
	var tools relaycommon.ResponsesToolUsageCounter
	tools.ObserveResponse(c, info, gjson.ParseBytes(responseBody), true)
	usage.ToolUsage = tools.Commit(c, info)
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil {
		return &usage, types.WithOpenAIError(*oaiError, http.StatusBadGateway)
	}
	if responsesStatusIsFailed(responsesResponse.Status) {
		return &usage, types.NewErrorWithStatusCode(fmt.Errorf("upstream Responses request failed"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	// 写入新的 response body
	if writeErr := service.WriteRelayJSON(c, resp, responseBody, info); writeErr != nil {
		return &usage, writeErr
	}
	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer helper.FinalizeThinkingStream(c)
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var itemTextBuilder strings.Builder
	terminalOutputTokens := 0
	var toolCalls relaycommon.ResponsesToolUsageCounter
	started := false
	explicitFailure := false

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		started = true

		// Preserve the distinction between explicit rejection and an empty or
		// interrupted success. Record a soft error even if the scanner already
		// saw [DONE], so empty-response compensation cannot treat failure as success.
		failed := streamResponse.Type == "error" || streamResponse.Type == "response.failed" || streamResponse.Type == "response.error" || streamResponse.Type == "response.cancelled" || streamResponse.Type == "response.canceled"
		if streamResponse.Response != nil {
			failed = failed || responsesStatusIsFailed(streamResponse.Response.Status) || streamResponse.Response.GetOpenAIError() != nil
		}
		if failed && !explicitFailure {
			explicitFailure = true
			sr.Error(fmt.Errorf("upstream Responses request failed"))
		}

		switch streamResponse.Type {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			if streamResponse.Response != nil {
				applyResponsesStreamUsage(usage, streamResponse.Response.Usage)
				// A full terminal snapshot may contain more than the deltas we saw.
				// Compare estimates rather than concatenating duplicate output.
				outputs := gjson.Get(data, "response.output").Array()
				text := responsesOutputTokenText(outputs)
				toolCalls.ObserveResponse(c, info, gjson.Get(data, "response"),
					streamResponse.Type == "response.completed" || streamResponse.Type == "response.done")
				if tokens := service.CountTextToken(text, info.UpstreamModelName); tokens > terminalOutputTokens {
					terminalOutputTokens = tokens
				}
			}
		case "response.output_text.delta", "response.function_call_arguments.delta",
			"response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
			if streamResponse.Type == "response.function_call_arguments.delta" {
				c.Set("responses_tool_call", true)
			}
			// Every delta kind here is generated output that upstream bills as
			// output tokens, so all of them feed the missing-usage estimate.
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			itemTextBuilder.WriteString(responsesOutputTokenText([]gjson.Result{gjson.Get(data, "item")}))
			toolCalls.ObserveItem(c, info, gjson.Get(data, "item"), streamResponse.OutputIndex, true)
		}

		if data != "" {
			if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
				sr.Stop(err)
				return
			}
		}
		switch streamResponse.Type {
		case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled":
			sr.Done()
		}
	})

	usage.ToolUsage = toolCalls.Commit(c, info)

	reportedUsage := usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.TotalTokens > 0
	if usage.TotalTokens > 0 {
		// Some providers send only total_tokens. Do not discard that known usage
		// when rebuilding the total below; recover the absent component first.
		if usage.PromptTokens > 0 && usage.CompletionTokens == 0 && usage.TotalTokens > usage.PromptTokens {
			usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
		} else if usage.PromptTokens == 0 && usage.TotalTokens > usage.CompletionTokens {
			usage.PromptTokens = usage.TotalTokens - usage.CompletionTokens
		}
	}
	if usage.CompletionTokens == 0 {
		for _, text := range []string{responseTextBuilder.String(), itemTextBuilder.String()} {
			if tokens := service.CountTextToken(text, info.UpstreamModelName); tokens > terminalOutputTokens {
				terminalOutputTokens = tokens
			}
		}
		usage.CompletionTokens = terminalOutputTokens
		if terminalOutputTokens > 0 {
			usage.UsageSource = "responses_estimated"
		}
	}

	// No generated or reported usage: this is a rejection, not a billable
	// empty success. Partial output/usage is settled normally and never refunded.
	if explicitFailure && !reportedUsage && usage.CompletionTokens == 0 && !c.GetBool("responses_tool_call") {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("upstream Responses request failed"), types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}

	// Upstream bills the prompt as soon as it starts generating, so a stream
	// that produced any event but no usage still owes its input tokens unless
	// upstream reported an explicit failure.
	billsPrompt := usage.CompletionTokens != 0 || (started && !explicitFailure)
	if info != nil && usage.PromptTokens == 0 && billsPrompt {
		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.UsageSource = "responses_estimated"
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}

func applyResponsesToolUsage(info *relaycommon.RelayInfo, counts map[string]int) {
	if info == nil || info.ResponsesUsageInfo == nil {
		return
	}
	for kind, count := range counts {
		if tool := info.ResponsesUsageInfo.BuiltInTools[kind]; tool != nil {
			tool.CallCount += count
		} else if kind == dto.BuildInToolWebSearchPreview {
			if tool := info.ResponsesUsageInfo.BuiltInTools["web_search"]; tool != nil {
				tool.CallCount += count
			}
		}
	}
}

func responsesBuiltInTool(kind string) string {
	switch kind {
	case dto.BuildInCallWebSearchCall:
		return dto.BuildInToolWebSearchPreview
	case "file_search_call":
		return dto.BuildInToolFileSearch
	}
	return ""
}

// responsesOutputTokenText reads only billable textual output, not IDs, tool
// names, image/base64 payloads or encrypted reasoning. The legacy response DTO
// omits summary/refusal fields, so extract these from the original SSE JSON.
func responsesOutputTokenText(outputs []gjson.Result) string {
	var text strings.Builder
	for _, output := range outputs {
		if output.Get("type").String() == "function_call" {
			text.WriteString(output.Get("arguments").String())
		}
		for _, field := range []string{"content", "summary"} {
			for _, part := range output.Get(field).Array() {
				switch part.Get("type").String() {
				case "output_text", "text", "reasoning_text", "summary_text":
					text.WriteString(part.Get("text").String())
				case "refusal":
					text.WriteString(part.Get("refusal").String())
				}
			}
		}
	}
	return text.String()
}

// applyResponsesStreamUsage merges upstream usage into the accumulated usage.
// Zero values are treated as "not reported" and never overwrite a known value.
func applyResponsesStreamUsage(dst *dto.Usage, src *dto.Usage) {
	if dst == nil || src == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.PromptTokens = src.InputTokens
	} else if src.PromptTokens > 0 {
		dst.PromptTokens = src.PromptTokens
	}
	if src.OutputTokens > 0 {
		dst.CompletionTokens = src.OutputTokens
	} else if src.CompletionTokens > 0 {
		dst.CompletionTokens = src.CompletionTokens
	}
	if src.TotalTokens > 0 {
		dst.TotalTokens = src.TotalTokens
	}
	dst.PromptTokensDetails.MergeNonZero(src.PromptTokensDetails)
	if src.InputTokensDetails != nil {
		dst.PromptTokensDetails.MergeNonZero(*src.InputTokensDetails)
	}
	if src.CompletionTokenDetails.ReasoningTokens > 0 {
		dst.CompletionTokenDetails.ReasoningTokens = src.CompletionTokenDetails.ReasoningTokens
	}
	if src.OutputTokensDetails != nil && src.OutputTokensDetails.ReasoningTokens > 0 {
		dst.CompletionTokenDetails.ReasoningTokens = src.OutputTokensDetails.ReasoningTokens
	}
	dst.InputTokens = dst.PromptTokens
	dst.OutputTokens = dst.CompletionTokens
	dst.TotalTokens = max(dst.TotalTokens, dst.PromptTokens+dst.CompletionTokens)
}

// responsesStatusIsFailed reports whether a Responses API status payload is
// the explicit "failed" terminal state.
func responsesStatusIsFailed(status json.RawMessage) bool {
	if len(status) == 0 {
		return false
	}
	var value string
	if err := common.Unmarshal(status, &value); err != nil {
		return false
	}
	return value == "failed" || value == "cancelled" || value == "canceled"
}

func responsesStatusIsCompleted(status json.RawMessage) bool {
	if len(status) == 0 {
		return false
	}
	var value string
	if err := common.Unmarshal(status, &value); err != nil {
		return false
	}
	return value == "completed"
}
