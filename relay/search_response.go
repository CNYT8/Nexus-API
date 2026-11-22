package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func searchResultBillable(path string, body []byte) (bool, error) {
	if !json.Valid(body) {
		return false, errors.New("invalid search response JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return false, errors.New("search response must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return false, err
		}
		name := key.(string)
		if _, duplicate := fields[name]; duplicate {
			return false, errors.New("ambiguous duplicate search response field")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return false, err
		}
		fields[name] = value
	}
	if raw, exists := fields["error"]; exists && string(bytes.TrimSpace(raw)) != "null" {
		return false, errors.New("search upstream returned an error envelope")
	}
	switch path {
	case dto.AlphaSearchPath:
		return true, nil
	case dto.DeepSeekSearchPath:
		raw := bytes.TrimSpace(fields["results"])
		// Tools may omit an empty result or encode it as null.
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			return false, nil
		}
		if raw[0] != '[' {
			return false, errors.New("search response requires a results array")
		}
		var results []json.RawMessage
		if err := json.Unmarshal(raw, &results); err != nil {
			return false, err
		}
		return len(results) > 0, nil
	case dto.DeepSeekFetchPath:
		raw := bytes.TrimSpace(fields["content"])
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			return false, nil
		}
		if raw[0] != '"' {
			return false, errors.New("fetch response requires string content")
		}
		var content string
		if err := json.Unmarshal(raw, &content); err != nil {
			return false, err
		}
		return content != "", nil
	default:
		return false, errors.New("invalid search endpoint")
	}
}

type searchStringSpan struct {
	start, end int
	text       string
}

// Search has no fixed response schema. Scan every string value, including
// future fields, without serializing numbers, object order or unknown fields.
// Keys are never rewritten. Strings are concatenated in wire order to prevent
// bypass by splitting a sensitive word across result/content fragments.
func sanitizeSearchResponse(body []byte, info *relaycommon.RelayInfo) ([]byte, bool, error) {
	matcher := relaycommon.NewOutputSensitiveMatcherForConfig(info.OutputSensitiveConfig)
	if matcher == nil && !info.ThinkingProcessStrip {
		return body, false, nil
	}
	if !json.Valid(body) {
		return nil, false, errors.New("invalid search response JSON")
	}
	spans := make([]searchStringSpan, 0)
	// A quoted token followed by ':' is a key. json.Valid has already checked
	// escaping and nesting; byte offsets remain relative to the original body.
	for i := 0; i < len(body); i++ {
		if body[i] != '"' {
			continue
		}
		start := i
		i++
		for i < len(body) {
			if body[i] == '\\' {
				i += 2
				continue
			}
			if body[i] == '"' {
				break
			}
			i++
		}
		end := i + 1
		next := end
		for next < len(body) && (body[next] == ' ' || body[next] == '\n' || body[next] == '\r' || body[next] == '\t') {
			next++
		}
		if next < len(body) && body[next] == ':' {
			continue
		}
		var text string
		if err := json.Unmarshal(body[start:end], &text); err != nil {
			return nil, false, err
		}
		spans = append(spans, searchStringSpan{start: start, end: end, text: text})
	}
	if info.ThinkingProcessStrip {
		filter := &relaycommon.ThinkingTagFilter{}
		for i := range spans {
			spans[i].text = filter.Filter(spans[i].text)
		}
		if len(spans) > 0 {
			spans[len(spans)-1].text += filter.Flush()
		}
	}
	var combined strings.Builder
	for _, span := range spans {
		combined.WriteString(span.text)
	}
	limit := -1
	if matcher != nil {
		limit, _ = matcher.MatchIndex(combined.String())
	}
	matched := limit >= 0
	offset, cursor := 0, 0
	var out bytes.Buffer
	changed := false
	for _, span := range spans {
		text := span.text
		runes := []rune(text)
		if matched && offset+len(runes) > limit {
			keep := limit - offset
			if keep < 0 {
				keep = 0
			}
			text = string(runes[:keep])
		}
		offset += len(runes)
		var original string
		_ = json.Unmarshal(body[span.start:span.end], &original)
		if text == original {
			continue
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return nil, false, err
		}
		out.Write(body[cursor:span.start])
		out.Write(encoded)
		cursor = span.end
		changed = true
	}
	if !changed {
		return body, matched, nil
	}
	out.Write(body[cursor:])
	return out.Bytes(), matched, nil
}
