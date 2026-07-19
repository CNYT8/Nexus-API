package codex

import (
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func TestModelListKeepsCurrentAndLegacyFallbackModels(t *testing.T) {
	required := []string{
		"gpt-5.6-sol",
		"gpt-5.4-mini",
		"codex-auto-review",
		"gpt-5-codex",
		"gpt-5.1-codex-max",
	}
	for _, model := range required {
		if !slices.Contains(ModelList, model) {
			t.Fatalf("ModelList does not contain %q", model)
		}
	}

	if slices.Contains(ModelList, ratio_setting.WithCompactModelSuffix("codex-auto-review")) {
		t.Fatal("codex-auto-review must not expose an unsupported compact variant")
	}
	if !slices.Contains(ModelList, ratio_setting.WithCompactModelSuffix("gpt-5.4-mini")) {
		t.Fatal("regular Codex models must expose compact variants")
	}
}
