package setting

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var CheckSensitiveEnabled = true
var CheckSensitiveOnPromptEnabled = true

// CheckSensitiveOnCompletionEnabled controls the legacy completion scanner.
var CheckSensitiveOnCompletionEnabled = false

const (
	MaxOutputSensitivePatterns     = 256
	MaxOutputSensitivePatternBytes = 64 << 10
	MaxOutputSensitiveTotalBytes   = 1 << 20
)

type OutputSensitiveConfig struct {
	Enabled      bool     `json:"enabled"`
	Action       string   `json:"action"`
	MatchPercent int      `json:"match_percent"`
	Patterns     []string `json:"patterns"`
}

var (
	outputSensitiveMutex sync.RWMutex

	// These exported values are retained for option compatibility. Runtime
	// request handling must use GetOutputSensitiveConfig to obtain a coherent
	// snapshot instead of reading them individually.
	OutputSensitiveEnabled      = false
	OutputSensitiveAction       = "truncate"
	OutputSensitiveMatchPercent = 20
	OutputSensitiveWords        = []string{}
)

func (config OutputSensitiveConfig) MatchRatio() float64 {
	return float64(config.MatchPercent) / 100
}

func NormalizeOutputSensitivePatterns(patterns []string) ([]string, error) {
	normalized := make([]string, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	totalBytes := 0
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if len(pattern) > MaxOutputSensitivePatternBytes {
			return nil, fmt.Errorf("output sensitive pattern exceeds %d bytes", MaxOutputSensitivePatternBytes)
		}
		if _, exists := seen[pattern]; exists {
			continue
		}
		if len(normalized) >= MaxOutputSensitivePatterns {
			return nil, fmt.Errorf("output sensitive patterns exceed %d entries", MaxOutputSensitivePatterns)
		}
		totalBytes += len(pattern)
		if totalBytes > MaxOutputSensitiveTotalBytes {
			return nil, fmt.Errorf("output sensitive patterns exceed %d total bytes", MaxOutputSensitiveTotalBytes)
		}
		seen[pattern] = struct{}{}
		normalized = append(normalized, pattern)
	}
	return normalized, nil
}

func ValidateOutputSensitiveConfig(config OutputSensitiveConfig) (OutputSensitiveConfig, error) {
	if config.Action != "truncate" && config.Action != "error" {
		return OutputSensitiveConfig{}, fmt.Errorf("output sensitive action must be truncate or error")
	}
	if config.MatchPercent < 1 || config.MatchPercent > 100 {
		return OutputSensitiveConfig{}, fmt.Errorf("output sensitive match percent must be between 1 and 100")
	}
	patterns, err := NormalizeOutputSensitivePatterns(config.Patterns)
	if err != nil {
		return OutputSensitiveConfig{}, err
	}
	config.Patterns = patterns
	return config, nil
}

func SetOutputSensitiveConfig(config OutputSensitiveConfig) error {
	normalized, err := ValidateOutputSensitiveConfig(config)
	if err != nil {
		return err
	}
	outputSensitiveMutex.Lock()
	OutputSensitiveEnabled = normalized.Enabled
	OutputSensitiveAction = normalized.Action
	OutputSensitiveMatchPercent = normalized.MatchPercent
	OutputSensitiveWords = append([]string(nil), normalized.Patterns...)
	outputSensitiveMutex.Unlock()
	return nil
}

func GetOutputSensitiveConfig() OutputSensitiveConfig {
	outputSensitiveMutex.RLock()
	config := OutputSensitiveConfig{
		Enabled:      OutputSensitiveEnabled,
		Action:       OutputSensitiveAction,
		MatchPercent: OutputSensitiveMatchPercent,
		Patterns:     append([]string(nil), OutputSensitiveWords...),
	}
	outputSensitiveMutex.RUnlock()
	return config
}

func SetOutputSensitiveEnabled(enabled bool) {
	outputSensitiveMutex.Lock()
	OutputSensitiveEnabled = enabled
	outputSensitiveMutex.Unlock()
}

func SetOutputSensitiveAction(action string) error {
	if action != "truncate" && action != "error" {
		return fmt.Errorf("output sensitive action must be truncate or error")
	}
	outputSensitiveMutex.Lock()
	OutputSensitiveAction = action
	outputSensitiveMutex.Unlock()
	return nil
}

func SetOutputSensitiveMatchPercent(percent int) error {
	if percent < 1 || percent > 100 {
		return fmt.Errorf("output sensitive match percent must be between 1 and 100")
	}
	outputSensitiveMutex.Lock()
	OutputSensitiveMatchPercent = percent
	outputSensitiveMutex.Unlock()
	return nil
}

func SetOutputSensitivePatterns(patterns []string) error {
	normalized, err := NormalizeOutputSensitivePatterns(patterns)
	if err != nil {
		return err
	}
	outputSensitiveMutex.Lock()
	OutputSensitiveWords = append([]string(nil), normalized...)
	outputSensitiveMutex.Unlock()
	return nil
}

func ParseOutputSensitivePatterns(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}, nil
	}
	if strings.HasPrefix(value, "[") {
		var patterns []string
		if err := json.Unmarshal([]byte(value), &patterns); err != nil {
			return nil, fmt.Errorf("invalid output sensitive patterns JSON: %w", err)
		}
		return NormalizeOutputSensitivePatterns(patterns)
	}
	// Backward compatibility for v1.0.9 and older newline-delimited values.
	return NormalizeOutputSensitivePatterns(strings.Split(value, "\n"))
}

func OutputSensitiveConfigJSONString(config OutputSensitiveConfig) (string, error) {
	normalized, err := ValidateOutputSensitiveConfig(config)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ParseOutputSensitiveConfigJSONString(value string) (OutputSensitiveConfig, error) {
	var config OutputSensitiveConfig
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return OutputSensitiveConfig{}, fmt.Errorf("invalid output sensitive config JSON: %w", err)
	}
	return ValidateOutputSensitiveConfig(config)
}

func OutputSensitivePatternsJSONString(patterns []string) (string, error) {
	normalized, err := NormalizeOutputSensitivePatterns(patterns)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func CurrentOutputSensitivePatternsJSONString() string {
	value, err := OutputSensitivePatternsJSONString(GetOutputSensitiveConfig().Patterns)
	if err != nil {
		return "[]"
	}
	return value
}

func OutputSensitiveMatchRatio() float64 {
	return GetOutputSensitiveConfig().MatchRatio()
}

// StopOnSensitiveEnabled controls the prompt replacement action: true stops
// the request with an error, false replaces the matched text.
var StopOnSensitiveEnabled = true

// StreamCacheQueueLength stream cache queue length; zero disables caching.
var StreamCacheQueueLength = 0

var SensitiveWords = []string{
	"test_sensitive",
}

func SensitiveWordsToString() string {
	return strings.Join(SensitiveWords, "\n")
}

func SensitiveWordsFromString(value string) {
	SensitiveWords = ParseSensitiveWords(value)
}

func ParseSensitiveWords(value string) []string {
	words := make([]string, 0)
	for _, word := range strings.Split(value, "\n") {
		word = strings.TrimSpace(word)
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

func ShouldCheckPromptSensitive() bool {
	return CheckSensitiveEnabled && CheckSensitiveOnPromptEnabled && len(SensitiveWords) > 0
}

func ShouldCheckCompletionSensitive() bool {
	return CheckSensitiveEnabled && CheckSensitiveOnCompletionEnabled && len(SensitiveWords) > 0
}

func ShouldCheckOutputSensitive() bool {
	config := GetOutputSensitiveConfig()
	return config.Enabled && len(config.Patterns) > 0
}
