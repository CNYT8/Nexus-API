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

func TestAdvancedCustomRouteModelRulesAndFallback(t *testing.T) {
	config := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", Models: []string{" client-model "}, UpstreamPath: "/exact", PassThroughBodyEnabled: true},
		{IncomingPath: "/v1/chat/completions", Models: []string{"re:^vendor-.+$"}, UpstreamPath: "/regex"},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/fallback"},
	}}
	require.NoError(t, config.Validate())
	for model, want := range map[string]string{"client-model": "/exact", "vendor-chat": "/regex", "other": "/fallback"} {
		route, matched := config.MatchPathForModel("/v1/chat/completions", model)
		require.True(t, matched)
		assert.Equal(t, want, route.UpstreamPath)
	}
	_, matched := config.MatchPathForModel("/v1/messages", "client-model")
	assert.False(t, matched)

	config.Routes[0].Models = nil
	require.ErrorContains(t, config.Validate(), "catch-all route must be unique and last")
	config.Routes[0].Models = []string{"re:["}
	require.ErrorContains(t, config.Validate(), "regex is empty or invalid")
	config.Routes[0].Models = []string{"re:^vendor-.+$"}
	require.ErrorContains(t, config.Validate(), "duplicate rule")
	config.Routes[0].Models = []string{"client-model"}
	config.Routes[0].Converter = AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses
	require.ErrorContains(t, config.Validate(), "requires converter none")

	for _, route := range []AdvancedCustomRoute{
		{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/models", Models: []string{"client-model"}},
		{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/models", PassThroughBodyEnabled: true},
	} {
		require.ErrorContains(t, (&AdvancedCustomConfig{Routes: []AdvancedCustomRoute{route}}).Validate(), "does not support model rules or body passthrough")
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
