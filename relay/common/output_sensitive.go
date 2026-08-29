package common

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/setting"
)

const (
	outputSensitiveHashBase     uint64 = 1099511628211
	minimumOutputSensitiveRunes        = 8
	outputSensitiveMatchLabel          = "matched"
)

type outputSensitiveWindowRef struct {
	pattern int
	start   int
}

type outputSensitiveCompiledPattern struct {
	runes []rune
}

type outputSensitiveCompiledMatcher struct {
	patternsByLength map[int]map[uint64][]outputSensitiveWindowRef
	patterns         []outputSensitiveCompiledPattern
	lengths          []int
	maxLen           int
}

// OutputSensitiveMatcher owns only request-local stream state. Its immutable
// compiled window index is shared across requests with the same configuration.
type OutputSensitiveMatcher struct {
	compiled *outputSensitiveCompiledMatcher
	pending  []rune
}

var outputSensitiveCompiledCache struct {
	sync.Mutex
	key      [sha256.Size]byte
	compiled *outputSensitiveCompiledMatcher
}

func NewOutputSensitiveMatcherForConfig(config setting.OutputSensitiveConfig) *OutputSensitiveMatcher {
	if !config.Enabled || len(config.Patterns) == 0 {
		return nil
	}
	return NewOutputSensitiveMatcher(config.Patterns, config.MatchRatio())
}

func NewOutputSensitiveMatcher(words []string, minMatchRatio float64) *OutputSensitiveMatcher {
	if minMatchRatio <= 0 || minMatchRatio > 1 {
		minMatchRatio = 1
	}
	key := outputSensitiveConfigHash(words, minMatchRatio)

	outputSensitiveCompiledCache.Lock()
	compiled := outputSensitiveCompiledCache.compiled
	if compiled == nil || outputSensitiveCompiledCache.key != key {
		compiled = compileOutputSensitiveMatcher(words, minMatchRatio)
		outputSensitiveCompiledCache.key = key
		outputSensitiveCompiledCache.compiled = compiled
	}
	outputSensitiveCompiledCache.Unlock()

	if compiled == nil {
		return nil
	}
	return &OutputSensitiveMatcher{compiled: compiled}
}

func outputSensitiveConfigHash(words []string, minMatchRatio float64) [sha256.Size]byte {
	hash := sha256.New()
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], math.Float64bits(minMatchRatio))
	_, _ = hash.Write(scratch[:])
	for _, word := range words {
		binary.LittleEndian.PutUint64(scratch[:], uint64(len(word)))
		_, _ = hash.Write(scratch[:])
		_, _ = hash.Write([]byte(word))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func compileOutputSensitiveMatcher(words []string, minMatchRatio float64) *outputSensitiveCompiledMatcher {
	compiled := &outputSensitiveCompiledMatcher{
		patternsByLength: make(map[int]map[uint64][]outputSensitiveWindowRef),
	}
	lengthSet := make(map[int]struct{})

	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		runes := []rune(word)
		window := int(math.Ceil(float64(len(runes)) * minMatchRatio))
		if window < minimumOutputSensitiveRunes {
			window = minimumOutputSensitiveRunes
		}
		if window > len(runes) {
			window = len(runes)
		}

		patternIndex := len(compiled.patterns)
		compiled.patterns = append(compiled.patterns, outputSensitiveCompiledPattern{runes: runes})
		if window > compiled.maxLen {
			compiled.maxLen = window
		}
		lengthSet[window] = struct{}{}
		byHash := compiled.patternsByLength[window]
		if byHash == nil {
			byHash = make(map[uint64][]outputSensitiveWindowRef)
			compiled.patternsByLength[window] = byHash
		}

		prefix := outputSensitivePrefixHashes(runes)
		power := outputSensitiveHashPower(window)
		for start := 0; start+window <= len(runes); start++ {
			hash := outputSensitiveWindowHash(prefix, start, window, power)
			byHash[hash] = append(byHash[hash], outputSensitiveWindowRef{
				pattern: patternIndex,
				start:   start,
			})
		}
	}

	if len(compiled.patterns) == 0 {
		return nil
	}
	compiled.lengths = make([]int, 0, len(lengthSet))
	for length := range lengthSet {
		compiled.lengths = append(compiled.lengths, length)
	}
	sort.Ints(compiled.lengths)
	return compiled
}

func outputSensitivePrefixHashes(runes []rune) []uint64 {
	prefix := make([]uint64, len(runes)+1)
	for index, value := range runes {
		prefix[index+1] = prefix[index]*outputSensitiveHashBase + uint64(value) + 1
	}
	return prefix
}

func outputSensitiveHashPower(length int) uint64 {
	power := uint64(1)
	for index := 0; index < length; index++ {
		power *= outputSensitiveHashBase
	}
	return power
}

func outputSensitiveWindowHash(prefix []uint64, start int, length int, power uint64) uint64 {
	return prefix[start+length] - prefix[start]*power
}

func (m *OutputSensitiveMatcher) Scan(text string) (bool, string) {
	matched, _, label := m.find(text)
	if matched {
		return true, label
	}
	return false, ""
}

// MatchIndex checks a complete string and returns the rune index of the first
// match. It does not mutate request-local streaming state.
func (m *OutputSensitiveMatcher) MatchIndex(text string) (int, string) {
	if m == nil || m.compiled == nil || text == "" {
		return -1, ""
	}
	input := []rune(strings.ToLower(text))
	index := m.compiled.matchIndex(input)
	if index < 0 {
		return -1, ""
	}
	return index, outputSensitiveMatchLabel
}

func (m *OutputSensitiveMatcher) find(text string) (bool, int, string) {
	if m == nil || m.compiled == nil || text == "" {
		return false, -1, ""
	}
	input := append(append([]rune(nil), m.pending...), []rune(strings.ToLower(text))...)
	pendingOffset := len(m.pending)
	if index := m.compiled.matchIndex(input); index >= 0 {
		return true, index - pendingOffset, outputSensitiveMatchLabel
	}

	keep := m.compiled.maxLen - 1
	if keep > len(input) {
		keep = len(input)
	}
	m.pending = append(m.pending[:0], input[len(input)-keep:]...)
	return false, -1, ""
}

func (compiled *outputSensitiveCompiledMatcher) matchIndex(input []rune) int {
	if compiled == nil || len(input) == 0 {
		return -1
	}
	prefix := outputSensitivePrefixHashes(input)
	firstMatch := -1
	for _, length := range compiled.lengths {
		if len(input) < length {
			continue
		}
		byHash := compiled.patternsByLength[length]
		power := outputSensitiveHashPower(length)
		for start := 0; start+length <= len(input); start++ {
			if firstMatch >= 0 && start >= firstMatch {
				break
			}
			hash := outputSensitiveWindowHash(prefix, start, length, power)
			for _, ref := range byHash[hash] {
				pattern := compiled.patterns[ref.pattern].runes
				if equalOutputSensitiveRunes(input[start:start+length], pattern[ref.start:ref.start+length]) {
					firstMatch = start
					break
				}
			}
		}
	}
	return firstMatch
}

func equalOutputSensitiveRunes(left []rune, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
