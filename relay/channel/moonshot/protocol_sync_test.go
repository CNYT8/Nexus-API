package moonshot

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestProtocolSyncMoonshotNativeResponsesAndLegacyURLs(t *testing.T) {
	adaptor := &Adaptor{}
	for _, tc := range []struct {
		name, base, want string
		format           types.RelayFormat
		mode             int
	}{
		{"responses", "https://api.moonshot.cn", "https://api.moonshot.cn/v1/responses", types.RelayFormatOpenAIResponses, constant.RelayModeResponses},
		{"chat", "https://api.moonshot.cn", "https://api.moonshot.cn/v1/chat/completions", types.RelayFormatOpenAI, constant.RelayModeChatCompletions},
		{"completion", "https://api.moonshot.cn", "https://api.moonshot.cn/v1/completions", types.RelayFormatOpenAI, constant.RelayModeCompletions},
		{"embedding", "https://api.moonshot.cn", "https://api.moonshot.cn/v1/embeddings", types.RelayFormatOpenAI, constant.RelayModeEmbeddings},
		{"rerank", "https://api.moonshot.cn", "https://api.moonshot.cn/v1/rerank", types.RelayFormatOpenAI, constant.RelayModeRerank},
		{"claude", "https://api.moonshot.cn", "https://api.moonshot.cn/anthropic/v1/messages", types.RelayFormatClaude, constant.RelayModeChatCompletions},
		{"plan chat", "kimi-coding-plan", "https://api.kimi.com/coding/v1/chat/completions", types.RelayFormatOpenAI, constant.RelayModeChatCompletions},
		{"plan claude", "kimi-coding-plan", "https://api.kimi.com/coding/v1/messages", types.RelayFormatClaude, constant.RelayModeChatCompletions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tc.base}, RelayFormat: tc.format, RelayMode: tc.mode}
			got, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	const input = `{"model":"kimi-k3","input":[{"role":"user","content":"hello"}],"stream":true,
		"temperature":0,"max_output_tokens":0,"top_p":0,"previous_response_id":"resp_1",
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"reasoning":{"mode":"enabled","context":{"budget":0}},"chat_template_kwargs":{"enable_thinking":false},
		"moderation":false,"frequency_penalty":0,"presence_penalty":0,"client_metadata":{"trace":"a"},
		"top_k":0,"min_p":0,"repetition_penalty":0,"stop":[],"cache_salt":"abc"}`
	var request dto.OpenAIResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(input), &request))
	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, nil, request)
	require.NoError(t, err)
	responseRequest, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	encoded, err := common.Marshal(responseRequest)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))
}

func TestProtocolSyncMoonshotKimi26TemperatureUsesMappedModel(t *testing.T) {
	adaptor := &Adaptor{}
	for _, tc := range []struct {
		name, model, upstream string
		temperature           *float64
		want                  *float64
	}{
		{"missing", "kimi-k2.6", "", nil, nil},
		{"zero", "kimi-k2.6", "", moonshotProtocolFloat(0), moonshotProtocolFloat(1)},
		{"one", "kimi-k2.6", "", moonshotProtocolFloat(1), moonshotProtocolFloat(1)},
		{"other", "kimi-k2.6", "", moonshotProtocolFloat(0.6), moonshotProtocolFloat(1)},
		{"case insensitive", "KIMI-K2.6", "", moonshotProtocolFloat(0.6), moonshotProtocolFloat(1)},
		{"mapped to", "client-alias", "kimi-k2.6", moonshotProtocolFloat(0.6), moonshotProtocolFloat(1)},
		{"mapped away", "kimi-k2.6", "kimi-k2.5", moonshotProtocolFloat(0.6), moonshotProtocolFloat(0.6)},
		{"other model", "kimi-k2.5", "", moonshotProtocolFloat(0), moonshotProtocolFloat(0)},
		{"similar name", "kimi-k2.6-preview", "", moonshotProtocolFloat(0.6), moonshotProtocolFloat(0.6)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{Model: tc.model, Temperature: tc.temperature, TopP: moonshotProtocolFloat(0)}
			var original float64
			if tc.temperature != nil {
				original = *tc.temperature
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.upstream}}
			converted, err := adaptor.ConvertOpenAIRequest(nil, info, request)
			require.NoError(t, err)
			require.Same(t, request, converted)
			require.Equal(t, tc.want, request.Temperature)
			require.Equal(t, tc.model, request.Model)
			require.Zero(t, *request.TopP)
			if tc.temperature != nil {
				require.Equal(t, original, *tc.temperature, "shared sampling pointers must not be changed in place")
			}
		})
	}
	for _, info := range []*relaycommon.RelayInfo{nil, {}} {
		request := &dto.GeneralOpenAIRequest{Model: "kimi-k2.6", Temperature: moonshotProtocolFloat(0)}
		_, err := adaptor.ConvertOpenAIRequest(nil, info, request)
		require.NoError(t, err)
		require.Equal(t, 1.0, *request.Temperature)
	}
	_, err := adaptor.ConvertOpenAIRequest(nil, nil, nil)
	require.ErrorContains(t, err, "request is nil")
}

func moonshotProtocolFloat(value float64) *float64 {
	return &value
}
