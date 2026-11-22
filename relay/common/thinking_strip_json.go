package common

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 使用 RawMessage 保留未触及字段的数值精度与扩展字段。只有协议输出
// 节点可以删除思考；工具 input/args/arguments 仅供敏感词扫描，不做剥离。
type outputObject map[string]json.RawMessage

type outputTextLocation struct {
	path      string
	hidden    bool
	tags      bool
	streaming bool
	end       bool
	tail      func(string) []byte
	local     *ThinkingTagFilter
}

type outputJSONWalker struct {
	strip  bool
	stream bool
	filter *ThinkingResponseFilter
	data   outputObject
	text   func(string, outputTextLocation) string
}

func outputMap(raw json.RawMessage) outputObject {
	var value outputObject
	_ = json.Unmarshal(raw, &value)
	return value
}

func outputString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func outputEncode(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func outputIndex(value outputObject, key string, fallback int) string {
	if raw := value[key]; len(raw) > 0 && string(raw) != "null" {
		return string(raw)
	}
	return fmt.Sprint(fallback)
}

func (w *outputJSONWalker) field(obj outputObject, key string, loc outputTextLocation) {
	var text string
	if raw, ok := obj[key]; ok && json.Unmarshal(raw, &text) == nil {
		cleaned := w.text(text, loc)
		if cleaned != text {
			obj[key] = outputEncode(cleaned)
		}
	}
}

func (w *outputJSONWalker) list(obj outputObject, key string, visit func(outputObject, int) bool) {
	var items []json.RawMessage
	if json.Unmarshal(obj[key], &items) != nil || items == nil {
		return
	}
	kept := make([]json.RawMessage, 0, len(items))
	for i, raw := range items {
		item := outputMap(raw)
		if item == nil {
			kept = append(kept, raw)
			continue
		}
		if visit(item, i) {
			kept = append(kept, outputEncode(item))
		}
	}
	obj[key] = outputEncode(kept)
}

func (w *outputJSONWalker) thinkingFields(obj outputObject, path string) {
	for _, key := range []string{"reasoning_content", "reasoning", "thinking", "thoughts", "reasoning_details", "reasoning_summary", "reasoning_summary_text"} {
		if w.strip {
			delete(obj, key)
		} else if raw, ok := obj[key]; ok {
			obj[key] = w.argument(raw, path+"."+key, true)
		}
	}
}

// 参数内容可以包含任意字段名，包括 thinking/type/signature；不得按名字免检或删除。
func (w *outputJSONWalker) argument(raw json.RawMessage, path string, hidden bool) json.RawMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		cleaned := w.text(text, outputTextLocation{path: path, hidden: hidden})
		if cleaned == text {
			return raw
		}
		return outputEncode(cleaned)
	}
	if obj := outputMap(raw); obj != nil {
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			obj[key] = w.argument(obj[key], path+"."+key, hidden)
		}
		return outputEncode(obj)
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil && items != nil {
		for i := range items {
			items[i] = w.argument(items[i], fmt.Sprintf("%s[%d]", path, i), hidden)
		}
		return outputEncode(items)
	}
	return raw
}

func (w *outputJSONWalker) tool(obj outputObject, path string) {
	for _, key := range []string{"arguments", "input", "input_json", "partial_json", "args"} {
		if raw, ok := obj[key]; ok {
			obj[key] = w.argument(raw, path+"."+key, false)
		}
	}
	for _, key := range []string{"function", "functionCall", "function_call", "functionResponse"} {
		if child := outputMap(obj[key]); child != nil {
			w.tool(child, path+"."+key)
			obj[key] = outputEncode(child)
		}
	}
}

func toolPart(obj outputObject) bool {
	kind := outputString(obj["type"])
	return kind == "tool_use" || kind == "server_tool_use" || kind == "function_call" || obj["functionCall"] != nil
}

func thinkingPart(obj outputObject) bool {
	return !toolPart(obj) && (isThinkingObjectType(outputString(obj["type"])) || string(obj["thought"]) == "true")
}

func (w *outputJSONWalker) part(obj outputObject, loc outputTextLocation) bool {
	// Gemini 签名与 functionCall 是后续工具回传所需的数据，即使附带 thought 也保留。
	if toolPart(obj) {
		if w.strip && string(obj["thought"]) == "true" {
			delete(obj, "text")
		}
		w.tool(obj, loc.path)
		return true
	}
	loc.hidden = loc.hidden || thinkingPart(obj)
	if w.strip && loc.hidden {
		return false
	}
	w.thinkingFields(obj, loc.path)
	for _, key := range []string{"text", "refusal"} {
		fieldLoc := loc
		fieldLoc.path += "." + key
		w.field(obj, key, fieldLoc)
	}
	// 可执行代码/工具结果属于可见输出，签名、媒体及元数据不参与扫描。
	for _, entry := range []struct{ object, field string }{{"executableCode", "code"}, {"codeExecutionResult", "output"}} {
		if child := outputMap(obj[entry.object]); child != nil {
			w.field(child, entry.field, outputTextLocation{path: loc.path + "." + entry.object, hidden: loc.hidden})
			obj[entry.object] = outputEncode(child)
		}
	}
	return true
}

func (w *outputJSONWalker) tail(prefix []any, keys ...any) func(string) []byte {
	path := append(append([]any(nil), prefix...), keys...)
	return outputAppender(w.data, path...)
}

func (w *outputJSONWalker) message(obj outputObject, path string, streaming, end bool, prefix []any) {
	w.thinkingFields(obj, path)
	loc := outputTextLocation{path: path + ".content", tags: true, streaming: streaming, end: end, tail: w.tail(prefix, "content")}
	w.field(obj, "content", loc)
	local := &ThinkingTagFilter{}
	visibleIndex := 0
	lastText := -1
	w.list(obj, "content", func(part outputObject, i int) bool {
		partLoc := loc
		if toolPart(part) {
			partLoc.path += ".tool." + fmt.Sprint(i)
		}
		if !streaming {
			partLoc.local = local
		}
		partLoc.tail = w.tail(prefix, "content", visibleIndex, "text")
		kept := w.part(part, partLoc)
		if kept {
			if part["text"] != nil && !toolPart(part) {
				lastText = visibleIndex
			}
			visibleIndex++
		}
		return kept
	})
	if w.strip && !streaming && lastText >= 0 {
		if text := local.Flush(); text != "" {
			outputAppender(obj, "content", lastText, "text")(text)
		}
	}
	w.field(obj, "refusal", outputTextLocation{path: path + ".refusal"})
	w.list(obj, "tool_calls", func(tool outputObject, i int) bool {
		w.tool(tool, path+".tool."+outputIndex(tool, "index", i))
		return true
	})
	w.tool(obj, path)
}

func (w *outputJSONWalker) response(obj outputObject, path string) {
	w.thinkingFields(obj, path)
	w.list(obj, "output", func(item outputObject, i int) bool {
		return w.item(item, path+".output."+outputIndex(item, "id", i))
	})
	w.field(obj, "output_text", outputTextLocation{path: path + ".output_text", tags: true})
}

func (w *outputJSONWalker) item(item outputObject, path string) bool {
	if thinkingPart(item) {
		if w.strip {
			return false
		}
		for _, key := range []string{"summary", "content"} {
			w.list(item, key, func(part outputObject, i int) bool {
				return w.part(part, outputTextLocation{path: fmt.Sprintf("%s.%s.%d", path, key, i), hidden: true})
			})
		}
		return true
	}
	if outputString(item["type"]) == "message" {
		w.message(item, path, false, true, nil)
	} else {
		w.tool(item, path)
	}
	return true
}

func (w *outputJSONWalker) root(obj outputObject) bool {
	w.data = obj
	kind := outputString(obj["type"])
	if strings.HasPrefix(kind, "response.") {
		return w.responsesEvent(obj, kind)
	}
	if strings.HasPrefix(kind, "content_block_") || kind == "message_start" || kind == "message_delta" || kind == "message_stop" {
		return w.claudeEvent(obj, kind)
	}
	w.list(obj, "choices", func(choice outputObject, i int) bool {
		index := outputIndex(choice, "index", i)
		path := "chat." + index
		w.thinkingFields(choice, path)
		end := choice["finish_reason"] != nil && string(choice["finish_reason"]) != "null" && outputString(choice["finish_reason"]) != ""
		for _, key := range []string{"message", "delta"} {
			if message := outputMap(choice[key]); message != nil {
				w.message(message, path, w.stream && key == "delta", end, []any{"choices", i, key})
				choice[key] = outputEncode(message)
			}
		}
		w.field(choice, "text", outputTextLocation{path: path + ".text", tags: true, streaming: w.stream, end: end, tail: outputAppender(obj, "choices", i, "text")})
		return true
	})
	w.list(obj, "candidates", func(candidate outputObject, i int) bool {
		index := outputIndex(candidate, "index", i)
		path := "gemini." + index
		content := outputMap(candidate["content"])
		if content == nil {
			return true
		}
		visibleIndex := 0
		lastText := -1
		local := &ThinkingTagFilter{}
		w.list(content, "parts", func(part outputObject, partIndex int) bool {
			// Gemini 的 parts 每帧重新编号；正文状态归属于候选项，工具参数单独隔离。
			partPath := path
			if toolPart(part) {
				partPath += ".tool." + outputIndex(outputMap(part["functionCall"]), "id", partIndex)
			}
			kept := w.part(part, outputTextLocation{path: partPath, tags: true, streaming: w.stream, local: local, tail: outputAppender(obj, "candidates", i, "content", "parts", visibleIndex, "text")})
			if kept {
				if part["text"] != nil && !toolPart(part) {
					lastText = visibleIndex
				}
				visibleIndex++
			}
			return kept
		})
		if w.strip && !w.stream && lastText >= 0 {
			if text := local.Flush(); text != "" {
				outputAppender(content, "parts", lastText, "text")(text)
			}
		}
		candidate["content"] = outputEncode(content)
		return true
	})
	if kind == "message" || obj["content"] != nil {
		w.message(obj, "claude", false, true, nil)
	}
	if obj["output"] != nil || outputString(obj["object"]) == "response" {
		w.response(obj, "responses")
	}
	// 兼容旧 completions/Ollama 的明确顶层输出字段。
	for _, key := range []string{"completion", "answer", "response"} {
		w.field(obj, key, outputTextLocation{path: key, tags: true, streaming: w.stream})
	}
	return true
}

func (w *outputJSONWalker) claudeEvent(obj outputObject, kind string) bool {
	index := outputIndex(obj, "index", 0)
	if w.strip && strings.HasPrefix(kind, "content_block_") {
		if !w.filter.claudeIndex(obj, kind) {
			return false
		}
		index = outputIndex(obj, "index", 0)
	}
	path := "claude." + index
	if kind == "message_start" {
		if message := outputMap(obj["message"]); message != nil {
			w.message(message, "claude", false, true, nil)
			obj["message"] = outputEncode(message)
		}
		return true
	}
	if kind == "content_block_start" {
		block := outputMap(obj["content_block"])
		if block == nil {
			return true
		}
		w.part(block, outputTextLocation{path: path, tags: true, streaming: w.stream, tail: outputAppender(obj, "content_block", "text")})
		obj["content_block"] = outputEncode(block)
	}
	if kind == "content_block_delta" {
		delta := outputMap(obj["delta"])
		deltaKind := outputString(delta["type"])
		hidden := deltaKind == "thinking_delta"
		if w.strip && hidden {
			return false
		}
		w.thinkingFields(delta, path)
		w.field(delta, "text", outputTextLocation{path: path + ".text", hidden: hidden, tags: true, streaming: w.stream, tail: outputAppender(obj, "delta", "text")})
		w.tool(delta, path)
		obj["delta"] = outputEncode(delta)
	}
	return true
}

func (w *outputJSONWalker) responsesEvent(obj outputObject, kind string) bool {
	if w.strip && !w.filter.responsesIndex(obj, kind) {
		return false
	}
	hidden := strings.HasPrefix(kind, "response.reasoning") || strings.HasPrefix(kind, "response.thinking")
	if hidden && w.strip {
		return false
	}
	if response := outputMap(obj["response"]); response != nil {
		w.response(response, "responses")
		obj["response"] = outputEncode(response)
	}
	if item := outputMap(obj["item"]); item != nil {
		if !w.item(item, "responses.item."+outputString(obj["item_id"])) {
			return false
		}
		obj["item"] = outputEncode(item)
	}
	path := "responses." + outputIndex(obj, "output_index", 0) + "." + outputIndex(obj, "content_index", 0)
	if part := outputMap(obj["part"]); part != nil {
		if !w.part(part, outputTextLocation{path: path, hidden: hidden, tags: true}) {
			return false
		}
		obj["part"] = outputEncode(part)
	}
	argument := strings.HasPrefix(kind, "response.function_call_arguments.") || strings.HasPrefix(kind, "response.custom_tool_call_input.")
	isText := strings.HasPrefix(kind, "response.output_text.")
	if isText || hidden || argument || strings.HasPrefix(kind, "response.refusal.") {
		for _, key := range []string{"delta", "text", "arguments", "input", "refusal"} {
			w.field(obj, key, outputTextLocation{path: path + "." + key, hidden: hidden, tags: isText, streaming: w.stream && strings.HasSuffix(kind, ".delta"), tail: outputAppender(obj, key)})
		}
	}
	return true
}

// StripStructuredThinkingJSON 用于转换前分离同帧思考与正文，不消费正文标签状态。
func StripStructuredThinkingJSON(data []byte) ([]byte, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid response JSON")
	}
	obj := outputMap(data)
	if obj == nil {
		return data, nil
	}
	filter := &ThinkingResponseFilter{}
	filter.init()
	walker := outputJSONWalker{strip: true, filter: filter, text: func(text string, _ outputTextLocation) string { return text }}
	if !walker.root(obj) {
		return nil, nil
	}
	return json.Marshal(obj)
}

// ThinkingResponseFilter 的实例只属于一个响应出口，不能在扫描/写出或并发流之间共享。
type ThinkingResponseFilter struct {
	tags            map[string]*ThinkingTagFilter
	tails           map[string]func(string) []byte
	carriers        map[string]*ThinkingResponseEvent
	current         *ThinkingResponseEvent
	pending         []*ThinkingResponseEvent
	claudeIndexes   map[string]int
	responseIndexes map[string]int
	partIndexes     map[string]map[string]int
}

func (f *ThinkingResponseFilter) filterJSON(data []byte, stream bool) ([]byte, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid response JSON")
	}
	obj := outputMap(data)
	if obj == nil {
		return data, nil
	}
	f.init()
	walker := outputJSONWalker{strip: true, stream: stream, filter: f, text: f.text}
	if !walker.root(obj) {
		return nil, nil
	}
	return json.Marshal(obj)
}

func (f *ThinkingResponseFilter) init() {
	if f.tags == nil {
		f.tags = make(map[string]*ThinkingTagFilter)
		f.tails = make(map[string]func(string) []byte)
		f.carriers = make(map[string]*ThinkingResponseEvent)
		f.claudeIndexes = make(map[string]int)
		f.responseIndexes = make(map[string]int)
		f.partIndexes = make(map[string]map[string]int)
	}
}

func (f *ThinkingResponseFilter) text(text string, loc outputTextLocation) string {
	if !loc.tags {
		return text
	}
	if !loc.streaming {
		if loc.local != nil {
			return loc.local.Filter(text)
		}
		filter := &ThinkingTagFilter{}
		return filter.Filter(text) + filter.Flush()
	}
	filter := f.tags[loc.path]
	if filter == nil {
		filter = &ThinkingTagFilter{}
		f.tags[loc.path] = filter
	}
	if loc.tail != nil {
		f.tails[loc.path] = loc.tail
		f.carriers[loc.path] = f.current
	}
	cleaned := filter.Filter(text)
	if loc.end {
		cleaned += f.flush(loc.path)
	}
	return cleaned
}

func (f *ThinkingResponseFilter) flush(path string) string {
	filter := f.tags[path]
	if filter == nil {
		return ""
	}
	text := filter.Flush()
	delete(f.tags, path)
	delete(f.tails, path)
	delete(f.carriers, path)
	return text
}
