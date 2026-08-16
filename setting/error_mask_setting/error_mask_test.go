package error_mask_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRulesJSONStringDefaultsLegacyWeight(t *testing.T) {
	rules, err := ParseRulesJSONString(`[{"status":429,"pattern":"quota","replacement":"masked"}]`)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, DefaultErrorMaskRuleWeight, rules[0].Weight)
}

func TestParseRulesJSONStringRejectsOutOfRangeWeight(t *testing.T) {
	for _, rulesJSON := range []string{
		`[{"status":0,"pattern":"","replacement":"masked","weight":0}]`,
		`[{"status":0,"pattern":"","replacement":"masked","weight":11}]`,
	} {
		_, err := ParseRulesJSONString(rulesJSON)
		require.Error(t, err)
	}
}

func TestGetSettingSortsRulesByWeightAndKeepsEqualWeightOrder(t *testing.T) {
	saved := errorMaskSetting
	t.Cleanup(func() {
		errorMaskSetting = saved
	})

	require.NoError(t, UpdateRulesByJSONString(`[
		{"status":0,"pattern":"legacy-first","replacement":"legacy"},
		{"status":0,"pattern":"medium","replacement":"medium","weight":5},
		{"status":0,"pattern":"high","replacement":"high","weight":10},
		{"status":0,"pattern":"same-weight-second","replacement":"same","weight":1}
	]`))

	rules := GetSetting().Rules
	require.Len(t, rules, 4)
	require.Equal(t, "high", rules[0].Pattern)
	require.Equal(t, "medium", rules[1].Pattern)
	require.Equal(t, "legacy-first", rules[2].Pattern)
	require.Equal(t, DefaultErrorMaskRuleWeight, rules[2].Weight)
	require.Equal(t, "same-weight-second", rules[3].Pattern)
}
