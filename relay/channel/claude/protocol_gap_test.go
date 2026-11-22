package claude

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeTextDocumentRoundTrip(t *testing.T) {
	const text = "标题\nalpha beta"
	request := dto.ClaudeRequest{
		Model: "claude-3-5-sonnet",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{{
				Type:         "document",
				Title:        "notes.txt",
				Source:       &dto.ClaudeMessageSource{Type: "text", MediaType: "text/plain", Data: text},
				CacheControl: json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`),
			}},
		}},
	}
	chat, err := service.ClaudeToOpenAIRequest(request, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.NoError(t, err)
	require.Len(t, chat.Messages, 1)
	parts := chat.Messages[0].ParseContent()
	require.Len(t, parts, 1)
	require.Equal(t, dto.ContentTypeFile, parts[0].Type)
	require.NotNil(t, parts[0].GetFile())
	require.Equal(t, "data:text/plain;base64,"+base64.StdEncoding.EncodeToString([]byte(text)), parts[0].GetFile().FileData)

	converted, err := RequestOpenAI2ClaudeMessage(nil, *chat)
	require.NoError(t, err)
	require.Len(t, converted.Messages, 1)
	blocks, err := converted.Messages[0].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "document", blocks[0].Type)
	require.Equal(t, "notes.txt", blocks[0].Title)
	require.NotNil(t, blocks[0].Source)
	require.Equal(t, "text", blocks[0].Source.Type)
	require.Equal(t, text, blocks[0].Source.Data)
	require.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, string(blocks[0].CacheControl))
}

func TestClaudeRebuiltTypedMediaKeepsDocument(t *testing.T) {
	original := dto.Message{Role: "user"}
	original.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeText, Text: "前言"},
		{
			Type:         dto.ContentTypeFile,
			File:         &dto.MessageFile{FileName: "notes.txt", FileData: "data:text/plain;base64,YWJj"},
			CacheControl: json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`),
		},
	})
	// 只复制公开正文，模拟转换器重建 DTO 后失去解析缓存。
	rebuilt := dto.Message{Role: original.Role, Content: original.Content}
	converted, err := RequestOpenAI2ClaudeMessage(nil, dto.GeneralOpenAIRequest{
		Model:    "claude-3-5-sonnet",
		Messages: []dto.Message{rebuilt},
	})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 1)
	blocks, err := converted.Messages[0].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "前言", blocks[0].GetText())
	require.Equal(t, "document", blocks[1].Type)
	require.Equal(t, "notes.txt", blocks[1].Title)
	require.NotNil(t, blocks[1].Source)
	require.Equal(t, "text", blocks[1].Source.Type)
	require.Equal(t, "abc", blocks[1].Source.Data)
	require.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, string(blocks[1].CacheControl))
}

func TestClaudeFileDocumentMIMEValidation(t *testing.T) {
	for _, tc := range []struct {
		name, filename, data, wantError string
	}{
		{"json", "notes.bin", "data:application/json;base64,eyJvayI6ZmFsc2V9", ""},
		{"unknown MIME", "notes.txt", "data:application/x-custom;base64,YQ==", "cannot carry content of MIME type"},
		{"unsupported image", "drawing.svg", "data:image/svg+xml;base64,PHN2Zy8+", "cannot carry content of MIME type"},
		{"invalid UTF-8", "notes.txt", "data:text/plain;base64,/w==", "UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, err := openAIFileContentToClaudeMedia(nil, dto.MediaContent{
				Type: dto.ContentTypeFile,
				File: &dto.MessageFile{FileName: tc.filename, FileData: tc.data},
			})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, block)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "document", block.Type)
			require.Equal(t, "text", block.Source.Type)
			require.Equal(t, `{"ok":false}`, block.Source.Data)
		})
	}
	t.Run("file ID requires resolver", func(t *testing.T) {
		_, err := openAIFileContentToClaudeMedia(nil, dto.MediaContent{
			Type: dto.ContentTypeFile,
			File: &dto.MessageFile{FileId: "file-123"},
		})
		require.ErrorContains(t, err, "file_id alone is unsupported")
	})
}

func TestClaudeChatSSECitations(t *testing.T) {
	for _, tc := range []struct {
		name, event, content, annotations string
	}{
		{
			name:        "text start",
			event:       `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"中 abc","citations":[{"type":"web_search_result_location","url":"https://example.test/source","title":"Source","cited_text":"abc"}]}}`,
			content:     "中 abc",
			annotations: `[{"type":"url_citation","url_citation":{"url":"https://example.test/source","title":"Source","cited_text":"abc","start_index":2,"end_index":5}}]`,
		},
		{
			name:        "URL citation delta",
			event:       `{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.test/source","title":"Source","cited_text":"abc","encrypted_index":"opaque"}}}`,
			annotations: `[{"type":"url_citation","url_citation":{"url":"https://example.test/source","title":"Source","cited_text":"abc","encrypted_index":"opaque"}}]`,
		},
		{
			name:        "document citation delta",
			event:       `{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"char_location","document_index":0,"start_char_index":0,"end_char_index":3,"cited_text":"abc"}}}`,
			annotations: `[{"type":"document_citation","document_citation":{"type":"char_location","document_index":0,"start_char_index":0,"end_char_index":3,"cited_text":"abc"}}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			state := &ClaudeResponseInfo{ResponseId: "msg-citations", Model: "claude-3-5-sonnet"}
			err := HandleStreamResponseData(c, info, state, tc.event)
			require.Nil(t, err)
			frame := strings.TrimSpace(recorder.Body.String())
			require.True(t, strings.HasPrefix(frame, "data: "), frame)
			var chunk dto.ChatCompletionsStreamResponse
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &chunk))
			require.Equal(t, "msg-citations", chunk.Id)
			require.Len(t, chunk.Choices, 1)
			require.Equal(t, tc.content, chunk.Choices[0].Delta.GetContentString())
			require.JSONEq(t, tc.annotations, string(chunk.Choices[0].Delta.Annotations))
		})
	}
}

func TestClaudeChatSSECitationsAcrossTextBlocks(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	state := &ClaudeResponseInfo{}
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg-offsets","model":"claude-3-5-sonnet"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"甲🙂"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"abc"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"内部思考"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"不应计入正文偏移"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call-1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"abc\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"text","text":"中 abc","citations":[{"type":"web_search_result_location","url":"https://example.test/start","cited_text":"abc"}]}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"尾🙂"}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.test/delta","cited_text":"abc尾🙂"}}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"message_stop"}`,
	} {
		require.Nil(t, HandleStreamResponseData(c, info, state, event), event)
	}
	var text strings.Builder
	var annotations []json.RawMessage
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk))
		require.Len(t, chunk.Choices, 1)
		text.WriteString(chunk.Choices[0].Delta.GetContentString())
		if raw := chunk.Choices[0].Delta.Annotations; len(raw) > 0 {
			var items []json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &items))
			annotations = append(annotations, items...)
		}
	}
	require.Equal(t, "甲🙂abc中 abc尾🙂", text.String())
	require.Len(t, annotations, 2)
	require.JSONEq(t, `{"type":"url_citation","url_citation":{"url":"https://example.test/start","cited_text":"abc","start_index":7,"end_index":10}}`, string(annotations[0]))
	require.JSONEq(t, `{"type":"url_citation","url_citation":{"url":"https://example.test/delta","cited_text":"abc尾🙂","start_index":7,"end_index":12}}`, string(annotations[1]))
}
