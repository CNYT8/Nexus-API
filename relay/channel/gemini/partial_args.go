package gemini

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

type partialFunctionCalls map[int64]*dto.FunctionCall

// prepare buffers each continued call until its arguments form a complete value.
// Gemini serializes continued calls per candidate; switching ID/name mid-call
// is an upstream error, not a new tool invocation.
func (s partialFunctionCalls) prepare(response *dto.GeminiChatResponse) error {
	for i := range response.Candidates {
		candidate := &response.Candidates[i]
		parts := make([]dto.GeminiPart, 0, len(candidate.Content.Parts))
		for _, part := range candidate.Content.Parts {
			call := part.FunctionCall
			if call == nil || (s[candidate.Index] == nil && call.WillContinue == nil && len(call.PartialArgs) == 0) {
				parts = append(parts, part)
				continue
			}
			current := s[candidate.Index]
			if current == nil {
				current = &dto.FunctionCall{Arguments: map[string]any{}}
				s[candidate.Index] = current
			}
			if call.ID != "" {
				if current.ID != "" && current.ID != call.ID {
					return fmt.Errorf("Gemini candidate %d changed function ID during continuation", candidate.Index)
				}
				current.ID = call.ID
			}
			if call.FunctionName != "" {
				if current.FunctionName != "" && current.FunctionName != call.FunctionName {
					return fmt.Errorf("Gemini candidate %d changed function name during continuation", candidate.Index)
				}
				current.FunctionName = call.FunctionName
			}
			if call.Arguments != nil {
				object, ok := call.Arguments.(map[string]any)
				if !ok {
					return fmt.Errorf("Gemini function args must be an object")
				}
				for key, value := range object {
					current.Arguments.(map[string]any)[key] = value
				}
			}
			continuing := call.WillContinue != nil && *call.WillContinue
			for _, partial := range call.PartialArgs {
				path, err := parseGeminiPartialArgPath(partial.JSONPath)
				if err != nil {
					return err
				}
				var value any
				switch {
				case partial.StringValue != nil:
					value = *partial.StringValue
				case partial.NumberValue != nil:
					value = *partial.NumberValue
				case partial.BoolValue != nil:
					value = *partial.BoolValue
				case partial.NullValue != nil:
					value = nil
				default:
					continue
				}
				current.Arguments, err = setGeminiPartialArgValue(current.Arguments, path, value, partial.StringValue != nil)
				if err != nil {
					return fmt.Errorf("Gemini partial argument %q: %w", partial.JSONPath, err)
				}
				if partial.WillContinue != nil && *partial.WillContinue {
					continuing = true
				}
			}
			if continuing {
				continue
			}
			if current.FunctionName == "" {
				return fmt.Errorf("Gemini candidate %d completed a function call without a name", candidate.Index)
			}
			part.FunctionCall = current
			parts = append(parts, part)
			delete(s, candidate.Index)
		}
		candidate.Content.Parts = parts
	}
	return nil
}

type geminiPartialArgPathSegment struct {
	member  string
	index   int
	isIndex bool
}

func parseGeminiPartialArgPath(path string) ([]geminiPartialArgPathSegment, error) {
	path = strings.TrimSpace(path)
	if len(path) < 2 || path[0] != '$' || len(path) > 8192 {
		return nil, fmt.Errorf("unsupported Gemini partial argument path %q", path)
	}
	var segments []geminiPartialArgPathSegment
	for offset := 1; offset < len(path); {
		if len(segments) >= 64 {
			return nil, fmt.Errorf("Gemini partial argument path exceeds depth limit")
		}
		switch path[offset] {
		case '.':
			offset++
			start := offset
			for offset < len(path) && path[offset] != '.' && path[offset] != '[' {
				offset++
			}
			member := path[start:offset]
			if member == "" || strings.ContainsAny(member, "]*?") {
				return nil, fmt.Errorf("unsupported member in Gemini partial argument path %q", path)
			}
			segments = append(segments, geminiPartialArgPathSegment{member: member})
		case '[':
			offset++
			if offset >= len(path) {
				return nil, fmt.Errorf("unterminated Gemini partial argument selector")
			}
			if path[offset] == '\'' || path[offset] == '"' {
				quote, start := path[offset], offset
				offset++
				for offset < len(path) && path[offset] != quote {
					if path[offset] == '\\' {
						offset++
					}
					offset++
				}
				if offset+1 >= len(path) || path[offset+1] != ']' {
					return nil, fmt.Errorf("unterminated Gemini partial argument member")
				}
				raw := path[start : offset+1]
				if quote == '\'' {
					raw = `"` + strings.ReplaceAll(strings.ReplaceAll(raw[1:len(raw)-1], `"`, `\"`), `\'`, `'`) + `"`
				}
				var member string
				if err := json.Unmarshal([]byte(raw), &member); err != nil {
					return nil, err
				}
				segments = append(segments, geminiPartialArgPathSegment{member: member})
				offset += 2
				continue
			}
			start := offset
			for offset < len(path) && path[offset] >= '0' && path[offset] <= '9' {
				offset++
			}
			if start == offset || offset >= len(path) || path[offset] != ']' {
				return nil, fmt.Errorf("unsupported Gemini partial argument selector")
			}
			index, err := strconv.Atoi(path[start:offset])
			if err != nil || index > 65535 {
				return nil, fmt.Errorf("Gemini partial argument array index exceeds limit")
			}
			segments = append(segments, geminiPartialArgPathSegment{index: index, isIndex: true})
			offset++
		default:
			return nil, fmt.Errorf("unsupported Gemini partial argument path %q", path)
		}
	}
	return segments, nil
}

func setGeminiPartialArgValue(current any, path []geminiPartialArgPathSegment, value any, appendString bool) (any, error) {
	if len(path) == 0 {
		if appendString {
			if existing, ok := current.(string); ok {
				return existing + value.(string), nil
			}
		}
		return value, nil
	}
	segment := path[0]
	if segment.isIndex {
		var array []any
		switch typed := current.(type) {
		case nil:
			array = make([]any, segment.index+1)
		case []any:
			array = typed
			if len(array) <= segment.index {
				array = append(array, make([]any, segment.index-len(array)+1)...)
			}
		default:
			return nil, fmt.Errorf("array index traverses %T", current)
		}
		updated, err := setGeminiPartialArgValue(array[segment.index], path[1:], value, appendString)
		if err != nil {
			return nil, err
		}
		array[segment.index] = updated
		return array, nil
	}
	var object map[string]any
	switch typed := current.(type) {
	case nil:
		object = make(map[string]any)
	case map[string]any:
		object = typed
	default:
		return nil, fmt.Errorf("member %q traverses %T", segment.member, current)
	}
	updated, err := setGeminiPartialArgValue(object[segment.member], path[1:], value, appendString)
	if err != nil {
		return nil, err
	}
	object[segment.member] = updated
	return object, nil
}
