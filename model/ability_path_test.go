package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func insertPathAwareChannel(t *testing.T, id int, channelType int, priority int64, routes []dto.AdvancedCustomRoute) {
	t.Helper()

	channel := &Channel{
		Id:       id,
		Type:     channelType,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("path-aware-%d", id),
		Models:   "path-aware-model",
		Group:    "default",
		Priority: &priority,
	}
	if channelType == constant.ChannelTypeAdvancedCustom {
		channel.SetOtherSettings(dto.ChannelOtherSettings{
			AdvancedCustom: &dto.AdvancedCustomConfig{Routes: routes},
		})
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "path-aware-model",
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
	}).Error)
}

func TestGetChannelFiltersAdvancedRoutesBeforeSelectingPriority(t *testing.T) {
	truncateTables(t)

	insertPathAwareChannel(t, 8101, constant.ChannelTypeAdvancedCustom, 100, []dto.AdvancedCustomRoute{{
		IncomingPath: "/v1/messages",
		UpstreamPath: "/v1/messages",
		Converter:    dto.AdvancedCustomConverterNone,
	}})
	insertPathAwareChannel(t, 8102, constant.ChannelTypeAdvancedCustom, 50, []dto.AdvancedCustomRoute{{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
		Converter:    dto.AdvancedCustomConverterNone,
	}})
	insertPathAwareChannel(t, 8103, constant.ChannelTypeOpenAI, 10, nil)

	channel, err := GetChannel("default", "path-aware-model", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 8102, channel.Id)

	channel, err = GetChannel("default", "path-aware-model", 1, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 8103, channel.Id)

	channel, err = GetChannel("default", "path-aware-model", 0, "/v1/messages")
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 8101, channel.Id)
}
