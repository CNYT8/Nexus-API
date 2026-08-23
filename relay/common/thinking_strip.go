package common

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// StripThinkingJSON removes reasoning payloads from an upstream response while
// preserving usage fields such as reasoning_tokens. It is used as a final
// defense for pass-through and vendor-specific response shapes.
func StripThinkingJSON(data []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	cleaned, keep := stripThinkingValue(value)
	if !keep {
		return []byte("null"), nil
	}
	return json.Marshal(cleaned)
}

func stripThinkingValue(value any) (any, bool) {
	switch current := value.(type) {
	case []any:
		result := make([]any, 0, len(current))
		for _, item := range current {
			cleaned, keep := stripThinkingValue(item)
			if keep {
				result = append(result, cleaned)
			}
		}
		return result, true
	case map[string]any:
		if responseType, ok := current["type"].(string); ok && isThinkingObjectType(responseType) {
			return nil, false
		}
		if thought, ok := current["thought"].(bool); ok && thought {
			return nil, false
		}
		for key, item := range current {
			if isThinkingPayloadKey(key) {
				delete(current, key)
				continue
			}
			cleaned, keep := stripThinkingValue(item)
			if keep {
				current[key] = cleaned
			} else {
				delete(current, key)
			}
		}
		return current, true
	default:
		return value, true
	}
}

func isThinkingPayloadKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "reasoning_content", "reasoning", "thinking", "thought", "thoughts", "signature", "thought_signature", "thoughtsignature", "thinking_delta", "reasoning_summary", "reasoningsummary", "reasoning_summary_text":
		return true
	default:
		return false
	}
}

func isThinkingObjectType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "thinking", "reasoning", "redacted_thinking", "reasoning_text", "reasoning_summary_text":
		return true
	default:
		return false
	}
}

func StripChatCompletionsThinking(response *dto.ChatCompletionsStreamResponse) {
	if response == nil {
		return
	}
	for index := range response.Choices {
		response.Choices[index].Delta.ReasoningContent = nil
		response.Choices[index].Delta.Reasoning = nil
	}
}

func StripOpenAITextResponseThinking(response *dto.OpenAITextResponse) {
	if response == nil {
		return
	}
	for index := range response.Choices {
		response.Choices[index].Message.ReasoningContent = nil
		response.Choices[index].Message.Reasoning = nil
	}
}

func StripResponsesResponseThinking(response *dto.OpenAIResponsesResponse) {
	if response == nil {
		return
	}
	filtered := response.Output[:0]
	for _, item := range response.Output {
		if isThinkingObjectType(item.Type) {
			continue
		}
		content := item.Content[:0]
		for _, part := range item.Content {
			if !isThinkingObjectType(part.Type) {
				content = append(content, part)
			}
		}
		item.Content = content
		filtered = append(filtered, item)
	}
	response.Output = filtered
	response.Reasoning = nil
}

func IsResponsesThinkingEvent(response dto.ResponsesStreamResponse) bool {
	typeName := strings.ToLower(strings.TrimSpace(response.Type))
	if strings.Contains(typeName, "reasoning") || strings.Contains(typeName, "thinking") {
		return true
	}
	return response.Item != nil && isThinkingObjectType(response.Item.Type)
}

func ChatCompletionsHasVisibleData(response *dto.ChatCompletionsStreamResponse) bool {
	if response == nil {
		return false
	}
	if response.Usage != nil && len(response.Choices) == 0 {
		return true
	}
	for _, choice := range response.Choices {
		if choice.Delta.GetContentString() != "" || len(choice.Delta.ToolCalls) > 0 ||
			choice.Delta.Role != "" || choice.FinishReason != nil {
			return true
		}
	}
	return false
}

// ThinkingTagFilter removes reasoning accidentally embedded in ordinary text,
// including tags split across SSE chunks. Content inside an unclosed thinking
// tag is deliberately withheld until the stream ends.
type ThinkingTagFilter struct {
	InThinking bool
	Pending    string
}

var thinkingTagMarkers = []string{
	"<think>", "</think>", "<analysis>", "</analysis>",
	"<reasoning>", "</reasoning>", "<thought>", "</thought>",
}

func (f *ThinkingTagFilter) Reset() {
	if f == nil {
		return
	}
	f.InThinking = false
	f.Pending = ""
}

func (f *ThinkingTagFilter) Filter(text string) string {
	if f == nil || text == "" {
		return text
	}
	input := f.Pending + text
	f.Pending = ""
	var output strings.Builder
	for input != "" {
		lower := strings.ToLower(input)
		if f.InThinking {
			index, marker := findThinkingMarker(lower, false)
			if index < 0 {
				f.Pending = retainThinkingMarkerPrefix(input, false)
				return output.String()
			}
			input = input[index+len(marker):]
			f.InThinking = false
			continue
		}
		index, marker := findThinkingMarker(lower, true)
		if index < 0 {
			keep := retainThinkingMarkerPrefix(input, true)
			output.WriteString(input[:len(input)-len(keep)])
			f.Pending = keep
			return output.String()
		}
		output.WriteString(input[:index])
		input = input[index+len(marker):]
		f.InThinking = true
	}
	return output.String()
}

func (f *ThinkingTagFilter) Flush() string {
	if f == nil || f.InThinking {
		if f != nil {
			f.Pending = ""
		}
		return ""
	}
	pending := f.Pending
	f.Pending = ""
	return pending
}

func findThinkingMarker(lower string, opening bool) (int, string) {
	bestIndex := -1
	bestMarker := ""
	for _, marker := range thinkingTagMarkers {
		isOpen := !strings.HasPrefix(marker, "</")
		if isOpen != opening {
			continue
		}
		if index := strings.Index(lower, marker); index >= 0 && (bestIndex < 0 || index < bestIndex) {
			bestIndex = index
			bestMarker = marker
		}
	}
	return bestIndex, bestMarker
}

func retainThinkingMarkerPrefix(input string, opening bool) string {
	lower := strings.ToLower(input)
	for size := len(input); size > 0; size-- {
		suffix := lower[len(lower)-size:]
		for _, marker := range thinkingTagMarkers {
			if (!strings.HasPrefix(marker, "</")) == opening && strings.HasPrefix(marker, suffix) {
				return input[len(input)-size:]
			}
		}
	}
	return ""
}

func FilterChatCompletionsThinkingTags(response *dto.ChatCompletionsStreamResponse, filter *ThinkingTagFilter) {
	if response == nil || filter == nil {
		return
	}
	for index := range response.Choices {
		content := response.Choices[index].Delta.GetContentString()
		if content != "" {
			content = filter.Filter(content)
			response.Choices[index].Delta.SetContentString(content)
		}
	}
}
