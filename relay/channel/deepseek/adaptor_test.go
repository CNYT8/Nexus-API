package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLSupportsResponses(t *testing.T) {
	url, err := (&Adaptor{}).GetRequestURL(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.deepseek.example/v1",
		},
		RelayMode: constant.RelayModeResponses,
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.example/v1/responses", url)
}

func TestConvertOpenAIResponsesRequestAppliesThinkingSuffix(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "deepseek-v4-chat-max",
		},
	}
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model: "deepseek-v4-chat-max",
	})
	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.Equal(t, "deepseek-v4-chat", request.Model)
	require.NotNil(t, request.Reasoning)
	require.Equal(t, "max", request.Reasoning.Effort)
	require.Equal(t, "max", info.ReasoningEffort)
}
