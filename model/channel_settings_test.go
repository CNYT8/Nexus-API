package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvancedCustomChannelRequiresDiscoveryRouteForUpdateChecks(t *testing.T) {
	inferenceRoute := dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
		Converter:    dto.AdvancedCustomConverterNone,
	}

	tests := []struct {
		name          string
		checksEnabled bool
		routes        []dto.AdvancedCustomRoute
		wantErr       bool
	}{
		{name: "legacy channel remains valid", routes: []dto.AdvancedCustomRoute{inferenceRoute}},
		{name: "checks require discovery", checksEnabled: true, routes: []dto.AdvancedCustomRoute{inferenceRoute}, wantErr: true},
		{name: "checks accept discovery", checksEnabled: true, routes: []dto.AdvancedCustomRoute{
			inferenceRoute,
			{IncomingPath: dto.AdvancedCustomModelListPath, UpstreamPath: dto.AdvancedCustomModelListPath},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{Type: constant.ChannelTypeAdvancedCustom}
			channel.SetOtherSettings(dto.ChannelOtherSettings{
				UpstreamModelUpdateCheckEnabled: tt.checksEnabled,
				AdvancedCustom:                  &dto.AdvancedCustomConfig{Routes: tt.routes},
			})
			err := channel.ValidateSettings()
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), dto.AdvancedCustomModelListPath)
		})
	}
}
