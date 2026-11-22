package openaicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// ResponsesTools 保存桥接前的工具类型，供返回时还原 custom_tool_call。
type ResponsesTools map[string]bool

func responsesValue(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// ResponsesRequestToChat 把无服务端会话状态的 Responses 请求交给既有 Chat 转换器。
// 无法表示的数据必须在派发前报错，不能把一个有输入的请求变成空请求。
func ResponsesRequestToChat(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, ResponsesTools, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("Responses request is nil")
	}
	for name, raw := range map[string]json.RawMessage{"conversation": req.Conversation, "context_management": req.ContextManagement, "prompt": req.Prompt, "preset": req.Preset} {
		if responsesValue(raw) {
			return nil, nil, fmt.Errorf("Responses %s cannot be resolved by this protocol bridge", name)
		}
	}
	if req.PreviousResponseID != "" {
		return nil, nil, fmt.Errorf("Responses previous_response_id requires the original Responses provider; send the complete input history")
	}
	if req.MaxToolCalls != nil {
		return nil, nil, fmt.Errorf("Responses max_tool_calls is not supported by this protocol bridge")
	}
	if responsesValue(req.Truncation) && string(req.Truncation) != `"disabled"` {
		return nil, nil, fmt.Errorf("Responses automatic truncation is not supported by this protocol bridge")
	}
	chat := &dto.GeneralOpenAIRequest{
		Model: req.Model, Stream: req.Stream, StreamOptions: req.StreamOptions,
		MaxCompletionTokens: req.MaxOutputTokens, Temperature: req.Temperature, TopP: req.TopP,
		TopLogProbs: req.TopLogProbs, Metadata: req.Metadata, Store: req.Store, User: req.User,
		SafetyIdentifier: req.SafetyIdentifier, PromptCacheRetention: req.PromptCacheRetention,
		PromptCacheOptions: req.PromptCacheOptions, EnableThinking: req.EnableThinking, ThinkingBudget: req.ThinkingBudget,
	}
	if req.ServiceTier != "" {
		chat.ServiceTier, _ = json.Marshal(req.ServiceTier)
	}
	if req.Reasoning != nil {
		chat.ReasoningEffort = req.Reasoning.Effort
		chat.Reasoning, _ = json.Marshal(req.Reasoning)
	}
	if responsesValue(req.ParallelToolCalls) {
		var parallel bool
		if err := json.Unmarshal(req.ParallelToolCalls, &parallel); err != nil {
			return nil, nil, fmt.Errorf("parallel_tool_calls must be a boolean: %w", err)
		}
		chat.ParallelTooCalls = &parallel
	}
	if responsesValue(req.PromptCacheKey) {
		if err := json.Unmarshal(req.PromptCacheKey, &chat.PromptCacheKey); err != nil {
			return nil, nil, fmt.Errorf("prompt_cache_key must be a string: %w", err)
		}
	}
	if responsesValue(req.Instructions) {
		var instructions string
		if err := json.Unmarshal(req.Instructions, &instructions); err != nil {
			return nil, nil, fmt.Errorf("instructions must be a string: %w", err)
		}
		chat.Messages = append(chat.Messages, dto.Message{Role: "system", Content: instructions})
	}
	tools := ResponsesTools{}
	if responsesValue(req.Tools) {
		var definitions []map[string]json.RawMessage
		if err := json.Unmarshal(req.Tools, &definitions); err != nil {
			return nil, nil, fmt.Errorf("invalid Responses tools: %w", err)
		}
		for _, definition := range definitions {
			var kind string
			if err := json.Unmarshal(definition["type"], &kind); err != nil {
				return nil, nil, fmt.Errorf("tool type is required")
			}
			switch kind {
			case "function", "custom":
				encoded, _ := json.Marshal(definition)
				var function dto.FunctionRequest
				if err := json.Unmarshal(encoded, &function); err != nil || function.Name == "" {
					return nil, nil, fmt.Errorf("Responses tool must have a valid name and function schema")
				}
				if _, exists := tools[function.Name]; exists {
					return nil, nil, fmt.Errorf("duplicate Responses tool name %q", function.Name)
				}
				tools[function.Name] = kind == "custom"
				if kind == "custom" {
					if raw := definition["format"]; responsesValue(raw) {
						var format struct {
							Type string `json:"type"`
						}
						if json.Unmarshal(raw, &format) != nil || (format.Type != "" && format.Type != "text") {
							return nil, nil, fmt.Errorf("custom tool %q grammar cannot be enforced by this protocol bridge", function.Name)
						}
					}
					function.Parameters = map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []string{"input"}, "additionalProperties": false}
				}
				chat.Tools = append(chat.Tools, dto.ToolCallRequest{Type: "function", Function: function})
			default:
				encoded, _ := json.Marshal(definition)
				var tool dto.ToolCallRequest
				if err := json.Unmarshal(encoded, &tool); err != nil {
					return nil, nil, err
				}
				chat.Tools = append(chat.Tools, tool)
			}
		}
	}
	if responsesValue(req.ToolChoice) {
		var choice any
		if err := json.Unmarshal(req.ToolChoice, &choice); err != nil {
			return nil, nil, err
		}
		if object, ok := choice.(map[string]any); ok {
			kind, _ := object["type"].(string)
			if kind == "function" || kind == "custom" {
				name, _ := object["name"].(string)
				if name == "" {
					return nil, nil, fmt.Errorf("named tool_choice requires name")
				}
				choice = map[string]any{"type": "function", "function": map[string]any{"name": name}}
			} else {
				return nil, nil, fmt.Errorf("Responses tool_choice type %q is not supported by this protocol bridge", kind)
			}
		}
		chat.ToolChoice = choice
	}
	if responsesValue(req.Text) {
		var text struct {
			Format    map[string]json.RawMessage `json:"format"`
			Verbosity json.RawMessage            `json:"verbosity"`
		}
		if err := json.Unmarshal(req.Text, &text); err != nil {
			return nil, nil, err
		}
		chat.Verbosity = text.Verbosity
		if len(text.Format) != 0 {
			var kind string
			if err := json.Unmarshal(text.Format["type"], &kind); err != nil {
				return nil, nil, fmt.Errorf("text.format.type is required")
			}
			switch kind {
			case "text":
			case "json_object":
				chat.ResponseFormat = &dto.ResponseFormat{Type: kind}
			case "json_schema":
				if !responsesValue(text.Format["schema"]) {
					return nil, nil, fmt.Errorf("text.format.schema is required")
				}
				delete(text.Format, "type")
				schema, _ := json.Marshal(text.Format)
				chat.ResponseFormat = &dto.ResponseFormat{Type: kind, JsonSchema: schema}
			default:
				return nil, nil, fmt.Errorf("unsupported text.format.type %q", kind)
			}
		}
	}
	var input string
	if json.Unmarshal(req.Input, &input) == nil && responsesValue(req.Input) {
		chat.Messages = append(chat.Messages, dto.Message{Role: "user", Content: input})
		return chat, tools, nil
	}
	var items []responsesInputItem
	if responsesValue(req.Input) {
		if err := json.Unmarshal(req.Input, &items); err != nil {
			return nil, nil, fmt.Errorf("Responses input must be a string or an array: %w", err)
		}
	}
	var pendingMedia []dto.MediaContent
	flushMedia := func() {
		if len(pendingMedia) > 0 {
			chat.Messages = append(chat.Messages, responsesMessage("user", pendingMedia))
			pendingMedia = nil
		}
	}
	for _, item := range items {
		if item.Type != "function_call_output" && item.Type != "custom_tool_call_output" {
			flushMedia()
		}
		switch item.Type {
		case "", "message":
			role := item.Role
			if role == "developer" {
				role = "system"
			}
			if role != "system" && role != "user" && role != "assistant" {
				return nil, nil, fmt.Errorf("unsupported Responses message role %q", item.Role)
			}
			content, err := responsesContent(item.Content)
			if err != nil {
				return nil, nil, err
			}
			message := responsesMessage(role, content)
			// reasoning 与后续 assistant 内容属于同一轮，原生转换器据此保留工具历史。
			if role == "assistant" && len(chat.Messages) > 0 {
				last := &chat.Messages[len(chat.Messages)-1]
				if last.Role == "assistant" && last.ReasoningContent != nil && last.StringContent() == "" && len(last.ParseContent()) <= 1 && len(last.ParseToolCalls()) == 0 {
					message.ReasoningContent = last.ReasoningContent
					*last = message
					continue
				}
			}
			chat.Messages = append(chat.Messages, message)
		case "function_call", "custom_tool_call":
			if item.CallID == "" || item.Name == "" {
				return nil, nil, fmt.Errorf("tool call requires call_id and name")
			}
			args := item.Arguments
			if item.Type == "custom_tool_call" {
				if custom, exists := tools[item.Name]; exists && !custom {
					return nil, nil, fmt.Errorf("tool %q history conflicts with its definition", item.Name)
				}
				tools[item.Name] = true
				encoded, _ := json.Marshal(map[string]string{"input": item.Input})
				args = string(encoded)
			} else {
				var object map[string]json.RawMessage
				if json.Unmarshal([]byte(args), &object) != nil || object == nil {
					return nil, nil, fmt.Errorf("function %q arguments must be a JSON object", item.Name)
				}
			}
			call := dto.ToolCallRequest{ID: item.CallID, Type: "function", Function: dto.FunctionRequest{Name: item.Name, Arguments: args}}
			if len(chat.Messages) > 0 && chat.Messages[len(chat.Messages)-1].Role == "assistant" {
				last := &chat.Messages[len(chat.Messages)-1]
				last.SetToolCalls(append(last.ParseToolCalls(), call))
			} else {
				message := dto.Message{Role: "assistant", Content: ""}
				message.SetToolCalls([]dto.ToolCallRequest{call})
				chat.Messages = append(chat.Messages, message)
			}
		case "function_call_output", "custom_tool_call_output":
			if item.CallID == "" {
				return nil, nil, fmt.Errorf("tool output requires call_id")
			}
			content, err := responsesContent(item.Output)
			if err != nil {
				return nil, nil, err
			}
			var text strings.Builder
			for _, part := range content {
				if part.Type == "text" {
					text.WriteString(part.Text)
				} else {
					pendingMedia = append(pendingMedia, part)
				}
			}
			chat.Messages = append(chat.Messages, dto.Message{Role: "tool", ToolCallId: item.CallID, Content: text.String()})
		case "reasoning":
			if responsesValue(item.EncryptedContent) {
				return nil, nil, fmt.Errorf("encrypted reasoning cannot be replayed across providers")
			}
			var summary []struct {
				Text string `json:"text"`
			}
			if responsesValue(item.Summary) && json.Unmarshal(item.Summary, &summary) != nil {
				return nil, nil, fmt.Errorf("invalid reasoning summary")
			}
			var text strings.Builder
			for _, part := range summary {
				text.WriteString(part.Text)
			}
			if text.Len() > 0 {
				reasoning := text.String()
				chat.Messages = append(chat.Messages, dto.Message{Role: "assistant", Content: "", ReasoningContent: &reasoning})
			}
		default:
			return nil, nil, fmt.Errorf("Responses input item %q cannot be represented by this protocol bridge", item.Type)
		}
	}
	flushMedia()
	return chat, tools, nil
}

// Message.ParseContent 的缓存必须通过 SetMediaContent 初始化；纯文本保持 string，
// 兼容既有 Claude/Gemini 的 system 与 tool 分支。
func responsesMessage(role string, content []dto.MediaContent) dto.Message {
	message := dto.Message{Role: role}
	var text strings.Builder
	for _, part := range content {
		if part.Type != dto.ContentTypeText {
			message.SetMediaContent(content)
			return message
		}
		text.WriteString(part.Text)
	}
	message.Content = text.String()
	return message
}

type responsesInputItem struct {
	Type             string          `json:"type"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Input            string          `json:"input"`
	Output           json.RawMessage `json:"output"`
	Summary          json.RawMessage `json:"summary"`
	EncryptedContent json.RawMessage `json:"encrypted_content"`
}

func responsesContent(raw json.RawMessage) ([]dto.MediaContent, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil && responsesValue(raw) {
		return []dto.MediaContent{{Type: "text", Text: text}}, nil
	}
	if !responsesValue(raw) {
		return nil, nil
	}
	var parts []struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		Refusal    string `json:"refusal"`
		ImageURL   string `json:"image_url"`
		Detail     string `json:"detail"`
		Filename   string `json:"filename"`
		FileData   string `json:"file_data"`
		FileURL    string `json:"file_url"`
		FileID     string `json:"file_id"`
		InputAudio any    `json:"input_audio"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("invalid Responses content: %w", err)
	}
	result := make([]dto.MediaContent, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			result = append(result, dto.MediaContent{Type: "text", Text: part.Text})
		case "refusal":
			result = append(result, dto.MediaContent{Type: "text", Text: part.Refusal})
		case "input_image":
			if part.ImageURL == "" {
				return nil, fmt.Errorf("input_image requires image_url; provider file_id cannot be resolved across protocols")
			}
			result = append(result, dto.MediaContent{Type: "image_url", ImageUrl: map[string]any{"url": part.ImageURL, "detail": part.Detail}})
		case "input_file":
			if part.FileData == "" && part.FileURL == "" && part.FileID == "" {
				return nil, fmt.Errorf("input_file requires file_data, file_url or file_id")
			}
			result = append(result, dto.MediaContent{Type: "file", File: &dto.MessageFile{FileName: part.Filename, FileData: part.FileData, FileUrl: part.FileURL, FileId: part.FileID}})
		case "input_audio":
			result = append(result, dto.MediaContent{Type: "input_audio", InputAudio: part.InputAudio})
		default:
			return nil, fmt.Errorf("unsupported Responses content type %q", part.Type)
		}
	}
	return result, nil
}
