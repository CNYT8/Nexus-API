package common

import (
	"net/http/httptest"
	"testing"

	appcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoGetFinalRequestRelayFormatPrefersExplicitFinal(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToConversionChain(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToRelayFormat(t *testing.T) {
	info := &RelayInfo{
		RelayFormat: types.RelayFormatGemini,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatNilReceiver(t *testing.T) {
	var info *RelayInfo
	require.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
}

func TestAdvancedRoutePassThroughDoesNotLeakAcrossRetries(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	appcommon.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
	appcommon.SetContextKey(c, constant.ContextKeyOriginalModel, "client-model")
	appcommon.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	config := &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", Models: []string{"client-model"}, UpstreamPath: "/native", PassThroughBodyEnabled: true},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/fallback"},
	}}
	appcommon.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AdvancedCustom: config})
	info := &RelayInfo{OriginModelName: "client-model"}
	info.InitChannelMeta(c)
	require.True(t, info.ChannelSetting.PassThroughBodyEnabled)

	// A retry with a different route must rebuild its per-channel flag.
	config.Routes[0].Models = []string{"other-model"}
	info.InitChannelMeta(c)
	require.False(t, info.ChannelSetting.PassThroughBodyEnabled)

	// An explicit channel-wide flag retains its existing precedence.
	appcommon.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
	info.InitChannelMeta(c)
	require.True(t, info.ChannelSetting.PassThroughBodyEnabled)

	// Invalid stored converter routes cannot force native passthrough.
	appcommon.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	config.Routes[0].Models = []string{"client-model"}
	config.Routes[0].Converter = dto.AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses
	info.InitChannelMeta(c)
	require.False(t, info.ChannelSetting.PassThroughBodyEnabled)
}

func TestRelayInfoResetChannelSystemPromptApplied(t *testing.T) {
	info := &RelayInfo{}
	info.MarkChannelSystemPromptApplied("channel prompt")
	require.True(t, info.HasChannelSystemPromptApplied())

	info.resetChannelSystemPromptApplied()

	require.False(t, info.HasChannelSystemPromptApplied())
	require.Empty(t, info.ChannelSystemPrompt)
}
