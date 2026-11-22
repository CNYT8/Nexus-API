package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestProtocolSyncGeminiStopSequencesSafePrefix(t *testing.T) {
	for count := 0; count <= 6; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			stops := make([]string, count)
			for i := range stops {
				stops[i] = fmt.Sprintf("stop-%d", i)
			}
			request := &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{StopSequences: stops}}
			converted, err := GeminiToOpenAIRequest(request, nil)
			require.NoError(t, err)
			if count == 0 {
				require.Nil(t, converted.Stop)
			} else {
				require.Equal(t, stops[:min(count, 4)], converted.Stop)
			}
			require.Len(t, request.GenerationConfig.StopSequences, count)
		})
	}
}

func TestProtocolSyncGeminiExplicitZeroAndAbsentGenerationValues(t *testing.T) {
	var request dto.GeminiChatRequest
	require.NoError(t, json.Unmarshal([]byte(`{"contents":[],"generationConfig":{"temperature":0,"topP":0,"topK":0,"maxOutputTokens":0,"candidateCount":0}}`), &request))
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"}}
	converted, err := GeminiToOpenAIRequest(&request, info)
	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"mapped-model","stream":true,"temperature":0,"top_p":0,"top_k":0,"max_tokens":0,"n":0}`, string(encoded))

	converted, err = GeminiToOpenAIRequest(&dto.GeminiChatRequest{}, nil)
	require.NoError(t, err)
	encoded, err = common.Marshal(converted)
	require.NoError(t, err)
	require.JSONEq(t, `{"stream":false}`, string(encoded))
	converted, err = GeminiToOpenAIRequest(&dto.GeminiChatRequest{}, &relaycommon.RelayInfo{IsStream: true})
	require.NoError(t, err)
	require.True(t, *converted.Stream)
	_, err = GeminiToOpenAIRequest(nil, info)
	require.ErrorContains(t, err, "request is nil")
}

func TestProtocolSyncGeminiThoughtHistoryStaysSeparate(t *testing.T) {
	request := &dto.GeminiChatRequest{
		SystemInstructions: &dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "system"}}},
		Contents: []dto.GeminiChatContent{
			{Role: "model", Parts: []dto.GeminiPart{{Thought: true, Text: "private one"}, {Text: "answer"}, {Thought: true, Text: "private two"}}},
			{Role: "model", Parts: []dto.GeminiPart{{Thought: true, Text: "thought only"}}},
			{Role: "model", Parts: []dto.GeminiPart{{Thought: true, Text: "planning"}, {FunctionCall: &dto.FunctionCall{ID: "tool-1", FunctionName: "lookup", Arguments: map[string]any{}}}}},
		},
	}
	converted, err := GeminiToOpenAIRequest(request, nil)
	require.NoError(t, err)
	require.Len(t, converted.Messages, 4)
	require.Equal(t, "system", converted.Messages[0].Content)
	require.Equal(t, "assistant", converted.Messages[1].Role)
	require.Equal(t, "answer", converted.Messages[1].Content)
	require.Equal(t, "private one\nprivate two", converted.Messages[1].GetReasoningContent())
	require.Nil(t, converted.Messages[2].Content)
	require.Equal(t, "thought only", converted.Messages[2].GetReasoningContent())
	require.Equal(t, "planning", converted.Messages[3].GetReasoningContent())
	require.Len(t, converted.Messages[3].ParseToolCalls(), 1)
	require.Equal(t, "tool-1", converted.Messages[3].ParseToolCalls()[0].ID)
}

func TestProtocolSyncGeminiMediaMIMEConversion(t *testing.T) {
	const data = "YWJj"
	for _, tc := range []struct {
		mime, kind, detail string
	}{
		{"image/png", dto.ContentTypeImageURL, "image/png"},
		{" Image/JPG ", dto.ContentTypeImageURL, "image/jpeg"},
		{"image/jpeg", dto.ContentTypeImageURL, "image/jpeg"},
		{"image/webp", dto.ContentTypeImageURL, "image/webp"},
		{"image/gif", dto.ContentTypeImageURL, "image/gif"},
		{"application/pdf", dto.ContentTypeFile, "document.pdf"},
		{"audio/wav", dto.ContentTypeInputAudio, "wav"},
		{"audio/x-wav", dto.ContentTypeInputAudio, "wav"},
		{" AUDIO/WAVE ", dto.ContentTypeInputAudio, "wav"},
		{"audio/mp3", dto.ContentTypeInputAudio, "mp3"},
		{"audio/mpeg", dto.ContentTypeInputAudio, "mp3"},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			request := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{InlineData: &dto.GeminiInlineData{MimeType: tc.mime, Data: data}}}}}}
			converted, err := GeminiToOpenAIRequest(request, nil)
			require.NoError(t, err)
			require.Len(t, converted.Messages, 1)
			parts := converted.Messages[0].ParseContent()
			require.Len(t, parts, 1)
			require.Equal(t, tc.kind, parts[0].Type)
			switch tc.kind {
			case dto.ContentTypeImageURL:
				image := parts[0].GetImageMedia()
				require.NotNil(t, image)
				require.Equal(t, "data:"+tc.detail+";base64,"+data, image.Url)
				require.Equal(t, tc.detail, image.MimeType)
				require.Equal(t, "auto", image.Detail)
			case dto.ContentTypeFile:
				file := parts[0].GetFile()
				require.NotNil(t, file)
				require.Equal(t, tc.detail, file.FileName)
				require.Equal(t, "data:application/pdf;base64,"+data, file.FileData)
			case dto.ContentTypeInputAudio:
				audio := parts[0].GetInputAudio()
				require.NotNil(t, audio)
				require.Equal(t, tc.detail, audio.Format)
				require.Equal(t, data, audio.Data)
			}
		})
	}
	for _, mimeType := range []string{"text/plain", " TEXT/HTML; charset=UTF-8 ", "application/json; charset=utf-8"} {
		t.Run(mimeType, func(t *testing.T) {
			const text = "中文\n{\"ok\":false}"
			part := dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: mimeType, Data: " \n" + base64.StdEncoding.EncodeToString([]byte(text)) + "\t "}}
			converted, err := GeminiToOpenAIRequest(&dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{part}}}}, nil)
			require.NoError(t, err)
			require.Len(t, converted.Messages, 1)
			require.Equal(t, text, converted.Messages[0].Content)
		})
	}
	for _, mimeType := range []string{"", "image/png", " Image/JPG "} {
		t.Run("URL/"+mimeType, func(t *testing.T) {
			const uri = "https://example.invalid/image"
			part := dto.GeminiPart{FileData: &dto.GeminiFileData{MimeType: mimeType, FileUri: uri}}
			converted, err := GeminiToOpenAIRequest(&dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{part}}}}, nil)
			require.NoError(t, err)
			parts := converted.Messages[0].ParseContent()
			require.Len(t, parts, 1)
			require.Equal(t, uri, parts[0].GetImageMedia().Url, "conversion must preserve remote references without fetching")
		})
	}
}

func TestProtocolSyncGeminiUnsupportedMediaReturnsLocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		part dto.GeminiPart
		want string
	}{
		{"svg", dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: "image/svg+xml", Data: "PHN2Zy8+"}}, "only PNG, JPEG, WebP, and GIF"},
		{"video", dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: "video/mp4", Data: "YWJj"}}, "no Chat Completions content part"},
		{"ogg", dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: "audio/ogg", Data: "YWJj"}}, "no Chat Completions content part"},
		{"bad base64", dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: "text/plain", Data: "???"}}, "UTF-8 text"},
		{"bad UTF-8", dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: "text/plain", Data: "/w=="}}, "UTF-8 text"},
		{"remote PDF", dto.GeminiPart{FileData: &dto.GeminiFileData{MimeType: "application/pdf", FileUri: "https://example.invalid/document.pdf"}}, "cannot reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{
				{Role: "user", Parts: []dto.GeminiPart{{Text: "first"}}},
				{Role: "user", Parts: []dto.GeminiPart{{Text: "second"}, tc.part}},
			}}
			converted, err := GeminiToOpenAIRequest(request, nil)
			require.ErrorContains(t, err, "contents[1].parts[1].")
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, converted)
		})
	}
}

func TestProtocolSyncGeminiToolHistoryReservesIDsAndMatchesResults(t *testing.T) {
	var request dto.GeminiChatRequest
	require.NoError(t, json.Unmarshal([]byte(`{"contents":[
		{"role":"model","parts":[{"functionCall":{"name":"first","args":{}}},{"functionCall":{"id":"call_3","name":"second","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"second","response":{"ok":2}}},{"functionResponse":{"name":"first","response":{"ok":1}}}]},
		{"role":"model","parts":[{"functionCall":{"name":"repeat","args":{}}},{"functionCall":{"name":"repeat","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"repeat","id":"call_5","response":{}}},{"functionResponse":{"name":"repeat","response":{}}},{"functionResponse":{"name":"external","id":"call_1","response":{}}}]}
	]}`), &request))
	converted, err := GeminiToOpenAIRequest(&request, nil)
	require.NoError(t, err)
	require.Len(t, converted.Messages, 7)
	calls := converted.Messages[0].ParseToolCalls()
	require.Len(t, calls, 2)
	require.Equal(t, "call_2", calls[0].ID, "reserve response-only call_1 before generating IDs")
	require.Equal(t, "call_3", calls[1].ID)
	require.Equal(t, "call_3", converted.Messages[1].ToolCallId)
	require.Equal(t, "call_2", converted.Messages[2].ToolCallId)
	calls = converted.Messages[3].ParseToolCalls()
	require.Len(t, calls, 2)
	require.Equal(t, "call_4", calls[0].ID)
	require.Equal(t, "call_6", calls[1].ID, "future explicit response IDs remain reserved")
	require.Equal(t, "call_5", converted.Messages[4].ToolCallId)
	require.Equal(t, "call_4", converted.Messages[5].ToolCallId)
	require.Equal(t, "call_1", converted.Messages[6].ToolCallId)

	request.Contents[1].Parts[0].FunctionResponse.ID = json.RawMessage(`123`)
	_, err = GeminiToOpenAIRequest(&request, nil)
	require.ErrorContains(t, err, "invalid Gemini function response ID")
}
