package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/openaicompat"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func convertResponsesToClaude(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	chat, err := channel.ResponsesBridgeRequest(c, &request)
	if err != nil {
		return nil, err
	}
	if err := channel.ValidateResponsesBridgeMedia(chat); err != nil {
		return nil, err
	}
	if err := helper.ApplyReasoningModelSuffix(c, info, chat); err != nil {
		return nil, err
	}
	// 原转换器按文件名推断 MIME；data URL 和 URL 路径也能提供文件类型。
	for index := range chat.Messages {
		message := &chat.Messages[index]
		if message.IsStringContent() {
			continue
		}
		parts := message.ParseContent()
		for i := range parts {
			part := &parts[i]
			if part.Type == dto.ContentTypeInputAudio || part.Type == dto.ContentTypeVideoUrl {
				return nil, fmt.Errorf("Claude Responses bridge does not support %s", part.Type)
			}
			if part.Type != dto.ContentTypeFile {
				continue
			}
			file := part.GetFile()
			if file.FileName == "" && file.FileUrl != "" {
				if parsed, parseErr := url.Parse(file.FileUrl); parseErr == nil {
					file.FileName = path.Base(parsed.Path)
				}
			}
			mime := service.GetMimeTypeByExtension(strings.TrimPrefix(path.Ext(file.FileName), "."))
			if (mime == "" || mime == "application/octet-stream") && strings.HasPrefix(file.FileData, "data:") {
				if end := strings.IndexByte(file.FileData, ';'); end > 5 {
					mime = file.FileData[5:end]
				}
				switch {
				case mime == "application/pdf":
					file.FileName = "document.pdf"
				case strings.HasPrefix(mime, "text/"):
					file.FileName = "document.txt"
				case strings.HasPrefix(mime, "image/"):
					file.FileName = "image." + strings.TrimPrefix(mime, "image/")
				}
			}
			if mime != "application/pdf" && !strings.HasPrefix(mime, "text/") && !strings.HasPrefix(mime, "image/") {
				return nil, fmt.Errorf("Claude file %q must be PDF, text or image with a known MIME type", file.FileName)
			}
			part.File = file
		}
		// 原生转换器重建 Message 时只复制 Content，不保留媒体解析缓存。
		// 使用 ParseContent 支持的 []any，让文件、图片和同条正文一起传递。
		content := make([]any, len(parts))
		for i := range parts {
			content[i] = parts[i]
		}
		message.Content = content
	}
	converted, err := RequestOpenAI2ClaudeMessage(c, *chat)
	if err != nil {
		return nil, err
	}
	if chat.ResponseFormat != nil {
		if chat.ResponseFormat.Type != "json_schema" {
			return nil, fmt.Errorf("Claude structured output requires text.format.type json_schema")
		}
		var schema dto.FormatJsonSchema
		if err := json.Unmarshal(chat.ResponseFormat.JsonSchema, &schema); err != nil || schema.Schema == nil {
			return nil, fmt.Errorf("text.format.schema is required")
		}
		config := map[string]any{}
		if len(converted.OutputConfig) > 0 {
			if err := json.Unmarshal(converted.OutputConfig, &config); err != nil {
				return nil, err
			}
		}
		config["format"] = map[string]any{"type": "json_schema", "schema": schema.Schema}
		converted.OutputConfig, err = json.Marshal(config)
		if err != nil {
			return nil, err
		}
	}
	return converted, nil
}

func captureResponsesClaudeUsage(c *gin.Context, response *dto.ClaudeResponse, accumulator *ClaudeResponseInfo) {
	FormatClaudeResponseInfo(response, nil, accumulator)
	usage := response.Usage
	if response.Message != nil && response.Message.Usage != nil {
		usage = response.Message.Usage
	}
	if usage != nil {
		if accumulator.Usage == nil {
			accumulator.Usage = &dto.Usage{}
		}
		accumulator.Usage.UsageSemantic = "anthropic"
		if usage.CacheCreationInputTokens == 0 && usage.GetCacheCreationTotalTokens() > 0 {
			accumulator.Usage.PromptTokensDetails.CachedCreationTokens = usage.GetCacheCreationTotalTokens()
		}
		if usage.InputTokens > 0 {
			accumulator.Usage.PromptTokens = usage.InputTokens
		}
		if usage.OutputTokens > 0 {
			accumulator.Usage.CompletionTokens = usage.OutputTokens
		}
		if usage.CacheReadInputTokens > 0 {
			accumulator.Usage.PromptTokensDetails.CachedTokens = usage.CacheReadInputTokens
		}
		if usage.CacheCreationInputTokens > 0 {
			accumulator.Usage.PromptTokensDetails.CachedCreationTokens = usage.CacheCreationInputTokens
		}
		if value := usage.GetCacheCreation5mTokens(); value > 0 {
			accumulator.Usage.ClaudeCacheCreation5mTokens = value
		}
		if value := usage.GetCacheCreation1hTokens(); value > 0 {
			accumulator.Usage.ClaudeCacheCreation1hTokens = value
		}
		if usage.ServerToolUse != nil && usage.ServerToolUse.WebSearchRequests > 0 {
			c.Set("claude_web_search_requests", usage.ServerToolUse.WebSearchRequests)
			if accumulator.Usage.ToolUsage == nil {
				accumulator.Usage.ToolUsage = map[string]int{}
			}
			accumulator.Usage.ToolUsage["web_search"] = usage.ServerToolUse.WebSearchRequests
		}
		accumulator.Usage.TotalTokens = accumulator.Usage.PromptTokens + accumulator.Usage.CompletionTokens
	}
}

func ClaudeResponsesHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	usage := &dto.Usage{}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	var response dto.ClaudeResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	accumulator := &ClaudeResponseInfo{Usage: usage, Done: true}
	captureResponsesClaudeUsage(c, &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Usage: response.Usage}}, accumulator)
	if upstream := response.GetClaudeError(); upstream != nil {
		return usage, types.WithClaudeError(*upstream, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	maybeMarkClaudeRefusal(c, response.StopReason)
	state := channel.NewResponsesBridge(c, info, false)
	if err := feedClaudeResponses(state, &response); err != nil {
		return usage, channel.ResponsesBridgeError(err)
	}
	accumulator.ResponseText.WriteString(state.CountedText())
	if response.Usage == nil || state.HasOutput() {
		HandleStreamFinalResponse(c, info, accumulator)
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	display := buildOpenAIStyleUsageFromClaudeUsage(usage)
	return usage, channel.CompleteResponsesBridge(c, resp, info, state, &display, false)
}

func feedClaudeResponses(state *openaicompat.ResponsesOutputBridge, response *dto.ClaudeResponse) error {
	toolIndex := 0
	for _, block := range response.Content {
		if block.Type == "server_tool_use" || strings.HasSuffix(block.Type, "_tool_result") {
			if err := state.AddHosted(claudeResponsesHostedItem(block)); err != nil {
				return err
			}
			continue
		}
		if block.Type == "tool_use" && block.Input == nil {
			block.Input = map[string]any{}
		}
		chat := ResponseClaude2OpenAI(&dto.ClaudeResponse{Content: []dto.ClaudeMediaMessage{block}})
		for _, choice := range chat.Choices {
			text := choice.Message.StringContent()
			delta := dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text, ReasoningContent: choice.Message.ReasoningContent}
			if len(block.Citations) > 0 {
				delta.Annotations, _ = json.Marshal(claudeCitationsToChat(block.Citations, block.GetText(), state.CurrentOutputText()))
			}
			for _, call := range choice.Message.ParseToolCalls() {
				index := toolIndex
				toolIndex++
				delta.ToolCalls = append(delta.ToolCalls, dto.ToolCallResponse{Index: &index, ID: call.ID, Type: call.Type, Function: dto.FunctionResponse{Name: call.Function.Name, Arguments: call.Function.Arguments}})
			}
			if err := state.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta}}}); err != nil {
				return err
			}
		}
	}
	finish := stopReasonClaude2OpenAI(response.StopReason)
	return state.Feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finish}}})
}

func claudeResponsesHostedItem(block dto.ClaudeMediaMessage) map[string]any {
	item := map[string]any{"type": "web_search_call", "id": block.Id}
	if block.Type == "server_tool_use" {
		item["action"] = map[string]any{"type": "search", "input": block.Input}
		if input, ok := block.Input.(map[string]any); ok {
			if query, exists := input["query"]; exists {
				item["action"] = map[string]any{"type": "search", "query": query}
			}
		}
	} else {
		item["id"] = block.ToolUseId + "_result"
		item["call_id"], item["results"] = block.ToolUseId, block.Content
	}
	return item
}

func ClaudeResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer helper.FinalizeThinkingStream(c)
	state := channel.NewResponsesBridge(c, info, true)
	accumulator := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	var streamErr *types.NewAPIError
	stopped, seen := false, false
	hosted := map[int]*dto.ClaudeMediaMessage{}
	hostedArgs := map[int]string{}
	toolArgs := map[int]bool{}
	initialArgs := map[int]string{}
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		fail := func(err error) { streamErr = channel.ResponsesBridgeError(err); sr.Stop(streamErr) }
		var response dto.ClaudeResponse
		if err := json.Unmarshal([]byte(data), &response); err != nil {
			fail(err)
			return
		}
		seen = true
		captureResponsesClaudeUsage(c, &response, accumulator)
		if upstream := response.GetClaudeError(); upstream != nil {
			streamErr = types.WithClaudeError(*upstream, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		index := response.GetIndex()
		if response.Type == "message_stop" {
			stopped = true
			sr.Done()
			return
		}
		if response.Type == "message_delta" && response.Delta != nil && response.Delta.StopReason != nil {
			maybeMarkClaudeRefusal(c, *response.Delta.StopReason)
		}
		if response.Type == "content_block_start" && response.ContentBlock != nil {
			block := response.ContentBlock
			if block.Type == "server_tool_use" || strings.HasSuffix(block.Type, "_tool_result") {
				hosted[index] = block
				return
			}
			if block.Type == "tool_use" {
				encoded, err := json.Marshal(block.Input)
				if err != nil {
					fail(err)
					return
				}
				if block.Input == nil {
					encoded = []byte("{}")
				}
				initialArgs[index], toolArgs[index] = string(encoded), false
			}
		}
		if response.Type == "content_block_delta" && response.Delta != nil && response.Delta.Type == "input_json_delta" {
			if response.Delta.PartialJson == nil {
				fail(fmt.Errorf("Claude input_json_delta has no partial_json"))
				return
			}
			if hosted[index] != nil {
				hostedArgs[index] += *response.Delta.PartialJson
				accumulator.ResponseText.WriteString(*response.Delta.PartialJson)
				return
			}
			toolArgs[index] = true
			accumulator.ResponseText.WriteString(*response.Delta.PartialJson)
		}
		if response.Type == "content_block_stop" {
			if block := hosted[index]; block != nil {
				if raw := hostedArgs[index]; raw != "" {
					if err := json.Unmarshal([]byte(raw), &block.Input); err != nil {
						fail(err)
						return
					}
				}
				if err := state.AddHosted(claudeResponsesHostedItem(*block)); err != nil {
					fail(err)
					return
				}
				delete(hosted, index)
				return
			}
			if hasDelta, exists := toolArgs[index]; exists && !hasDelta {
				args := initialArgs[index]
				chunk := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Arguments: args}}}}}}}
				accumulator.ResponseText.WriteString(args)
				if err := state.Feed(chunk); err != nil {
					fail(err)
					return
				}
			}
			delete(toolArgs, index)
			return
		}
		chunk := StreamResponseClaude2OpenAI(&response)
		if response.Type == "content_block_delta" && response.Delta != nil && response.Delta.Type == "citations_delta" {
			var envelope struct {
				Delta struct {
					Citation json.RawMessage `json:"citation"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &envelope); err != nil {
				fail(err)
				return
			}
			if len(envelope.Delta.Citation) > 0 && chunk != nil && len(chunk.Choices) > 0 {
				chunk.Choices[0].Delta.Annotations, _ = json.Marshal(claudeCitationsToChat([]json.RawMessage{envelope.Delta.Citation}, state.CurrentOutputText(), ""))
			}
		}
		if response.Type == "content_block_start" && response.ContentBlock != nil && chunk != nil && len(chunk.Choices) > 0 {
			block := response.ContentBlock
			if block.Thinking != nil {
				chunk.Choices[0].Delta.ReasoningContent = block.Thinking
				accumulator.ResponseText.WriteString(*block.Thinking)
			}
			if block.Text != nil {
				accumulator.ResponseText.WriteString(*block.Text)
			}
			if len(block.Citations) > 0 {
				chunk.Choices[0].Delta.Annotations, _ = json.Marshal(claudeCitationsToChat(block.Citations, block.GetText(), state.CurrentOutputText()))
			}
		}
		if err := state.Feed(chunk); err != nil {
			fail(err)
		}
	})
	if seen {
		HandleStreamFinalResponse(c, info, accumulator)
	}
	usage := accumulator.Usage
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	display := buildOpenAIStyleUsageFromClaudeUsage(usage)
	state.SetUsage(&display)
	if streamErr == nil && (!stopped || len(hosted) != 0 || len(toolArgs) != 0 || c.Request.Context().Err() != nil || info.StreamStatus == nil || !info.StreamStatus.IsNormalEnd() || info.StreamStatus.HasErrors()) {
		streamErr = channel.ResponsesBridgeError(fmt.Errorf("Claude stream interrupted before message_stop"))
	}
	if streamErr != nil {
		return usage, channel.FailResponsesBridge(c, state, streamErr, true)
	}
	return usage, channel.CompleteResponsesBridge(c, resp, info, state, &display, true)
}
