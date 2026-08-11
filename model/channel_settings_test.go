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

func TestChannelProxyValidation(t *testing.T) {
	validChannel := &Channel{}
	validChannel.SetSetting(dto.ChannelSettings{Proxy: "http://proxy.example:8080"})
	assert.NoError(t, validChannel.ValidateSettings())

	invalidChannel := &Channel{}
	invalidChannel.SetSetting(dto.ChannelSettings{Proxy: "http://proxy.example:8080/legacy"})
	err := invalidChannel.ValidateSettings()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channel proxy")
}

func TestGetSettingDoesNotEraseInvalidStoredValue(t *testing.T) {
	rawSetting := `{"proxy":"http://proxy.example:8080",`
	channel := &Channel{Id: 42, Setting: &rawSetting}

	setting := channel.GetSetting()

	assert.Empty(t, setting.Proxy)
	require.NotNil(t, channel.Setting)
	assert.Equal(t, rawSetting, *channel.Setting)
}
