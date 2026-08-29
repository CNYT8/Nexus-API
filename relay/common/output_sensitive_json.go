package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type OutputSensitiveTextFragment struct {
	Path           string
	Text           string
	HiddenThinking bool
}

// SanitizeOutputSensitiveJSON scans only output-bearing string fields in a
// complete JSON response. Every matching output field is truncated while the
// response envelope and usage metadata are preserved.
func SanitizeOutputSensitiveJSON(data []byte, matcher *OutputSensitiveMatcher) ([]byte, bool, string, error) {
	return SanitizeOutputSensitiveJSONWithThinkingPolicy(data, matcher, false)
}

// SanitizeOutputSensitiveJSONWithThinkingPolicy excludes reasoning fields that
// the relay will strip before returning the response.
func SanitizeOutputSensitiveJSONWithThinkingPolicy(data []byte, matcher *OutputSensitiveMatcher, stripThinking bool) ([]byte, bool, string, error) {
	if matcher == nil || len(data) == 0 {
		return data, false, "", nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, false, "", err
	}
	matched := sanitizeOutputValue(&value, matcher, true, false, stripThinking)
	if !matched {
		return data, false, "", nil
	}
	cleaned, err := json.Marshal(value)
	if err != nil {
		return nil, true, outputSensitiveMatchLabel, err
	}
	return cleaned, true, outputSensitiveMatchLabel, nil
}

// OutputSensitiveTextFragments decodes a streaming JSON payload and returns
// generated-content fragments keyed by stable output paths. Invalid JSON is
// treated as plain text because some compatible upstreams emit text-only SSE.
func OutputSensitiveTextFragments(data string) []OutputSensitiveTextFragment {
	if data == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return []OutputSensitiveTextFragment{{Path: "$text", Text: data}}
	}
	fragments := make([]OutputSensitiveTextFragment, 0, 2)
	rootPath := "$"
	if root, ok := value.(map[string]any); ok {
		if eventType, ok := root["type"].(string); ok && eventType != "" {
			rootPath = "$event:" + eventType
		}
	}
	collectOutputTextFragments(value, rootPath, true, false, &fragments)
	return fragments
}

type outputSensitiveSanitizeState struct {
	matcher *OutputSensitiveMatcher
	matched bool
	blocked bool
}

func sanitizeOutputValue(value *any, matcher *OutputSensitiveMatcher, root bool, hiddenThinking bool, stripThinking bool) bool {
	state := &outputSensitiveSanitizeState{matcher: matcher}
	sanitizeOutputValueWithState(value, state, root, hiddenThinking, stripThinking)
	return state.matched
}

func sanitizeOutputValueWithState(value *any, state *outputSensitiveSanitizeState, root bool, hiddenThinking bool, stripThinking bool) {
	switch current := (*value).(type) {
	case string:
		if !root || (stripThinking && hiddenThinking) {
			return
		}
		if state.blocked {
			*value = ""
			return
		}
		index, _ := state.matcher.MatchIndex(current)
		if index < 0 {
			return
		}
		runes := []rune(current)
		*value = string(runes[:index])
		state.matched = true
		state.blocked = true
	case []any:
		for index := range current {
			sanitizeOutputValueWithState(&current[index], state, false, hiddenThinking, stripThinking)
		}
	case map[string]any:
		objectHiddenThinking := hiddenThinking || outputObjectContainsHiddenThinking(current)
		for _, key := range sortedOutputKeys(current) {
			item := current[key]
			fieldHiddenThinking := objectHiddenThinking || isThinkingOutputField(key)
			switch typed := item.(type) {
			case string:
				if !isOutputTextField(key) || (stripThinking && fieldHiddenThinking) {
					continue
				}
				if state.blocked {
					current[key] = ""
					continue
				}
				index, _ := state.matcher.MatchIndex(typed)
				if index < 0 {
					continue
				}
				runes := []rune(typed)
				current[key] = string(runes[:index])
				state.matched = true
				state.blocked = true
			case []any, map[string]any:
				itemCopy := typed
				sanitizeOutputValueWithState(&itemCopy, state, false, fieldHiddenThinking, stripThinking)
				current[key] = itemCopy
			}
		}
	}
}

func collectOutputTextFragments(value any, path string, root bool, hiddenThinking bool, fragments *[]OutputSensitiveTextFragment) {
	switch current := value.(type) {
	case string:
		if root && current != "" {
			*fragments = append(*fragments, OutputSensitiveTextFragment{
				Path:           path,
				Text:           current,
				HiddenThinking: hiddenThinking,
			})
		}
	case []any:
		for index, item := range current {
			collectOutputTextFragments(item, fmt.Sprintf("%s[%d]", path, index), false, hiddenThinking, fragments)
		}
	case map[string]any:
		objectHiddenThinking := hiddenThinking || outputObjectContainsHiddenThinking(current)
		for _, key := range sortedOutputKeys(current) {
			item := current[key]
			fieldPath := path + "." + key
			fieldHiddenThinking := objectHiddenThinking || isThinkingOutputField(key)
			switch typed := item.(type) {
			case string:
				if isOutputTextField(key) && typed != "" {
					*fragments = append(*fragments, OutputSensitiveTextFragment{
						Path:           fieldPath,
						Text:           typed,
						HiddenThinking: fieldHiddenThinking,
					})
				}
			case []any, map[string]any:
				collectOutputTextFragments(typed, fieldPath, false, fieldHiddenThinking, fragments)
			}
		}
	}
}

func sortedOutputKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isOutputTextField(key string) bool {
	switch key {
	case "answer", "arguments", "completion", "content", "delta", "input_json",
		"message", "output_text", "partial_json", "reasoning", "reasoning_content",
		"refusal", "response", "text", "thinking":
		return true
	default:
		return false
	}
}

func isThinkingOutputField(key string) bool {
	switch key {
	case "reasoning", "reasoning_content", "thinking":
		return true
	default:
		return false
	}
}

func outputObjectContainsHiddenThinking(value map[string]any) bool {
	if thought, ok := value["thought"].(bool); ok && thought {
		return true
	}
	outputType, _ := value["type"].(string)
	outputType = strings.ToLower(outputType)
	return outputType == "thinking" || outputType == "reasoning" || strings.Contains(outputType, "reasoning")
}

func OutputSensitiveError() error {
	return errors.New("output sensitive pattern matched")
}
