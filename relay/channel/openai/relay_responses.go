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
	if matcher := relaycommon.NewOutputSensitiveMatcherForConfig(info.OutputSensitiveConfig); matcher != nil {
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
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
		}
	}
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return &usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
	}
	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var itemTextBuilder strings.Builder
	terminalOutputTokens := 0
	toolCounts := map[string]int{}
	terminalToolCounts := map[string]int{}
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
		sendResponsesStreamData(c, streamResponse, data)

		// Preserve the distinction between explicit rejection and an empty or
		// interrupted success. Record a soft error even if the scanner already
		// saw [DONE], so empty-response compensation cannot treat failure as success.
		failed := streamResponse.Type == "error" || streamResponse.Type == "response.failed" || streamResponse.Type == "response.error"
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
				counts := map[string]int{}
				for _, output := range outputs {
					kind := output.Get("type").String()
					if strings.HasSuffix(kind, "_call") {
						c.Set("responses_tool_call", true)
					}
					counts[responsesBuiltInTool(kind)]++
				}
				for kind, count := range counts {
					if count > terminalToolCounts[kind] {
						terminalToolCounts[kind] = count
					}
				}
				if tokens := service.CountTextToken(text, info.UpstreamModelName); tokens > terminalOutputTokens {
					terminalOutputTokens = tokens
				}
				if streamResponse.Response.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
					c.Set("image_generation_call_size", streamResponse.Response.GetSize())
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
			// 函数调用处理
			if streamResponse.Item != nil {
				kind := streamResponse.Item.Type
				if strings.HasSuffix(kind, "_call") {
					c.Set("responses_tool_call", true)
				}
				toolCounts[responsesBuiltInTool(kind)]++
			}
		}
	})

	for kind, count := range terminalToolCounts {
		if count > toolCounts[kind] {
			toolCounts[kind] = count
		}
	}
	if info.ResponsesUsageInfo != nil {
		for kind, count := range toolCounts {
			if kind != "" {
				if tool := info.ResponsesUsageInfo.BuiltInTools[kind]; tool != nil {
					tool.CallCount += count
				}
			}
		}
	}

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
	if src.InputTokensDetails != nil && src.InputTokensDetails.CachedTokens > 0 {
		dst.PromptTokensDetails.CachedTokens = src.InputTokensDetails.CachedTokens
	}
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
	return value == "failed"
}
