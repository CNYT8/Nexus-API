package common

import (
	"encoding/json"
	"sort"
	"strings"
)

// ThinkingResponseEvent 保存原事件的 envelope，延迟文本不会新造事件或序号。
type ThinkingResponseEvent struct {
	Data     []byte
	Envelope []string
}

// outputAppender 只追加到已知正文位置，保留该事件的 usage/finish/sequence_number。
func outputAppender(root outputObject, path ...any) func(string) []byte {
	return func(text string) []byte {
		var appendAt func(json.RawMessage, []any) json.RawMessage
		appendAt = func(raw json.RawMessage, keys []any) json.RawMessage {
			if len(keys) == 0 {
				return outputEncode(outputString(raw) + text)
			}
			switch key := keys[0].(type) {
			case string:
				obj := outputMap(raw)
				if obj == nil {
					return raw
				}
				obj[key] = appendAt(obj[key], keys[1:])
				return outputEncode(obj)
			case int:
				var items []json.RawMessage
				if json.Unmarshal(raw, &items) != nil || key < 0 || key >= len(items) {
					return raw
				}
				items[key] = appendAt(items[key], keys[1:])
				return outputEncode(items)
			}
			return raw
		}
		key := path[0].(string)
		root[key] = appendAt(root[key], path[1:])
		return outputEncode(root)
	}
}

func visibleOutputIndex(indexes map[string]int, key string, hidden bool) (int, bool) {
	if index, ok := indexes[key]; ok {
		return index, index >= 0
	}
	if hidden {
		indexes[key] = -1
		return -1, false
	}
	index := 0
	for _, value := range indexes {
		if value >= index {
			index = value + 1
		}
	}
	indexes[key] = index
	return index, true
}

func (f *ThinkingResponseFilter) claudeIndex(obj outputObject, kind string) bool {
	hidden := kind == "content_block_start" && thinkingPart(outputMap(obj["content_block"]))
	if kind == "content_block_delta" && outputString(outputMap(obj["delta"])["type"]) == "thinking_delta" {
		hidden = true
	}
	index, visible := visibleOutputIndex(f.claudeIndexes, outputIndex(obj, "index", 0), hidden)
	if visible {
		obj["index"] = outputEncode(index)
	}
	return visible
}

func (f *ThinkingResponseFilter) responsesIndex(obj outputObject, kind string) bool {
	if obj["output_index"] == nil {
		return true
	}
	hidden := strings.HasPrefix(kind, "response.reasoning") || strings.HasPrefix(kind, "response.thinking") || thinkingPart(outputMap(obj["item"]))
	index, visible := visibleOutputIndex(f.responseIndexes, outputIndex(obj, "output_index", 0), hidden)
	if !visible {
		return false
	}
	obj["output_index"] = outputEncode(index)
	if obj["content_index"] != nil {
		key := string(obj["output_index"])
		if f.partIndexes[key] == nil {
			f.partIndexes[key] = make(map[string]int)
		}
		partIndex, partVisible := visibleOutputIndex(f.partIndexes[key], outputIndex(obj, "content_index", 0), thinkingPart(outputMap(obj["part"])))
		if !partVisible {
			return false
		}
		obj["content_index"] = outputEncode(partIndex)
	}
	return true
}

// flushPrefix 仅交付未构成标签的普通前缀；未闭合思考内容始终不发送。
func (f *ThinkingResponseFilter) flushPrefix(prefix string) {
	keys := make([]string, 0, len(f.tags))
	for key := range f.tags {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		tail, carrier := f.tails[key], f.carriers[key]
		if text := f.flush(key); text != "" && tail != nil && carrier != nil {
			carrier.Data = tail(text)
		}
	}
}

func (f *ThinkingResponseFilter) readyEvents() []*ThinkingResponseEvent {
	held := make(map[*ThinkingResponseEvent]bool)
	for key, filter := range f.tags {
		if !filter.InThinking && filter.Pending != "" {
			held[f.carriers[key]] = true
		}
	}
	count := 0
	for count < len(f.pending) && !held[f.pending[count]] {
		count++
	}
	ready := f.pending[:count:count]
	f.pending = f.pending[count:]
	return ready
}

func (f *ThinkingResponseFilter) FlushEvents() []*ThinkingResponseEvent {
	f.flushPrefix("")
	return f.readyEvents()
}

func (f *ThinkingResponseFilter) FilterEvent(data []byte, envelope []string) ([]*ThinkingResponseEvent, error) {
	obj := outputMap(data)
	kind := outputString(obj["type"])
	// done 文本是快照，不能再次接续 delta 的标签状态。
	switch kind {
	case "content_block_stop":
		if index, ok := f.claudeIndexes[outputIndex(obj, "index", 0)]; ok && index >= 0 {
			f.flushPrefix("claude." + string(outputEncode(index)) + ".")
		}
	case "response.output_text.done", "response.content_part.done", "response.output_item.done":
		if index, ok := f.responseIndexes[outputIndex(obj, "output_index", 0)]; ok && index >= 0 {
			prefix := "responses." + string(outputEncode(index)) + "."
			if kind != "response.output_item.done" {
				part := outputIndex(obj, "content_index", 0)
				if mapped, ok := f.partIndexes[string(outputEncode(index))][part]; ok {
					part = string(outputEncode(mapped))
				}
				prefix += part + "."
			}
			f.flushPrefix(prefix)
		}
	case "message_stop", "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		f.flushPrefix("")
	case "message_delta":
		if outputString(outputMap(obj["delta"])["stop_reason"]) != "" {
			f.flushPrefix("")
		}
	}
	f.current = &ThinkingResponseEvent{Envelope: envelope}
	cleaned, err := f.filterJSON(data, true)
	if err != nil {
		return nil, err
	}
	if len(cleaned) > 0 {
		f.current.Data = cleaned
		f.pending = append(f.pending, f.current)
	}
	// 带正文的 finish 帧必须先消费正文，再结束对应 choice/candidate。
	var choices []outputObject
	_ = json.Unmarshal(obj["choices"], &choices)
	for i, choice := range choices {
		if outputString(choice["finish_reason"]) != "" {
			f.flushPrefix("chat." + outputIndex(choice, "index", i) + ".")
		}
	}
	var candidates []outputObject
	_ = json.Unmarshal(obj["candidates"], &candidates)
	for i, candidate := range candidates {
		if outputString(candidate["finishReason"]) != "" {
			f.flushPrefix("gemini." + outputIndex(candidate, "index", i) + ".")
		}
	}
	f.current = nil
	return f.readyEvents(), nil
}
