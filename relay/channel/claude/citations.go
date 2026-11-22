package claude

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/dto"
)

func claudeCitationsToChat(citations []json.RawMessage, text, prefix string) []any {
	return claudeCitationsToChatAtOffset(citations, text, utf8.RuneCountInString(prefix))
}

func claudeCitationsToChatAtOffset(citations []json.RawMessage, text string, offset int) []any {
	var annotations []any
	for _, raw := range citations {
		var citation map[string]any
		if json.Unmarshal(raw, &citation) != nil {
			continue
		}
		url, _ := citation["url"].(string)
		if url == "" {
			// Document citations have no Chat URL equivalent; preserve explicitly
			// rather than inventing a URL or silently discarding their locations.
			annotations = append(annotations, map[string]any{"type": "document_citation", "document_citation": citation})
			continue
		}
		converted := map[string]any{"url": url}
		for _, key := range []string{"title", "cited_text", "encrypted_index"} {
			if value, exists := citation[key]; exists {
				converted[key] = value
			}
		}
		if cited, _ := citation["cited_text"].(string); cited != "" {
			if before, _, found := strings.Cut(text, cited); found {
				start := offset + utf8.RuneCountInString(before)
				converted["start_index"] = start
				converted["end_index"] = start + utf8.RuneCountInString(cited)
			}
		}
		annotations = append(annotations, map[string]any{"type": "url_citation", "url_citation": converted})
	}
	return annotations
}

type claudeCitationBlock struct {
	text   strings.Builder
	offset int
}

func (c *ClaudeResponseInfo) updateChatCitations(response *dto.ClaudeResponse, chunk *dto.ChatCompletionsStreamResponse) {
	if c == nil || response == nil {
		return
	}
	index := 0
	if response.Index != nil {
		index = *response.Index
	}
	var citations []json.RawMessage
	var block *claudeCitationBlock
	switch response.Type {
	case "message_start", "message_stop":
		c.citationBlocks = nil
		c.citationTextOffset = 0
		return
	case "content_block_stop":
		delete(c.citationBlocks, index)
		return
	case "content_block_start":
		if response.ContentBlock == nil || response.ContentBlock.Type != "text" {
			return
		}
		if c.citationBlocks == nil {
			c.citationBlocks = make(map[int]*claudeCitationBlock)
		}
		block = &claudeCitationBlock{offset: c.citationTextOffset}
		c.citationBlocks[index] = block
		text := response.ContentBlock.GetText()
		block.text.WriteString(text)
		c.citationTextOffset += utf8.RuneCountInString(text)
		citations = response.ContentBlock.Citations
	case "content_block_delta":
		if response.Delta == nil {
			return
		}
		block = c.citationBlocks[index]
		switch response.Delta.Type {
		case "text_delta":
			if block == nil {
				if c.citationBlocks == nil {
					c.citationBlocks = make(map[int]*claudeCitationBlock)
				}
				block = &claudeCitationBlock{offset: c.citationTextOffset}
				c.citationBlocks[index] = block
			}
			text := response.Delta.GetText()
			block.text.WriteString(text)
			c.citationTextOffset += utf8.RuneCountInString(text)
			return
		case "citations_delta":
			if len(response.Delta.Citation) > 0 {
				citations = []json.RawMessage{response.Delta.Citation}
			}
		default:
			return
		}
	default:
		return
	}
	if len(citations) == 0 || chunk == nil || len(chunk.Choices) == 0 {
		return
	}
	// 只用当前正文块定位引用；思考和工具参数不计入 Chat 正文偏移。
	text, offset := "", 0
	if block != nil {
		text, offset = block.text.String(), block.offset
	}
	if annotations := claudeCitationsToChatAtOffset(citations, text, offset); len(annotations) > 0 {
		chunk.Choices[0].Delta.Annotations, _ = json.Marshal(annotations)
	}
}
