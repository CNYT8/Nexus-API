package common

import (
	"encoding/json"
	"fmt"
)

// SanitizeOutputSensitiveJSON applies the same matcher to every textual value
// in a complete JSON response. It preserves usage and the JSON envelope while
// truncating only the value after the first matched point.
func SanitizeOutputSensitiveJSON(data []byte, matcher *OutputSensitiveMatcher) ([]byte, bool, string, error) {
	if matcher == nil || len(data) == 0 {
		return data, false, "", nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, false, "", err
	}
	matched, word := sanitizeOutputValue(&value, matcher)
	if !matched {
		return data, false, "", nil
	}
	cleaned, err := json.Marshal(value)
	if err != nil {
		return nil, true, word, err
	}
	return cleaned, true, word, nil
}

func sanitizeOutputValue(value *any, matcher *OutputSensitiveMatcher) (bool, string) {
	switch current := (*value).(type) {
	case string:
		index, word := matcher.MatchIndex(current)
		if index < 0 {
			return false, ""
		}
		runes := []rune(current)
		*value = string(runes[:index])
		return true, word
	case []any:
		for index := range current {
			matched, word := sanitizeOutputValue(&current[index], matcher)
			if matched {
				return true, word
			}
		}
	case map[string]any:
		for key, item := range current {
			// Only inspect output-bearing fields. IDs, model names, usage,
			// finish reasons and protocol metadata are not generated content.
			if !isOutputTextField(key) {
				if _, ok := item.(map[string]any); !ok {
					if _, ok := item.([]any); !ok {
						continue
					}
				}
				itemCopy := item
				matched, word := sanitizeOutputValue(&itemCopy, matcher)
				if matched {
					current[key] = itemCopy
					return true, word
				}
				continue
			}
			itemCopy := item
			matched, word := sanitizeOutputValue(&itemCopy, matcher)
			if matched {
				current[key] = itemCopy
				return true, word
			}
		}
	}
	return false, ""
}

func isOutputTextField(key string) bool {
	switch key {
	case "content", "text", "delta", "output_text", "reasoning_content", "reasoning", "thinking", "response", "message", "parts", "candidates", "content_block", "input_json", "arguments":
		return true
	default:
		return false
	}
}

func OutputSensitiveError(word string) error {
	return fmt.Errorf("output sensitive pattern matched: %q", word)
}
