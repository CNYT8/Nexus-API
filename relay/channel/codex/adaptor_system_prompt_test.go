package codex

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestTracksInjectedChannelPrompt(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{SystemPrompt: "channel prompt"},
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{})

	require.NoError(t, err)
	request := converted.(dto.OpenAIResponsesRequest)
	require.JSONEq(t, `"channel prompt"`, string(request.Instructions))
	require.True(t, info.HasChannelSystemPromptApplied())
}

func TestConvertOpenAIResponsesRequestDoesNotInjectChannelPromptTwice(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{
				SystemPrompt:         "channel prompt",
				SystemPromptOverride: true,
			},
		},
	}
	info.MarkChannelSystemPromptApplied("channel prompt")
	request := dto.OpenAIResponsesRequest{
		Instructions: json.RawMessage(`"channel prompt\nuser prompt"`),
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)

	require.NoError(t, err)
	convertedRequest := converted.(dto.OpenAIResponsesRequest)
	require.JSONEq(t, string(request.Instructions), string(convertedRequest.Instructions))
}
