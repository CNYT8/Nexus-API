package openaicompat

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/dto"
)

func ResponsesResponseToChatCompletionsResponse(resp *dto.OpenAIResponsesResponse, id string) (*dto.OpenAITextResponse, *dto.Usage, error) {
	if resp == nil {
		return nil, nil, errors.New("response is nil")
	}

	text := ExtractOutputTextFromResponses(resp)

	usage := &dto.Usage{}
	if resp.Usage != nil {
		if resp.Usage.InputTokens != 0 {
			usage.PromptTokens = resp.Usage.InputTokens
			usage.InputTokens = resp.Usage.InputTokens
		}
		if resp.Usage.OutputTokens != 0 {
			usage.CompletionTokens = resp.Usage.OutputTokens
			usage.OutputTokens = resp.Usage.OutputTokens
		}
		if resp.Usage.TotalTokens != 0 {
			usage.TotalTokens = resp.Usage.TotalTokens
		} else {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		usage.PromptTokensDetails = resp.Usage.PromptTokensDetails.Clone()
		if resp.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.MergeNonZero(*resp.Usage.InputTokensDetails)
		}
		usage.CompletionTokenDetails = resp.Usage.CompletionTokenDetails
		if resp.Usage.OutputTokensDetails != nil && resp.Usage.OutputTokensDetails.ReasoningTokens != 0 {
			usage.CompletionTokenDetails.ReasoningTokens = resp.Usage.OutputTokensDetails.ReasoningTokens
		}
	}

	created := resp.CreatedAt

	var toolCalls []dto.ToolCallResponse
	if len(resp.Output) > 0 {
		for _, out := range resp.Output {
			if out.Type != "function_call" {
				continue
			}
			name := strings.TrimSpace(out.Name)
			if name == "" {
				continue
			}
			callId := strings.TrimSpace(out.CallId)
			if callId == "" {
				callId = strings.TrimSpace(out.ID)
			}
			toolCalls = append(toolCalls, dto.ToolCallResponse{
				ID:   callId,
				Type: "function",
				Function: dto.FunctionResponse{
					Name:      name,
					Arguments: out.ArgumentsString(),
				},
			})
		}
	}

	finishReason := ResponsesFinishReason(resp, len(toolCalls) > 0)

	msg := dto.Message{
		Role:    "assistant",
		Content: text,
	}
	if len(toolCalls) > 0 {
		msg.SetToolCalls(toolCalls)
	}
	if reasoning := ExtractReasoningFromResponses(resp); reasoning != "" {
		msg.ReasoningContent = &reasoning
	}
	var annotations []any
	var refusal strings.Builder
	textOffset := 0
	for _, output := range resp.Output {
		if output.Type != "message" {
			continue
		}
		for _, part := range output.Content {
			annotations = append(annotations, ResponsesAnnotationsToChat(part.Annotations, textOffset)...)
			refusal.WriteString(part.Refusal)
			if part.Type == "output_text" {
				textOffset += utf8.RuneCountInString(part.Text)
			}
		}
	}
	if len(annotations) > 0 {
		msg.Annotations, _ = json.Marshal(annotations)
	}
	if refusal.Len() > 0 {
		value := refusal.String()
		msg.Refusal = &value
	}

	out := &dto.OpenAITextResponse{
		Id:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   resp.Model,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: *usage,
	}

	return out, usage, nil
}

func ExtractOutputTextFromResponses(resp *dto.OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Output) == 0 {
		return ""
	}

	var sb strings.Builder

	// Prefer assistant message outputs.
	for _, out := range resp.Output {
		if out.Type != "message" {
			continue
		}
		if out.Role != "" && out.Role != "assistant" {
			continue
		}
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	return sb.String()
}

func ExtractReasoningFromResponses(resp *dto.OpenAIResponsesResponse) string {
	if resp == nil {
		return ""
	}
	var parts []string
	for _, output := range resp.Output {
		if output.Type != "reasoning" {
			continue
		}
		for _, part := range output.Summary {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		if len(output.Summary) == 0 {
			for _, part := range output.Content {
				if part.Text != "" {
					parts = append(parts, part.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func ResponsesFinishReason(resp *dto.OpenAIResponsesResponse, hasTools bool) string {
	if resp != nil && resp.IncompleteDetails != nil {
		reason := resp.IncompleteDetails.Reason
		if reason == "" {
			reason = resp.IncompleteDetails.Reasoning
		}
		switch reason {
		case "max_output_tokens", "max_tokens":
			return "length"
		case "content_filter", "safety":
			return "content_filter"
		}
	}
	if hasTools {
		return "tool_calls"
	}
	return "stop"
}
