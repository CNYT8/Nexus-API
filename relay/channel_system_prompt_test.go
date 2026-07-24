package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApplySystemPromptIfNeededInjectsChannelPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{SystemPrompt: "channel prompt"},
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}

	applySystemPromptIfNeeded(ctx, info, request)

	require.Len(t, request.Messages, 2)
	require.Equal(t, request.GetSystemRoleName(), request.Messages[0].Role)
	require.Equal(t, "channel prompt", request.Messages[0].StringContent())
	require.True(t, info.HasChannelSystemPromptApplied())
}

func TestApplySystemPromptIfNeededKeepsUserPromptWithoutOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{SystemPrompt: "channel prompt"},
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "system", Content: "user prompt"}},
	}

	applySystemPromptIfNeeded(ctx, info, request)

	require.Len(t, request.Messages, 1)
	require.Equal(t, "user prompt", request.Messages[0].StringContent())
	require.False(t, info.HasChannelSystemPromptApplied())
}

func TestApplySystemPromptIfNeededPrependsChannelPromptOnOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{
				SystemPrompt:         "channel prompt",
				SystemPromptOverride: true,
			},
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "system", Content: "user prompt"}},
	}

	applySystemPromptIfNeeded(ctx, info, request)

	require.Len(t, request.Messages, 1)
	require.Equal(t, "channel prompt\nuser prompt", request.Messages[0].StringContent())
	require.True(t, info.HasChannelSystemPromptApplied())
}
