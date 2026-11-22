package reasoning

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekV4ThinkingSuffixLowAndModelBoundaries(t *testing.T) {
	for _, tc := range []struct {
		model, base, mode, effort string
		matched                   bool
	}{
		{model: "deepseek-v4-pro-low", base: "deepseek-v4-pro", mode: "enabled", effort: "low", matched: true},
		{model: "deepseek-v4-flash-low", base: "deepseek-v4-flash", mode: "enabled", effort: "low", matched: true},
		{model: "deepseek-v4-pro-max", base: "deepseek-v4-pro", mode: "enabled", effort: "max", matched: true},
		{model: "deepseek-v4-pro-none", base: "deepseek-v4-pro", mode: "disabled", matched: true},
		{model: "deepseek-v4-pro", base: "deepseek-v4-pro"},
		{model: "deepseek-v4-pro-high", base: "deepseek-v4-pro-high"},
		{model: "deepseek-v4-pro-low-extra", base: "deepseek-v4-pro-low-extra"},
		{model: "deepseek-v3-low", base: "deepseek-v3-low"},
		{model: "deepseek-chat-low", base: "deepseek-chat-low"},
		{model: "deepseek-reasoner-low", base: "deepseek-reasoner-low"},
		{model: "deepseek-v40-pro-low", base: "deepseek-v40-pro-low"},
		{model: "gpt-5-low", base: "gpt-5-low"},
		{model: "claude-sonnet-4-low", base: "claude-sonnet-4-low"},
		{model: "gemini-3-pro-low", base: "gemini-3-pro-low"},
		{model: "provider/deepseek-v4-pro-low", base: "provider/deepseek-v4-pro-low"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			base, mode, effort, matched := ParseDeepSeekV4ThinkingSuffix(tc.model)
			require.Equal(t, tc.base, base)
			require.Equal(t, tc.mode, mode)
			require.Equal(t, tc.effort, effort)
			require.Equal(t, tc.matched, matched)
		})
	}
}

func TestDeepSeekV4LowDoesNotChangeOpenAISuffixParsing(t *testing.T) {
	for _, model := range []string{"gpt-5-low", "o3-low"} {
		t.Run(model, func(t *testing.T) {
			base, mode, effort, matched := ParseDeepSeekV4ThinkingSuffix(model)
			require.False(t, matched)
			require.Equal(t, model, base)
			require.Empty(t, mode)
			require.Empty(t, effort)
			openAIEffort, openAIBase := ParseOpenAIReasoningEffortFromModelSuffix(model)
			require.Equal(t, "low", openAIEffort)
			require.Equal(t, model[:len(model)-len("-low")], openAIBase)
		})
	}
}
