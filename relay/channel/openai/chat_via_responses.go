package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/openaicompat"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func responsesStreamIndexKey(itemID string, idx *int) string {
	if itemID == "" {
		return ""
	}
	if idx == nil {
		return itemID
	}
	return fmt.Sprintf("%s:%d", itemID, *idx)
}

func stringDeltaFromPrefix(prev string, next string) string {
	if next == "" {
		return ""
	}
	if prev != "" && strings.HasPrefix(next, prev) {
		return next[len(prev):]
	}
	return next
}

func OaiResponsesToChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var responsesResp dto.OpenAIResponsesResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	if err := common.Unmarshal(body, &responsesResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	upstreamUsage := &dto.Usage{}
	applyResponsesStreamUsage(upstreamUsage, responsesResp.Usage)
	var tools dto.ToolUsageCounter
	for i, output := range responsesResp.Output {
		tools.Record(responsesBuiltInTool(output.Type), output.ID, &i)
	}
	upstreamUsage.ToolUsage = tools.Counts()
	applyResponsesToolUsage(info, upstreamUsage.ToolUsage)
	if oaiError := responsesResp.GetOpenAIError(); oaiError != nil {
		return upstreamUsage, types.WithOpenAIError(*oaiError, http.StatusBadGateway)
	}
	if responsesStatusIsFailed(responsesResp.Status) {
		return upstreamUsage, types.NewOpenAIError(fmt.Errorf("Responses request failed"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	chatId := helper.GetResponseID(c)
	chatResp, usage, err := service.ResponsesResponseToChatCompletionsResponse(&responsesResp, chatId)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(&responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		chatResp.Usage = *usage
	}

	usage.ToolUsage = upstreamUsage.ToolUsage
	chatResp.Usage = *usage
	var responseBody []byte
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		claudeResp := service.ResponseOpenAI2Claude(chatResp, info)
		responseBody, err = common.Marshal(claudeResp)
	case types.RelayFormatGemini:
		geminiResp := service.ResponseOpenAI2Gemini(chatResp, info)
		responseBody, err = common.Marshal(geminiResp)
	default:
		responseBody, err = common.Marshal(chatResp)
	}
	if err != nil {
		return usage, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	if writeErr := service.WriteRelayJSON(c, resp, responseBody, info); writeErr != nil {
		return usage, writeErr
	}
	return usage, nil
}

func OaiResponsesToChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer helper.FinalizeThinkingStream(c)
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createAt := time.Now().Unix()
	model := info.UpstreamModelName

	var (
		usage        = &dto.Usage{}
		outputText   strings.Builder
		finishReason = "stop"
		usageText    strings.Builder
		sentStart    bool
		sentStop     bool
		sawToolCall  bool
		streamErr    *types.NewAPIError
		tools        dto.ToolUsageCounter
	)
	defer func() {
		usage.ToolUsage = tools.Counts()
		applyResponsesToolUsage(info, usage.ToolUsage)
	}()

	toolCallIndexByID := make(map[string]int)
	toolCallNameByID := make(map[string]string)
	toolCallArgsByID := make(map[string]string)
	toolCallNameSent := make(map[string]bool)
	toolCallCanonicalIDByItemID := make(map[string]string)
	annotationSeen := make(map[string]bool)
	textByKey := make(map[string]string)
	reasoningByKey := make(map[string]string)
	outputKeyByID := make(map[string]string)
	refusalByKey := make(map[string]string)
	textOffsetByKey := make(map[string]int)
	outputKey := func(id string, index *int) string {
		if key := outputKeyByID[id]; id != "" && key != "" {
			return key
		}
		key := id
		if index != nil {
			key = fmt.Sprintf("output:%d", *index)
		}
		if id != "" {
			outputKeyByID[id] = key
		}
		return key
	}
	contentKey := func(id string, outputIndex, contentIndex *int) string {
		index := 0
		if contentIndex != nil {
			index = *contentIndex
		}
		return fmt.Sprintf("%s:%d", outputKey(id, outputIndex), index)
	}
	lastReasoningKey := ""
	reasoningKey := func(id string, outputIndex, partIndex *int, summary bool) string {
		kind := "reasoning:"
		if summary {
			kind = "summary:"
		}
		return kind + contentKey(id, outputIndex, partIndex)
	}

	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}
	}

	sendChatChunk := func(chunk *dto.ChatCompletionsStreamResponse) bool {
		if chunk == nil {
			return true
		}
		if info.RelayFormat == types.RelayFormatOpenAI {
			if err := helper.ObjectData(c, chunk); err != nil {
				streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
				return false
			}
			return true
		}

		chunkData, err := common.Marshal(chunk)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := handleThinkingStreamFormat(c, info, string(chunkData), false, false); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		return true
	}

	sendStartIfNeeded := func() bool {
		if sentStart {
			return true
		}
		if !sendChatChunk(helper.GenerateStartEmptyResponse(responseId, createAt, model, nil)) {
			return false
		}
		sentStart = true
		return true
	}

	sendReasoningSummaryDelta := func(key, delta string) bool {
		if delta == "" {
			return true
		}
		if lastReasoningKey != "" && lastReasoningKey != key {
			delta = "\n\n" + delta
		}
		lastReasoningKey = key
		if !sendStartIfNeeded() {
			return false
		}

		usageText.WriteString(delta)
		chunk := &dto.ChatCompletionsStreamResponse{
			Id:      responseId,
			Object:  "chat.completion.chunk",
			Created: createAt,
			Model:   model,
			Choices: []dto.ChatCompletionsStreamResponseChoice{
				{
					Index: 0,
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
						ReasoningContent: &delta,
					},
				},
			},
		}
		if !sendChatChunk(chunk) {
			return false
		}
		return true
	}

	sendToolCallDelta := func(callID string, name string, argsDelta string) bool {
		if callID == "" {
			return true
		}
		// Responses providers may emit the same function call once when it is
		// added and again when it is completed. The completed event carries no
		// new data after argument deltas have already been forwarded.
		if argsDelta == "" && toolCallNameSent[callID] &&
			(name == "" || name == toolCallNameByID[callID]) {
			return true
		}
		if !sendStartIfNeeded() {
			return false
		}

		idx, ok := toolCallIndexByID[callID]
		if !ok {
			idx = len(toolCallIndexByID)
			toolCallIndexByID[callID] = idx
		}
		if name != "" {
			toolCallNameByID[callID] = name
		}
		if toolCallNameByID[callID] != "" {
			name = toolCallNameByID[callID]
		}

		tool := dto.ToolCallResponse{
			ID:   callID,
			Type: "function",
			Function: dto.FunctionResponse{
				Arguments: argsDelta,
			},
		}
		tool.SetIndex(idx)
		if name != "" && !toolCallNameSent[callID] {
			tool.Function.Name = name
			toolCallNameSent[callID] = true
		}

		chunk := &dto.ChatCompletionsStreamResponse{
			Id:      responseId,
			Object:  "chat.completion.chunk",
			Created: createAt,
			Model:   model,
			Choices: []dto.ChatCompletionsStreamResponseChoice{
				{
					Index: 0,
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
						ToolCalls: []dto.ToolCallResponse{tool},
					},
				},
			},
		}
		if !sendChatChunk(chunk) {
			return false
		}
		sawToolCall = true

		// Include tool call data in the local builder for fallback token estimation.
		if tool.Function.Name != "" {
			usageText.WriteString(tool.Function.Name)
		}
		if argsDelta != "" {
			usageText.WriteString(argsDelta)
		}
		return true
	}

	// Replay only the unseen suffix of full item/terminal snapshots. Providers
	// may send only a terminal response, or repeat all previous deltas there.
	replayOutput := func(item *dto.ResponsesOutput, index *int) bool {
		if item == nil {
			return true
		}
		tools.Record(responsesBuiltInTool(item.Type), item.ID, index)
		key := outputKey(item.ID, index)
		if item.Type == "function_call" {
			callID := item.CallId
			if callID == "" {
				callID = item.ID
			}
			if callID == "" {
				callID = key
			}
			toolCallCanonicalIDByItemID[item.ID] = callID
			next := item.ArgumentsString()
			delta := stringDeltaFromPrefix(toolCallArgsByID[callID], next)
			if next != "" {
				toolCallArgsByID[callID] = next
			}
			return sendToolCallDelta(callID, item.Name, delta)
		}
		if item.Type == "reasoning" {
			parts := item.Summary
			isSummary := len(parts) > 0
			if !isSummary {
				for _, part := range item.Content {
					parts = append(parts, dto.ResponsesReasoningSummaryPart{Type: part.Type, Text: part.Text})
				}
			}
			for i, part := range parts {
				partKey := reasoningKey(item.ID, index, &i, isSummary)
				delta := stringDeltaFromPrefix(reasoningByKey[partKey], part.Text)
				if part.Text != "" {
					reasoningByKey[partKey] = part.Text
				}
				if !sendReasoningSummaryDelta(partKey, delta) {
					return false
				}
			}
			return true
		}
		if item.Type != "message" {
			return true
		}
		chunk := helper.GenerateStartEmptyResponse(responseId, createAt, model, nil)
		chunk.Choices[0].Delta = dto.ChatCompletionsStreamResponseChoiceDelta{}
		var textDelta, refusalDelta strings.Builder
		var annotations []any
		for i, part := range item.Content {
			partKey := contentKey(item.ID, index, &i)
			if _, ok := textOffsetByKey[partKey]; !ok {
				textOffsetByKey[partKey] = utf8.RuneCountInString(outputText.String())
			}
			if part.Type == "output_text" {
				delta := stringDeltaFromPrefix(textByKey[partKey], part.Text)
				if part.Text != "" {
					textByKey[partKey] = part.Text
				}
				textDelta.WriteString(delta)
				outputText.WriteString(delta)
				usageText.WriteString(delta)
			}
			for _, annotation := range part.Annotations {
				raw, _ := common.Marshal(annotation)
				annotationKey := partKey + string(raw)
				if !annotationSeen[annotationKey] {
					annotationSeen[annotationKey] = true
					annotations = append(annotations, openaicompat.ResponsesAnnotationsToChat([]any{annotation}, textOffsetByKey[partKey])...)
				}
			}
			if part.Refusal != "" {
				delta := stringDeltaFromPrefix(refusalByKey[partKey], part.Refusal)
				refusalByKey[partKey] = part.Refusal
				refusalDelta.WriteString(delta)
				usageText.WriteString(delta)
			}
		}
		if textDelta.Len() > 0 {
			value := textDelta.String()
			chunk.Choices[0].Delta.Content = &value
		}
		if refusalDelta.Len() > 0 {
			value := refusalDelta.String()
			chunk.Choices[0].Delta.Refusal = &value
		}
		if len(annotations) > 0 {
			chunk.Choices[0].Delta.Annotations, _ = common.Marshal(annotations)
		}
		if textDelta.Len() == 0 && len(annotations) == 0 && chunk.Choices[0].Delta.Refusal == nil {
			return true
		}
		return sendStartIfNeeded() && sendChatChunk(chunk)
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil || sentStop {
			sr.Stop(streamErr)
			return
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, "failed to unmarshal responses stream event: "+err.Error())
			sr.Error(err)
			return
		}

		if streamResp.Response != nil {
			applyResponsesStreamUsage(usage, streamResp.Response.Usage)
			for i, output := range streamResp.Response.Output {
				tools.Record(responsesBuiltInTool(output.Type), output.ID, &i)
			}
		}
		failed := streamResp.Type == "error" || streamResp.Type == "response.error" || streamResp.Type == "response.failed" || streamResp.Type == "response.cancelled" || streamResp.Type == "response.canceled"
		if streamResp.Response != nil {
			failed = failed || responsesStatusIsFailed(streamResp.Response.Status) || streamResp.Response.GetOpenAIError() != nil
		}
		if failed {
			oaiErr := dto.GetOpenAIError(streamResp.Error)
			if streamResp.Response != nil && streamResp.Response.GetOpenAIError() != nil {
				oaiErr = streamResp.Response.GetOpenAIError()
			}
			if oaiErr == nil {
				oaiErr = &types.OpenAIError{Type: "upstream_error", Message: "Responses stream failed: " + streamResp.Type + " " + streamResp.Message, Code: streamResp.Code}
			}
			streamErr = types.WithOpenAIError(*oaiErr, http.StatusBadGateway)
			sr.Error(streamErr)
			sr.Stop(streamErr)
			return
		}

		switch streamResp.Type {
		case "response.created":
			if streamResp.Response != nil {
				if streamResp.Response.Model != "" {
					model = streamResp.Response.Model
				}
				if streamResp.Response.CreatedAt != 0 {
					createAt = int64(streamResp.Response.CreatedAt)
				}
			}

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			isSummary := streamResp.Type == "response.reasoning_summary_text.delta"
			partIndex := streamResp.ContentIndex
			if isSummary {
				partIndex = streamResp.SummaryIndex
			}
			key := reasoningKey(streamResp.ItemID, streamResp.OutputIndex, partIndex, isSummary)
			reasoningByKey[key] += streamResp.Delta
			if !sendReasoningSummaryDelta(key, streamResp.Delta) {
				sr.Stop(streamErr)
				return
			}

		case "response.reasoning_summary_text.done", "response.reasoning_text.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
			isSummary := streamResp.Type != "response.reasoning_text.done"
			partIndex := streamResp.ContentIndex
			if isSummary {
				partIndex = streamResp.SummaryIndex
			}
			key := reasoningKey(streamResp.ItemID, streamResp.OutputIndex, partIndex, isSummary)
			next := streamResp.Text
			if streamResp.Part != nil {
				next = streamResp.Part.Text
			}
			delta := stringDeltaFromPrefix(reasoningByKey[key], next)
			if next != "" {
				reasoningByKey[key] = next
			}
			if !sendReasoningSummaryDelta(key, delta) {
				sr.Stop(streamErr)
				return
			}

		case "response.output_text.delta":
			if !sendStartIfNeeded() {
				sr.Stop(streamErr)
				return
			}

			if streamResp.Delta != "" {
				key := contentKey(streamResp.ItemID, streamResp.OutputIndex, streamResp.ContentIndex)
				if _, ok := textOffsetByKey[key]; !ok {
					textOffsetByKey[key] = utf8.RuneCountInString(outputText.String())
				}
				textByKey[key] += streamResp.Delta
				outputText.WriteString(streamResp.Delta)
				usageText.WriteString(streamResp.Delta)
				delta := streamResp.Delta
				chunk := &dto.ChatCompletionsStreamResponse{
					Id:      responseId,
					Object:  "chat.completion.chunk",
					Created: createAt,
					Model:   model,
					Choices: []dto.ChatCompletionsStreamResponseChoice{
						{
							Index: 0,
							Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
								Content: &delta,
							},
						},
					},
				}
				if !sendChatChunk(chunk) {
					sr.Stop(streamErr)
					return
				}
			}

		case "response.output_item.added", "response.output_item.done":
			if !replayOutput(streamResp.Item, streamResp.OutputIndex) {
				sr.Stop(streamErr)
				return
			}

		case "response.function_call_arguments.delta":
			itemID := strings.TrimSpace(streamResp.ItemID)
			callID := toolCallCanonicalIDByItemID[itemID]
			if callID == "" {
				callID = itemID
			}
			if callID == "" {
				break
			}
			toolCallArgsByID[callID] += streamResp.Delta
			if !sendToolCallDelta(callID, "", streamResp.Delta) {
				sr.Stop(streamErr)
				return
			}

		case "response.function_call_arguments.done":
			callID := toolCallCanonicalIDByItemID[streamResp.ItemID]
			if callID == "" {
				callID = streamResp.ItemID
			}
			next := dto.ResponsesArgumentsString(streamResp.Arguments)
			delta := stringDeltaFromPrefix(toolCallArgsByID[callID], next)
			if next != "" {
				toolCallArgsByID[callID] = next
			}
			if !sendToolCallDelta(callID, "", delta) {
				sr.Stop(streamErr)
				return
			}

		case "response.refusal.delta":
			key := contentKey(streamResp.ItemID, streamResp.OutputIndex, streamResp.ContentIndex)
			refusalByKey[key] += streamResp.Delta
			usageText.WriteString(streamResp.Delta)
			chunk := helper.GenerateStartEmptyResponse(responseId, createAt, model, nil)
			chunk.Choices[0].Delta = dto.ChatCompletionsStreamResponseChoiceDelta{Refusal: &streamResp.Delta}
			if !sendStartIfNeeded() || !sendChatChunk(chunk) {
				sr.Stop(streamErr)
				return
			}

		case "response.output_text.annotation.added":
			var annotation any
			if len(streamResp.Annotation) > 0 && common.Unmarshal(streamResp.Annotation, &annotation) == nil {
				key := contentKey(streamResp.ItemID, streamResp.OutputIndex, streamResp.ContentIndex)
				raw, _ := common.Marshal(annotation)
				annotationKey := key + string(raw)
				if annotationSeen[annotationKey] {
					break
				}
				annotationSeen[annotationKey] = true
				annotations, _ := common.Marshal(openaicompat.ResponsesAnnotationsToChat([]any{annotation}, textOffsetByKey[key]))
				chunk := helper.GenerateStartEmptyResponse(responseId, createAt, model, nil)
				chunk.Choices[0].Delta = dto.ChatCompletionsStreamResponseChoiceDelta{Annotations: annotations}
				if !sendStartIfNeeded() || !sendChatChunk(chunk) {
					sr.Stop(streamErr)
					return
				}
			}

		case "response.completed", "response.done", "response.incomplete":
			if streamResp.Response != nil {
				for i := range streamResp.Response.Output {
					if !replayOutput(&streamResp.Response.Output[i], &i) {
						sr.Stop(streamErr)
						return
					}
				}
				if streamResp.Response.Model != "" {
					model = streamResp.Response.Model
				}
				if streamResp.Response.CreatedAt != 0 {
					createAt = int64(streamResp.Response.CreatedAt)
				}
				if streamResp.Response.Usage != nil {
					if streamResp.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResp.Response.Usage.InputTokens
						usage.InputTokens = streamResp.Response.Usage.InputTokens
					}
					if streamResp.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResp.Response.Usage.OutputTokens
						usage.OutputTokens = streamResp.Response.Usage.OutputTokens
					}
					if streamResp.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResp.Response.Usage.TotalTokens
					} else {
						usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
					}
					usage.PromptTokensDetails.MergeNonZero(streamResp.Response.Usage.PromptTokensDetails)
					if streamResp.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.MergeNonZero(*streamResp.Response.Usage.InputTokensDetails)
					}
					if streamResp.Response.Usage.CompletionTokenDetails.ReasoningTokens != 0 {
						usage.CompletionTokenDetails.ReasoningTokens = streamResp.Response.Usage.CompletionTokenDetails.ReasoningTokens
					}
				}
			}

			if !sendStartIfNeeded() {
				sr.Stop(streamErr)
				return
			}
			if !sentStop {
				if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil {
					info.ClaudeConvertInfo.Usage = usage
				}
				finishReason = openaicompat.ResponsesFinishReason(streamResp.Response, sawToolCall)
				stop := helper.GenerateStopResponse(responseId, createAt, model, finishReason)
				if info.RelayFormat == types.RelayFormatClaude {
					stop.Usage = usage
				}
				if !sendChatChunk(stop) {
					sr.Stop(streamErr)
					return
				}
				sentStop = true
			}
			sr.Done()

		case "response.error", "response.failed":
			if streamResp.Response != nil {
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
					sr.Stop(streamErr)
					return
				}
			}
			streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return

		default:
		}
	})

	if usage.TotalTokens == 0 && (usage.PromptTokens != 0 || usage.CompletionTokens != 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if streamErr != nil {
		return usage, streamErr
	}
	if c.Request.Context().Err() != nil || (info.StreamStatus != nil && (!info.StreamStatus.IsNormalEnd() || info.StreamStatus.HasErrors())) {
		return usage, types.NewOpenAIError(fmt.Errorf("Responses stream interrupted"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	if usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, usageText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	if !sentStart {
		if !sendChatChunk(helper.GenerateStartEmptyResponse(responseId, createAt, model, nil)) {
			return usage, streamErr
		}
	}
	if !sentStop {
		if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil {
			info.ClaudeConvertInfo.Usage = usage
		}
		if sawToolCall {
			finishReason = "tool_calls"
		}
		stop := helper.GenerateStopResponse(responseId, createAt, model, finishReason)
		if info.RelayFormat == types.RelayFormatClaude {
			stop.Usage = usage
		}
		if !sendChatChunk(stop) {
			return usage, streamErr
		}
	}
	if info.RelayFormat == types.RelayFormatOpenAI && info.ShouldIncludeUsage && usage != nil {
		if err := helper.ObjectData(c, helper.GenerateFinalUsageResponse(responseId, createAt, model, *usage)); err != nil {
			return usage, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		helper.Done(c)
	}
	return usage, nil
}
