package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeMatchedModelMetaPreservesDisabledExactAndRuleIsolation(t *testing.T) {
	rule := &Model{Status: 1, Description: "family", Tags: "family"}
	exact := &Model{Status: 0, Icon: "disabled-icon"}
	first := mergeMatchedModelMeta(exact, rule)
	require.Zero(t, first.Status)
	require.Equal(t, "family", first.Description)
	require.Empty(t, exact.Description)
	require.Empty(t, rule.Icon)
	fallback := mergeMatchedModelMeta(nil, rule)
	fallback.Icon = "first-model-only"
	second := mergeMatchedModelMeta(nil, rule)
	require.Empty(t, second.Icon)
	require.Equal(t, 1, second.Status)
	require.NotSame(t, fallback, second)
	require.NotSame(t, rule, fallback)
}
