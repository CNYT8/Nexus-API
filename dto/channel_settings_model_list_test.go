package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvancedCustomModelListRouteValidation(t *testing.T) {
	valid := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{{
		IncomingPath: AdvancedCustomModelListPath,
		UpstreamPath: "/provider/models",
		Converter:    AdvancedCustomConverterNone,
	}}}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name  string
		route AdvancedCustomRoute
		want  string
	}{
		{
			name: "converter",
			route: AdvancedCustomRoute{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "/provider/models",
				Converter:    AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses,
			},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			route: AdvancedCustomRoute{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "/provider/{model}",
			},
			want: "must not contain {model}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: []AdvancedCustomRoute{tt.route}}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomModelListRouteRequiresExactPath(t *testing.T) {
	config := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/{model}", UpstreamPath: "/generic/{model}"},
		{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/provider/models"},
	}}
	require.NoError(t, config.Validate())

	route, ok := config.ModelListRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/models", route.UpstreamPath)
}
