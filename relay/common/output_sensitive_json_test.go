package common

import (
	"encoding/json"
	"testing"
)

func TestSanitizeOutputSensitiveJSONPreservesUsage(t *testing.T) {
	matcher := NewOutputSensitiveMatcher([]string{"秘密输出"}, 1)
	input := []byte(`{"choices":[{"message":{"content":"可见内容秘密输出后续"}}],"usage":{"prompt_tokens":4,"completion_tokens":8}}`)
	output, matched, word, err := SanitizeOutputSensitiveJSON(input, matcher)
	if err != nil || !matched || word == "" {
		t.Fatalf("sanitize failed: matched=%v word=%q err=%v", matched, word, err)
	}
	var value map[string]any
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatal(err)
	}
	if value["usage"].(map[string]any)["completion_tokens"] != float64(8) {
		t.Fatal("usage was not preserved")
	}
	content := value["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"]
	if content != "可见内容" {
		t.Fatalf("unexpected truncated content: %q", content)
	}
}
