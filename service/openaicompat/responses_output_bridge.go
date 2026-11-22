package openaicompat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/dto"
)

// ResponsesOutputBridge 的回调必须同步消费事件；所有事件共享一个序号分配器。
// 用独立的 wire object 保留 custom/hosted 工具字段，不改变原有 DTO 的使用方。
type ResponsesOutputBridge struct {
	id, model                string
	created                  int64
	sequence                 int
	started, finished        bool
	items                    []map[string]any
	text, reasoning, refusal int
	calls                    map[int]int
	customArgs               map[int]string
	tools                    ResponsesTools
	finishReason             string
	usage                    *dto.Usage
	emit                     func(map[string]any) error
	counted                  strings.Builder
}

func NewResponsesOutputBridge(id, model string, tools ResponsesTools, emit func(map[string]any) error) *ResponsesOutputBridge {
	return &ResponsesOutputBridge{id: id, model: model, created: time.Now().Unix(), text: -1, reasoning: -1, refusal: -1, calls: map[int]int{}, customArgs: map[int]string{}, tools: tools, items: []map[string]any{}, emit: emit}
}

func (s *ResponsesOutputBridge) event(kind string, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["type"], fields["sequence_number"] = kind, s.sequence
	s.sequence++
	if s.emit != nil {
		return s.emit(fields)
	}
	return nil
}

func (s *ResponsesOutputBridge) Start() error {
	if s.started {
		return nil
	}
	s.started = true
	if err := s.event("response.created", map[string]any{"response": s.Response("in_progress", nil)}); err != nil {
		return err
	}
	return s.event("response.in_progress", map[string]any{"response": s.Response("in_progress", nil)})
}

func ResponsesDisplayUsage(usage *dto.Usage) *dto.Usage {
	if usage == nil {
		return nil
	}
	copy := *usage
	copy.PromptTokensDetails = usage.PromptTokensDetails.Clone()
	if copy.PromptTokens == 0 {
		copy.PromptTokens = copy.InputTokens
	}
	if copy.CompletionTokens == 0 {
		copy.CompletionTokens = copy.OutputTokens
	}
	copy.InputTokens, copy.OutputTokens = copy.PromptTokens, copy.CompletionTokens
	if copy.TotalTokens == 0 {
		copy.TotalTokens = copy.InputTokens + copy.OutputTokens
	}
	if usage.InputTokensDetails != nil {
		details := usage.InputTokensDetails.Clone()
		copy.InputTokensDetails = &details
	} else {
		details := copy.PromptTokensDetails.Clone()
		copy.InputTokensDetails = &details
	}
	if usage.OutputTokensDetails != nil {
		details := *usage.OutputTokensDetails
		copy.OutputTokensDetails = &details
	} else {
		details := copy.CompletionTokenDetails
		copy.OutputTokensDetails = &details
	}
	return &copy
}

func (s *ResponsesOutputBridge) SetUsage(usage *dto.Usage) { s.usage = ResponsesDisplayUsage(usage) }
func (s *ResponsesOutputBridge) CountedText() string       { return s.counted.String() }
func (s *ResponsesOutputBridge) HasOutput() bool           { return len(s.items) > 0 }
func (s *ResponsesOutputBridge) CurrentOutputText() string {
	if s.text < 0 || s.text != len(s.items)-1 {
		return ""
	}
	return s.items[s.text]["content"].([]map[string]any)[0]["text"].(string)
}

func (s *ResponsesOutputBridge) Response(status string, failure any) map[string]any {
	response := map[string]any{"id": s.id, "object": "response", "created_at": s.created, "model": s.model, "status": status, "output": s.items, "error": failure, "usage": s.usage, "incomplete_details": nil}
	if status == "incomplete" {
		reason := "max_output_tokens"
		if s.finishReason == "content_filter" {
			reason = "content_filter"
		}
		response["incomplete_details"] = map[string]any{"reason": reason}
	}
	return response
}

func (s *ResponsesOutputBridge) addItem(item map[string]any) (int, error) {
	index := len(s.items)
	if item["id"] == nil || item["id"] == "" {
		item["id"] = fmt.Sprintf("item_%s_%d", s.id, index)
	}
	item["status"] = "in_progress"
	s.items = append(s.items, item)
	return index, s.event("response.output_item.added", map[string]any{"output_index": index, "item": item})
}

func (s *ResponsesOutputBridge) appendText(kind, text string) error {
	if text == "" {
		return nil
	}
	s.counted.WriteString(text)
	index := &s.text
	partType, collection, prefix, valueKey, positionKey := "output_text", "content", "response.output_text", "text", "content_index"
	if kind == "reasoning" {
		index, partType, collection, prefix, positionKey = &s.reasoning, "summary_text", "summary", "response.reasoning_summary_text", "summary_index"
	} else if kind == "refusal" {
		index, partType, prefix, valueKey = &s.refusal, "refusal", "response.refusal", "refusal"
	}
	if *index < 0 || *index != len(s.items)-1 {
		item := map[string]any{"type": "message", "role": "assistant", collection: []map[string]any{}}
		if kind == "reasoning" {
			item = map[string]any{"type": "reasoning", "summary": []map[string]any{}}
		}
		var err error
		*index, err = s.addItem(item)
		if err != nil {
			return err
		}
		part := map[string]any{"type": partType, valueKey: ""}
		if kind == "text" {
			part["annotations"] = []any{}
		}
		item[collection] = []map[string]any{part}
		partEvent := "response.content_part.added"
		if kind == "reasoning" {
			partEvent = "response.reasoning_summary_part.added"
		}
		if err := s.event(partEvent, map[string]any{"item_id": item["id"], "output_index": *index, positionKey: 0, "part": part}); err != nil {
			return err
		}
	}
	item := s.items[*index]
	part := item[collection].([]map[string]any)[0]
	part[valueKey] = part[valueKey].(string) + text
	return s.event(prefix+".delta", map[string]any{"item_id": item["id"], "output_index": *index, positionKey: 0, "delta": text})
}

func (s *ResponsesOutputBridge) addAnnotations(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var annotations []map[string]any
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return err
	}
	if s.text < 0 && len(annotations) > 0 {
		return fmt.Errorf("annotations have no output text")
	}
	for _, annotation := range annotations {
		if nested, ok := annotation["url_citation"].(map[string]any); ok {
			nested["type"] = "url_citation"
			annotation = nested
		}
		item := s.items[s.text]
		part := item["content"].([]map[string]any)[0]
		list := part["annotations"].([]any)
		part["annotations"] = append(list, annotation)
		if err := s.event("response.output_text.annotation.added", map[string]any{"item_id": item["id"], "output_index": s.text, "content_index": 0, "annotation_index": len(list), "annotation": annotation}); err != nil {
			return err
		}
	}
	return nil
}

func (s *ResponsesOutputBridge) tool(delta dto.ToolCallResponse, fallback int) error {
	key := fallback
	if delta.Index != nil {
		key = *delta.Index
	}
	index, exists := s.calls[key]
	if !exists {
		if delta.Function.Name == "" {
			return fmt.Errorf("tool call %d starts without a name", key)
		}
		kind := "function_call"
		if s.tools[delta.Function.Name] {
			kind = "custom_tool_call"
		}
		id := delta.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", s.id, key)
		}
		item := map[string]any{"type": kind, "call_id": id, "name": delta.Function.Name, "arguments": ""}
		if kind == "custom_tool_call" {
			item["input"] = ""
			delete(item, "arguments")
		}
		var err error
		index, err = s.addItem(item)
		if err != nil {
			return err
		}
		s.calls[key] = index
	}
	item := s.items[index]
	if delta.ID != "" && delta.ID != item["call_id"] {
		return fmt.Errorf("tool call %d changed id", key)
	}
	if delta.Function.Name != "" && delta.Function.Name != item["name"] {
		return fmt.Errorf("tool call %d changed name", key)
	}
	args := delta.Function.Arguments
	s.counted.WriteString(args)
	if item["type"] == "custom_tool_call" {
		s.customArgs[index] += args
		return s.updateCustomInput(index, false)
	}
	item["arguments"] = item["arguments"].(string) + args
	if args == "" {
		return nil
	}
	return s.event("response.function_call_arguments.delta", map[string]any{"item_id": item["id"], "output_index": index, "delta": args})
}

// updateCustomInput 仅输出已完整解码的 JSON 字符，跨帧转义和代理对不会泄漏包装内容。
func (s *ResponsesOutputBridge) updateCustomInput(index int, complete bool) error {
	item := s.items[index]
	input, err := responsesCustomInput(s.customArgs[index], complete)
	if err != nil {
		return fmt.Errorf("custom tool %v: %w", item["name"], err)
	}
	previous := item["input"].(string)
	if !strings.HasPrefix(input, previous) {
		return fmt.Errorf("custom tool %v changed input", item["name"])
	}
	item["input"] = input
	if len(input) == len(previous) {
		return nil
	}
	return s.event("response.custom_tool_call_input.delta", map[string]any{"item_id": item["id"], "output_index": index, "delta": input[len(previous):]})
}

func responsesCustomInput(raw string, complete bool) (string, error) {
	var wrapper struct {
		Input *string `json:"input"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) == nil && wrapper.Input != nil {
		return *wrapper.Input, nil
	}
	if complete {
		return "", fmt.Errorf("invalid input wrapper")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", nil
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", nil
		}
		if key != "input" {
			var ignored json.RawMessage
			if decoder.Decode(&ignored) != nil {
				return "", nil
			}
			continue
		}
		rest := strings.TrimSpace(raw[decoder.InputOffset():])
		if !strings.HasPrefix(rest, ":") {
			return "", nil
		}
		rest = strings.TrimSpace(rest[1:])
		if !strings.HasPrefix(rest, `"`) {
			return "", nil
		}
		rest = rest[1:]
		end := 0
		for end < len(rest) {
			if rest[end] == '"' {
				break
			}
			if rest[end] == '\\' {
				if end+1 >= len(rest) {
					break
				}
				width := 2
				if rest[end+1] == 'u' {
					if end+6 > len(rest) {
						break
					}
					value, err := strconv.ParseUint(rest[end+2:end+6], 16, 16)
					if err != nil {
						return "", err
					}
					width = 6
					if value >= 0xd800 && value <= 0xdbff {
						if end+12 > len(rest) {
							break
						}
						width = 12
					}
				}
				end += width
			} else {
				if !utf8.FullRuneInString(rest[end:]) {
					break
				}
				_, width := utf8.DecodeRuneInString(rest[end:])
				end += width
			}
		}
		var input string
		if err := json.Unmarshal([]byte(`"`+rest[:end]+`"`), &input); err != nil {
			return "", err
		}
		return input, nil
	}
	return "", nil
}

func (s *ResponsesOutputBridge) Feed(chat *dto.ChatCompletionsStreamResponse) error {
	if chat == nil || s.finished {
		return nil
	}
	if chat.Usage != nil {
		s.SetUsage(chat.Usage)
	}
	if err := s.Start(); err != nil {
		return err
	}
	for _, choice := range chat.Choices {
		if choice.Index != 0 {
			return fmt.Errorf("Responses bridge accepts only one output candidate")
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finishReason = *choice.FinishReason
		}
		if err := s.appendText("reasoning", choice.Delta.GetReasoningContent()); err != nil {
			return err
		}
		if err := s.appendText("text", choice.Delta.GetContentString()); err != nil {
			return err
		}
		if choice.Delta.Refusal != nil {
			if err := s.appendText("refusal", *choice.Delta.Refusal); err != nil {
				return err
			}
		}
		for index, call := range choice.Delta.ToolCalls {
			if err := s.tool(call, index); err != nil {
				return err
			}
		}
		if err := s.addAnnotations(choice.Delta.Annotations); err != nil {
			return err
		}
	}
	return nil
}

func (s *ResponsesOutputBridge) FeedFull(chat *dto.OpenAITextResponse) error {
	if chat == nil {
		return fmt.Errorf("empty chat response")
	}
	s.SetUsage(&chat.Usage)
	chunk := &dto.ChatCompletionsStreamResponse{}
	for _, choice := range chat.Choices {
		delta := dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: choice.ReasoningContent, Reasoning: choice.Reasoning, Annotations: choice.Annotations, Refusal: choice.Refusal}
		for _, call := range choice.ParseToolCalls() {
			delta.ToolCalls = append(delta.ToolCalls, dto.ToolCallResponse{ID: call.ID, Type: call.Type, Function: dto.FunctionResponse{Name: call.Function.Name, Arguments: call.Function.Arguments}})
		}
		text := choice.StringContent()
		delta.Content = &text
		finish := choice.FinishReason
		chunk.Choices = append(chunk.Choices, dto.ChatCompletionsStreamResponseChoice{Index: choice.Index, Delta: delta, FinishReason: &finish})
	}
	return s.Feed(chunk)
}

// AddHosted 保存原生 hosted 结果与 action，避免将它们当成需要客户端执行的函数。
func (s *ResponsesOutputBridge) AddHosted(item map[string]any) error {
	if err := s.Start(); err != nil {
		return err
	}
	if callID, ok := item["call_id"].(string); ok && callID != "" {
		for index, existing := range s.items {
			if existing["id"] != callID {
				continue
			}
			existing["results"] = item["results"]
			if existing["type"] == "web_search_call" {
				return s.event("response.web_search_call.completed", map[string]any{"item_id": existing["id"], "output_index": index})
			}
			return nil
		}
	}
	index, err := s.addItem(item)
	if err != nil {
		return err
	}
	if item["type"] == "web_search_call" {
		for _, phase := range []string{"in_progress", "searching"} {
			if err := s.event("response.web_search_call."+phase, map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ResponsesOutputBridge) Complete() (map[string]any, error) {
	status := "completed"
	if s.finishReason == "length" || s.finishReason == "content_filter" {
		status = "incomplete"
	}
	if s.finished {
		return s.Response(status, nil), nil
	}
	if err := s.Start(); err != nil {
		return nil, err
	}
	for index, item := range s.items {
		base := func() map[string]any { return map[string]any{"item_id": item["id"], "output_index": index} }
		switch item["type"] {
		case "function_call", "custom_tool_call":
			kind, field := "response.function_call_arguments", "arguments"
			if item["type"] == "custom_tool_call" {
				if err := s.updateCustomInput(index, status == "completed"); err != nil {
					return nil, err
				}
				kind, field = "response.custom_tool_call_input", "input"
			} else if status == "completed" {
				var arguments map[string]json.RawMessage
				if err := json.Unmarshal([]byte(item["arguments"].(string)), &arguments); err != nil || arguments == nil {
					return nil, fmt.Errorf("function %v returned invalid JSON arguments", item["name"])
				}
			}
			fields := base()
			fields[field] = item[field]
			if err := s.event(kind+".done", fields); err != nil {
				return nil, err
			}
		case "message", "reasoning":
			collection, position, partPrefix := "content", "content_index", "response.content_part"
			if item["type"] == "reasoning" {
				collection, position, partPrefix = "summary", "summary_index", "response.reasoning_summary_part"
			}
			for partIndex, part := range item[collection].([]map[string]any) {
				prefix, field := "response.output_text", "text"
				if part["type"] == "summary_text" {
					prefix = "response.reasoning_summary_text"
				}
				if part["type"] == "refusal" {
					prefix, field = "response.refusal", "refusal"
				}
				fields := base()
				fields[position], fields[field] = partIndex, part[field]
				if err := s.event(prefix+".done", fields); err != nil {
					return nil, err
				}
				fields = base()
				fields[position], fields["part"] = partIndex, part
				if err := s.event(partPrefix+".done", fields); err != nil {
					return nil, err
				}
			}
		}
		item["status"] = status
		if err := s.event("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return nil, err
		}
	}
	response := s.Response(status, nil)
	if err := s.event("response."+status, map[string]any{"response": response}); err != nil {
		return nil, err
	}
	s.finished = true
	return response, nil
}

func (s *ResponsesOutputBridge) Fail(failure any) error {
	if s.finished {
		return nil
	}
	if err := s.Start(); err != nil {
		return err
	}
	for _, item := range s.items {
		item["status"] = "incomplete"
	}
	fields := map[string]any{"error": failure}
	if data, err := json.Marshal(failure); err == nil {
		var detail map[string]any
		if json.Unmarshal(data, &detail) == nil {
			for _, key := range []string{"code", "message", "param"} {
				if value, exists := detail[key]; exists {
					fields[key] = value
				}
			}
		}
	}
	if err := s.event("error", fields); err != nil {
		return err
	}
	if err := s.event("response.failed", map[string]any{"response": s.Response("failed", failure)}); err != nil {
		return err
	}
	s.finished = true
	return nil
}
