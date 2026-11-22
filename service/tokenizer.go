package service

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

// tokenEncoderMap won't grow after initialization
var defaultTokenEncoder tokenizer.Codec
var defaultTokenEncoderOnce sync.Once
var modernTokenEncoder = sync.OnceValue(func() tokenizer.Codec { return codec.NewO200kBase() })

const maxTokenRunBytes = 1024
const maxTokenChunkBytes = 64 * 1024
const maxTokenEncoderCacheEntries = 64

// tokenEncoderMap is used to store token encoders for different models
var tokenEncoderMap = make(map[string]tokenizer.Codec)

// tokenEncoderMutex protects tokenEncoderMap for concurrent access
var tokenEncoderMutex sync.RWMutex

func InitTokenEncoders() {
	common.SysLog("initializing token encoders")
	defaultTokenEncoderOnce.Do(func() { defaultTokenEncoder = codec.NewCl100kBase() })
	common.SysLog("token encoders initialized")
}

func getTokenEncoder(model string) tokenizer.Codec {
	defaultTokenEncoderOnce.Do(func() { defaultTokenEncoder = codec.NewCl100kBase() })
	model = strings.ToLower(strings.TrimPrefix(model, "ft:"))
	legacy := model == "gpt-4" || strings.HasPrefix(model, "gpt-4-") || strings.HasPrefix(model, "gpt-3.5") || strings.HasPrefix(model, "gpt-35")
	if !legacy && (strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "chatgpt-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4")) {
		return modernTokenEncoder()
	}
	// First, try to get the encoder from cache with read lock
	tokenEncoderMutex.RLock()
	if encoder, exists := tokenEncoderMap[model]; exists {
		tokenEncoderMutex.RUnlock()
		return encoder
	}
	tokenEncoderMutex.RUnlock()

	// If not in cache, create new encoder with write lock
	tokenEncoderMutex.Lock()
	defer tokenEncoderMutex.Unlock()

	// Double-check if another goroutine already created the encoder
	if encoder, exists := tokenEncoderMap[model]; exists {
		return encoder
	}

	// Model aliases come from requests. Bound the cache and do not cache misses.
	if len(tokenEncoderMap) >= maxTokenEncoderCacheEntries {
		return defaultTokenEncoder
	}
	modelCodec, err := tokenizer.ForModel(tokenizer.Model(model))
	if err != nil {
		return defaultTokenEncoder
	}

	// Cache the new encoder
	tokenEncoderMap[model] = modelCodec
	return modelCodec
}

func getTokenNum(tokenEncoder tokenizer.Codec, text string) int {
	if text == "" {
		return 0
	}
	if tokenEncoder == nil {
		tokenEncoder = getTokenEncoder("")
	}
	countChunk := func(chunk string) int {
		n, err := tokenEncoder.Count(chunk)
		if err != nil || n < 0 {
			// Count failures must not silently turn nonempty input into free usage.
			return utf8.RuneCountInString(chunk)
		}
		return n
	}
	total, start, runStart, runClass := 0, 0, 0, 0
	for i, r := range text {
		class := tokenRunClass(r)
		if class != runClass {
			runClass, runStart = class, i
		}
		// Bound pathological BPE runs and overall chunks, preserving UTF-8.
		// Digits already split into groups of three inside the tokenizer.
		if i-start >= maxTokenChunkBytes || (class != 2 && i-runStart >= maxTokenRunBytes) {
			total += countChunk(text[start:i])
			start, runStart = i, i
		}
	}
	return total + countChunk(text[start:])
}

func tokenRunClass(r rune) int {
	switch {
	case unicode.IsLetter(r) || unicode.IsMark(r):
		return 1
	case unicode.IsNumber(r):
		return 2
	case unicode.IsSpace(r):
		return 3
	default:
		return 4
	}
}
