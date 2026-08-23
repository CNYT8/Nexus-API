package common

import "testing"

func TestOutputSensitiveMatcherMatchesAcrossChunksAndFraction(t *testing.T) {
	matcher := NewOutputSensitiveMatcher([]string{"系统提示词：请严格遵守这是一段很长的规则"}, 0.2)
	if matcher == nil {
		t.Fatal("matcher is nil")
	}
	if hit, _ := matcher.Scan("系统"); hit {
		t.Fatal("partial prefix should not match yet")
	}
	if hit, word := matcher.Scan("提示词：请严格遵守"); !hit || word == "" {
		t.Fatal("expected the configured fraction to match across chunks")
	}
}

func TestOutputSensitiveMatcherDisabledForEmptyPatterns(t *testing.T) {
	if matcher := NewOutputSensitiveMatcher(nil, 0.2); matcher != nil {
		t.Fatal("empty patterns must not enable scanning")
	}
}
