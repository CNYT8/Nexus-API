package common

import (
	"encoding/json"
	"errors"
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

// 剥离和扫描使用同一协议遍历器，正文标签先剥离，工具参数始终作为可见输出检查。
func SanitizeOutputSensitiveJSONWithThinkingPolicy(data []byte, matcher *OutputSensitiveMatcher, stripThinking bool) ([]byte, bool, string, error) {
	if !stripThinking {
		return sanitizeLegacyOutputJSON(data, matcher)
	}
	if len(data) > 0 {
		var err error
		data, err = StripThinkingJSON(data)
		if err != nil {
			return nil, false, "", err
		}
	}
	if matcher == nil || len(data) == 0 {
		return data, false, "", nil
	}
	if !json.Valid(data) {
		return nil, false, "", errors.New("invalid response JSON")
	}
	obj := outputMap(data)
	if obj == nil {
		return data, false, "", nil
	}
	// 完整响应先按逻辑正文路径拼接，跨 content/parts 的匹配也要从首个
	// 命中字符截断。工具参数的路径独立，不能与另一条 choice 拼接。
	texts := make(map[string]string)
	collector := outputJSONWalker{text: func(text string, loc outputTextLocation) string {
		texts[loc.path] += text
		return text
	}}
	collector.root(obj)
	limits := make(map[string]int)
	for path, text := range texts {
		if index, _ := matcher.MatchIndex(text); index >= 0 {
			limits[path] = index
		}
	}
	matched := false
	offsets := make(map[string]int)
	walker := outputJSONWalker{text: func(text string, loc outputTextLocation) string {
		if matched {
			return ""
		}
		runes := []rune(text)
		start := offsets[loc.path]
		offsets[loc.path] += len(runes)
		if limit, ok := limits[loc.path]; ok && limit < offsets[loc.path] {
			matched = true
			return string(runes[:limit-start])
		}
		return text
	}}
	walker.root(obj)
	if !matched {
		return data, false, "", nil
	}
	cleaned, err := json.Marshal(obj)
	return cleaned, true, outputSensitiveMatchLabel, err
}

// 流式路径按 choice/candidate/block/item 索引隔离，避免不同输出互相拼接命中。
func VisibleOutputSensitiveTextFragments(data string) []OutputSensitiveTextFragment {
	if data == "" {
		return nil
	}
	if !json.Valid([]byte(data)) {
		return []OutputSensitiveTextFragment{{Path: "$text", Text: data}}
	}
	obj := outputMap([]byte(data))
	if obj == nil {
		return nil
	}
	fragments := make([]OutputSensitiveTextFragment, 0, 2)
	walker := outputJSONWalker{stream: true, text: func(text string, loc outputTextLocation) string {
		if text != "" {
			fragments = append(fragments, OutputSensitiveTextFragment{Path: loc.path, Text: text, HiddenThinking: loc.hidden})
		}
		return text
	}}
	walker.root(obj)
	return fragments
}

func OutputSensitiveError() error {
	return errors.New("output sensitive pattern matched")
}
