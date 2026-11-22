package operation_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolPricesExactPrefixDefaultAndValidation(t *testing.T) {
	previous := toolPriceSetting.Prices
	t.Cleanup(func() { toolPriceSetting.Prices = previous; RebuildToolPriceIndex() })
	toolPriceSetting.Prices = map[string]float64{
		"google_search": 14, "google_search:gemini-*": 12,
		"google_search:gemini-3*": 9, "google_search:gemini-3-pro": 0,
		"image_generation:custom-model": 7,
	}
	RebuildToolPriceIndex()
	require.Equal(t, 0.0, GetToolPriceForModel("google_search", "gemini-3-pro"))
	require.Equal(t, 9.0, GetToolPriceForModel("google_search", "gemini-3-pro-preview"))
	require.Equal(t, 12.0, GetToolPriceForModel("google_search", "gemini-2.5"))
	require.Equal(t, 14.0, GetToolPriceForModel("google_search", "custom"))
	require.Equal(t, 7.0, GetToolPriceForModel("image_generation", "custom-model"))
	require.True(t, HasConfiguredToolPrice("image_generation", "custom-model"))
	require.False(t, HasConfiguredToolPrice("image_generation", "custom-model-other"))
	require.NoError(t, ValidateToolPrices(map[string]float64{"file_search": 0}))
	for key, value := range map[string]float64{"tool": -1, "tool:model": math.Inf(1), "tool:model*bad": 1, "tool:": 1} {
		require.Error(t, ValidateToolPrices(map[string]float64{key: value}))
	}
}
