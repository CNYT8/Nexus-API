package operation_setting

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting/config"
)

// ---------------------------------------------------------------------------
// Tool call prices ($/1K calls, admin-configurable)
// DB key: tool_price_setting.prices
//
// Key format:
//   - "tool_name"              → default price for all models
//   - "tool_name:model_prefix*" → override for models matching the prefix
//
// Lookup order: longest prefix match → default → hardcoded fallback → 0
// ---------------------------------------------------------------------------

var defaultToolPrices = map[string]float64{
	"web_search":         10.0, // OpenAI web search (all models) / Claude web search
	"web_search_preview": 10.0, // OpenAI web search preview (default: reasoning models)
	"file_search":        2.5,  // OpenAI file search (Responses API)
	"google_search":      14.0, // Gemini Grounding with Google Search
}

var defaultToolPriceOverrides = map[string]float64{
	"web_search_preview:gpt-4o*":       25.0, // non-reasoning models
	"web_search_preview:gpt-4.1*":      25.0,
	"web_search_preview:gpt-4o-mini*":  25.0,
	"web_search_preview:gpt-4.1-mini*": 25.0,
}

// ToolPriceSetting is managed by config.GlobalConfig.Register.
type ToolPriceSetting struct {
	Prices map[string]float64 `json:"prices"`
}

var toolPriceSetting = ToolPriceSetting{
	Prices: func() map[string]float64 {
		m := make(map[string]float64, len(defaultToolPrices)+len(defaultToolPriceOverrides))
		for k, v := range defaultToolPrices {
			m[k] = v
		}
		for k, v := range defaultToolPriceOverrides {
			m[k] = v
		}
		return m
	}(),
}

func init() {
	config.GlobalConfig.Register("tool_price_setting", &toolPriceSetting)
	RebuildToolPriceIndex()
}

// ---------------------------------------------------------------------------
// Precomputed price index (atomic, lock-free on read path)
// ---------------------------------------------------------------------------

type prefixEntry struct {
	prefix string
	price  float64
}

type toolPriceIndex struct {
	defaults map[string]float64
	exact    map[string]map[string]float64
	prefixes map[string][]prefixEntry
}

var currentIndex atomic.Pointer[toolPriceIndex]

// RebuildToolPriceIndex rebuilds the lookup index from the current config.
// Called on init and after config updates. Not on the billing hot path.
func RebuildToolPriceIndex() {
	merged := make(map[string]float64, len(defaultToolPrices)+len(defaultToolPriceOverrides)+len(toolPriceSetting.Prices))
	for k, v := range defaultToolPrices {
		merged[k] = v
	}
	for k, v := range defaultToolPriceOverrides {
		merged[k] = v
	}
	for k, v := range toolPriceSetting.Prices {
		if validateToolPrice(k, v) == nil {
			merged[k] = v
		}
	}

	idx := &toolPriceIndex{
		defaults: make(map[string]float64),
		exact:    make(map[string]map[string]float64),
		prefixes: make(map[string][]prefixEntry),
	}

	for key, price := range merged {
		colonIdx := strings.IndexByte(key, ':')
		if colonIdx < 0 {
			idx.defaults[key] = price
			continue
		}
		toolName := key[:colonIdx]
		modelPart := key[colonIdx+1:]
		if !strings.HasSuffix(modelPart, "*") {
			if idx.exact[toolName] == nil {
				idx.exact[toolName] = make(map[string]float64)
			}
			idx.exact[toolName][modelPart] = price
			continue
		}
		prefix := strings.TrimSuffix(modelPart, "*")
		idx.prefixes[toolName] = append(idx.prefixes[toolName], prefixEntry{prefix: prefix, price: price})
	}

	for tool := range idx.prefixes {
		entries := idx.prefixes[tool]
		sort.Slice(entries, func(i, j int) bool {
			return len(entries[i].prefix) > len(entries[j].prefix)
		})
		idx.prefixes[tool] = entries
	}

	currentIndex.Store(idx)
}

// GetToolPriceForModel returns the price ($/1K calls) for a tool given a model name.
// Lookup: longest prefix match → tool default → 0.
func GetToolPriceForModel(toolName, modelName string) float64 {
	idx := currentIndex.Load()
	if idx == nil {
		if v, ok := defaultToolPrices[toolName]; ok {
			return v
		}
		return 0
	}

	if price, ok := idx.exact[toolName][modelName]; ok {
		return price
	}
	if entries, ok := idx.prefixes[toolName]; ok && modelName != "" {
		for _, e := range entries {
			if strings.HasPrefix(modelName, e.prefix) {
				return e.price
			}
		}
	}

	if p, ok := idx.defaults[toolName]; ok {
		return p
	}
	if toolName == "image_generation" {
		return 150 // Generic image tool fallback; legacy quality/size stays separate.
	}
	return 0
}

// HasConfiguredToolPrice distinguishes an explicit image price (including zero)
// from the generic fallback, preserving historical quality/size billing.
func HasConfiguredToolPrice(toolName, modelName string) bool {
	idx := currentIndex.Load()
	if idx == nil {
		return false
	}
	if _, ok := idx.defaults[toolName]; ok {
		return true
	}
	if _, ok := idx.exact[toolName][modelName]; ok {
		return true
	}
	for _, entry := range idx.prefixes[toolName] {
		if strings.HasPrefix(modelName, entry.prefix) {
			return true
		}
	}
	return false
}

func validateToolPrice(key string, price float64) error {
	parts := strings.Split(key, ":")
	if key == "" || strings.TrimSpace(key) != key || len(parts) > 2 || parts[0] == "" || strings.Contains(parts[0], "*") {
		return fmt.Errorf("invalid tool price key: %s", key)
	}
	if len(parts) == 2 && (parts[1] == "" || strings.Contains(strings.TrimSuffix(parts[1], "*"), "*")) {
		return fmt.Errorf("invalid tool model pattern: %s", key)
	}
	if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return fmt.Errorf("tool price must be finite and non-negative: %s", key)
	}
	return nil
}

// ValidateToolPrices is available to the option controller before persistence.
func ValidateToolPrices(prices map[string]float64) error {
	for key, price := range prices {
		if err := validateToolPrice(key, price); err != nil {
			return err
		}
	}
	return nil
}

// GetToolPrice is a convenience wrapper when no model name is needed.
func GetToolPrice(toolName string) float64 {
	return GetToolPriceForModel(toolName, "")
}

// ---------------------------------------------------------------------------
// GPT Image 1 per-call pricing (special: depends on quality + size)
// ---------------------------------------------------------------------------

const (
	GPTImage1Low1024x1024    = 0.011
	GPTImage1Low1024x1536    = 0.016
	GPTImage1Low1536x1024    = 0.016
	GPTImage1Medium1024x1024 = 0.042
	GPTImage1Medium1024x1536 = 0.063
	GPTImage1Medium1536x1024 = 0.063
	GPTImage1High1024x1024   = 0.167
	GPTImage1High1024x1536   = 0.25
	GPTImage1High1536x1024   = 0.25
)

func GetGPTImage1PriceOnceCall(quality string, size string) float64 {
	prices := map[string]map[string]float64{
		"low": {
			"1024x1024": GPTImage1Low1024x1024,
			"1024x1536": GPTImage1Low1024x1536,
			"1536x1024": GPTImage1Low1536x1024,
		},
		"medium": {
			"1024x1024": GPTImage1Medium1024x1024,
			"1024x1536": GPTImage1Medium1024x1536,
			"1536x1024": GPTImage1Medium1536x1024,
		},
		"high": {
			"1024x1024": GPTImage1High1024x1024,
			"1024x1536": GPTImage1High1024x1536,
			"1536x1024": GPTImage1High1536x1024,
		},
	}

	if qualityMap, exists := prices[quality]; exists {
		if price, exists := qualityMap[size]; exists {
			return price
		}
	}

	return GPTImage1High1024x1024
}

// ---------------------------------------------------------------------------
// Gemini audio input pricing (per-million tokens, model-specific)
// ---------------------------------------------------------------------------

const (
	Gemini25FlashPreviewInputAudioPrice     = 1.00
	Gemini25FlashProductionInputAudioPrice  = 1.00
	Gemini25FlashLitePreviewInputAudioPrice = 0.50
	Gemini25FlashNativeAudioInputAudioPrice = 3.00
	Gemini20FlashInputAudioPrice            = 0.70
	GeminiRoboticsER15InputAudioPrice       = 1.00
)

func GetGeminiInputAudioPricePerMillionTokens(modelName string) float64 {
	if strings.HasPrefix(modelName, "gemini-2.5-flash-preview-native-audio") {
		return Gemini25FlashNativeAudioInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-preview-lite") {
		return Gemini25FlashLitePreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-preview") {
		return Gemini25FlashPreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash") {
		return Gemini25FlashProductionInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.0-flash") {
		return Gemini20FlashInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-robotics-er-1.5") {
		return GeminiRoboticsER15InputAudioPrice
	}
	return 0
}
