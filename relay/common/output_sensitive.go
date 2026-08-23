package common

import (
	"strings"
)

type outputSensitivePattern struct {
	text string
	word string
}

// OutputSensitiveMatcher keeps a bounded cross-frame tail and scans rolling
// hashes for configured windows. A long pasted pattern can therefore match an
// arbitrary contiguous fraction without expanding every possible window into
// an Aho-Corasick dictionary.
type OutputSensitiveMatcher struct {
	patterns map[int]map[uint64][]outputSensitivePattern
	maxLen   int
	pending  []rune
}

func NewOutputSensitiveMatcher(words []string, minMatchRatio float64) *OutputSensitiveMatcher {
	if minMatchRatio <= 0 || minMatchRatio > 1 {
		minMatchRatio = 1
	}
	patterns := make(map[int]map[uint64][]outputSensitivePattern)
	maxLen := 0
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		runes := []rune(word)
		window := int(float64(len(runes))*minMatchRatio + 0.999999)
		if window < 1 {
			window = 1
		}
		if window > len(runes) {
			window = len(runes)
		}
		if window > maxLen {
			maxLen = window
		}
		segmentCount := len(runes) - window + 1
		for start := 0; start < segmentCount; start++ {
			segment := string(runes[start : start+window])
			hash := outputSensitiveHash(runes[start : start+window])
			byHash := patterns[window]
			if byHash == nil {
				byHash = make(map[uint64][]outputSensitivePattern)
				patterns[window] = byHash
			}
			byHash[hash] = append(byHash[hash], outputSensitivePattern{text: segment, word: word})
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	// The matcher owns mutable cross-frame state. Never share it between
	// requests; sharing would mix tails across users and create a data race.
	return &OutputSensitiveMatcher{patterns: patterns, maxLen: maxLen}
}

func outputSensitiveHash(runes []rune) uint64 {
	var hash uint64 = 1469598103934665603
	for _, r := range runes {
		hash ^= uint64(r)
		hash *= 1099511628211
	}
	return hash
}

func (m *OutputSensitiveMatcher) Scan(text string) (bool, string) {
	matched, _, word := m.find(text)
	if matched {
		return true, word
	}
	return false, ""
}

// MatchIndex checks a complete string and returns the rune index of the first
// match. It is used by non-streaming JSON sanitization.
func (m *OutputSensitiveMatcher) MatchIndex(text string) (int, string) {
	if m == nil || text == "" {
		return -1, ""
	}
	// Non-stream requests are complete, so do not use or mutate the stream tail.
	input := []rune(strings.ToLower(text))
	for length, byHash := range m.patterns {
		if len(input) < length {
			continue
		}
		for start := 0; start+length <= len(input); start++ {
			segment := input[start : start+length]
			for _, entry := range byHash[outputSensitiveHash(segment)] {
				if string(segment) == entry.text {
					return start, entry.word
				}
			}
		}
	}
	return -1, ""
}

func (m *OutputSensitiveMatcher) find(text string) (bool, int, string) {
	if m == nil || len(m.patterns) == 0 || text == "" {
		return false, -1, ""
	}
	input := append(append([]rune(nil), m.pending...), []rune(strings.ToLower(text))...)
	pendingOffset := len(m.pending)
	for length, byHash := range m.patterns {
		if len(input) < length {
			continue
		}
		for start := 0; start+length <= len(input); start++ {
			segment := input[start : start+length]
			for _, entry := range byHash[outputSensitiveHash(segment)] {
				if string(segment) == entry.text {
					return true, start - pendingOffset, entry.word
				}
			}
		}
	}
	keep := m.maxLen - 1
	if keep > len(input) {
		keep = len(input)
	}
	m.pending = append(m.pending[:0], input[len(input)-keep:]...)
	return false, -1, ""
}
