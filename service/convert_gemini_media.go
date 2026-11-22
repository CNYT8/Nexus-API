package service

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/dto"
)

// geminiMediaToChatContent maps representable media without fetching remote files.
// Unsupported parts are errors because this converter has no diagnostic channel.
func geminiMediaToChatContent(inlineData *dto.GeminiInlineData, fileData *dto.GeminiFileData) (dto.MediaContent, error) {
	if inlineData == nil {
		if fileData == nil {
			return dto.MediaContent{}, fmt.Errorf("Gemini media is nil")
		}
		mimeType := normalizeGeminiChatMIME(fileData.MimeType)
		if mimeType != "" && !isGeminiChatImageMIME(mimeType) {
			return dto.MediaContent{}, fmt.Errorf("Chat Completions content cannot reference a Gemini file of MIME type %q", fileData.MimeType)
		}
		return dto.MediaContent{
			Type:     dto.ContentTypeImageURL,
			ImageUrl: &dto.MessageImageUrl{Url: fileData.FileUri, Detail: "auto", MimeType: mimeType},
		}, nil
	}

	mimeType := normalizeGeminiChatMIME(inlineData.MimeType)
	if isGeminiChatImageMIME(mimeType) {
		return dto.MediaContent{
			Type:     dto.ContentTypeImageURL,
			ImageUrl: &dto.MessageImageUrl{Url: fmt.Sprintf("data:%s;base64,%s", mimeType, inlineData.Data), Detail: "auto", MimeType: mimeType},
		}, nil
	}
	textMIME, _, _ := strings.Cut(mimeType, ";")
	textMIME = strings.TrimSpace(textMIME)
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return dto.MediaContent{}, fmt.Errorf("Chat Completions content accepts only PNG, JPEG, WebP, and GIF images, not %q", inlineData.MimeType)
	case mimeType == "application/pdf":
		return dto.MediaContent{
			Type: dto.ContentTypeFile,
			File: &dto.MessageFile{FileName: "document.pdf", FileData: "data:application/pdf;base64," + inlineData.Data},
		}, nil
	case strings.HasPrefix(textMIME, "text/") || textMIME == "application/json":
		text, err := base64.StdEncoding.DecodeString(strings.TrimSpace(inlineData.Data))
		if err != nil || !utf8.Valid(text) {
			return dto.MediaContent{}, fmt.Errorf("Gemini inlineData of MIME type %q could not be decoded as UTF-8 text", inlineData.MimeType)
		}
		return dto.MediaContent{Type: dto.ContentTypeText, Text: string(text)}, nil
	case mimeType == "audio/wav" || mimeType == "audio/x-wav" || mimeType == "audio/wave":
		return dto.MediaContent{
			Type:       dto.ContentTypeInputAudio,
			InputAudio: &dto.MessageInputAudio{Data: inlineData.Data, Format: "wav"},
		}, nil
	case mimeType == "audio/mp3" || mimeType == "audio/mpeg":
		return dto.MediaContent{
			Type:       dto.ContentTypeInputAudio,
			InputAudio: &dto.MessageInputAudio{Data: inlineData.Data, Format: "mp3"},
		}, nil
	}
	return dto.MediaContent{}, fmt.Errorf("Gemini inlineData of MIME type %q has no Chat Completions content part", inlineData.MimeType)
}

func normalizeGeminiChatMIME(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if mimeType == "image/jpg" {
		return "image/jpeg"
	}
	return mimeType
}

func isGeminiChatImageMIME(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}
