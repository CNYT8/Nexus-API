package setting

import "strings"

var CheckSensitiveEnabled = true
var CheckSensitiveOnPromptEnabled = true

// CheckSensitiveOnCompletionEnabled controls scanning of upstream output. It is
// intentionally off by default: when disabled, relay streaming has no output
// matching overhead at all.
var CheckSensitiveOnCompletionEnabled = false

// OutputSensitiveEnabled enables the optional upstream-output scanner.
var OutputSensitiveEnabled = false

// OutputSensitiveAction is either "truncate" or "error".
var OutputSensitiveAction = "truncate"

// OutputSensitiveMatchPercent is the minimum contiguous percentage of a
// configured pattern that triggers. It is stored and edited as a percentage,
// not a fraction. Short patterns are still checked as exact patterns when the
// configured threshold would otherwise be too small.
var OutputSensitiveMatchPercent = 20
var OutputSensitiveWords = []string{}

func OutputSensitiveMatchRatio() float64 {
	percent := OutputSensitiveMatchPercent
	if percent < 1 {
		percent = 1
	}
	if percent > 100 {
		percent = 100
	}
	return float64(percent) / 100
}

// StopOnSensitiveEnabled controls the prompt replacement action: true stops
// the request with an error, false replaces the matched text.
var StopOnSensitiveEnabled = true

// StreamCacheQueueLength 流模式缓存队列长度，0表示无缓存
var StreamCacheQueueLength = 0

// SensitiveWords 敏感词
// var SensitiveWords []string
var SensitiveWords = []string{
	"test_sensitive",
}

func SensitiveWordsToString() string {
	return strings.Join(SensitiveWords, "\n")
}

func SensitiveWordsFromString(s string) {
	SensitiveWords = ParseSensitiveWords(s)
}

func ParseSensitiveWords(s string) []string {
	words := make([]string, 0)
	for _, w := range strings.Split(s, "\n") {
		w = strings.TrimSpace(w)
		if w != "" {
			words = append(words, w)
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
	return OutputSensitiveEnabled && len(OutputSensitiveWords) > 0
}
