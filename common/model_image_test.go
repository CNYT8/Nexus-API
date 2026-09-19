package common_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestImageModelClassification(t *testing.T) {
	for _, name := range []string{"dall-e", "dall-e-3", "GPT-IMAGE-2", "qwen-image", "z-image", "wan2.7-image-pro", "wan2.6-t2i", "wanx2.1-t2i-turbo", "imagen-4", "flux-1"} {
		require.True(t, common.IsImageGenerationModel(name), name)
	}
	for _, name := range []string{"wanx2.1-t2v-plus", "wanx2.1-i2v-turbo", "other-imagen-4", "prefix:imagen-4", "gpt-4"} {
		require.False(t, common.IsImageGenerationModel(name), name)
	}
}
