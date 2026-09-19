package openai

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertGeminiRequestRequestsStreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	tests := []struct {
		name                 string
		isStream             bool
		supportStreamOptions bool
		wantUsage            bool
	}{
		{name: "stream with stream options", isStream: true, supportStreamOptions: true, wantUsage: true},
		{name: "stream without stream options", isStream: true, supportStreamOptions: false, wantUsage: false},
		{name: "non-stream with stream options", isStream: false, supportStreamOptions: true, wantUsage: false},
		{name: "non-stream without stream options", isStream: false, supportStreamOptions: false, wantUsage: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				IsStream: tt.isStream,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:          constant.ChannelTypeOpenAI,
					UpstreamModelName:    "gpt-4.1",
					SupportStreamOptions: tt.supportStreamOptions,
				},
			}
			request := &dto.GeminiChatRequest{
				Contents: []dto.GeminiChatContent{{
					Role:  "user",
					Parts: []dto.GeminiPart{{Text: "hello"}},
				}},
			}

			converted, err := (&Adaptor{}).ConvertGeminiRequest(c, info, request)
			require.NoError(t, err)
			openaiRequest, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)

			if !tt.wantUsage {
				assert.Nil(t, openaiRequest.StreamOptions)
				return
			}
			require.NotNil(t, openaiRequest.StreamOptions)
			assert.True(t, openaiRequest.StreamOptions.IncludeUsage)

			raw, err := common.Marshal(openaiRequest)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(raw, &payload))
			streamOptions, ok := payload["stream_options"].(map[string]any)
			require.True(t, ok, "stream_options must be present in the JSON body")
			assert.Equal(t, true, streamOptions["include_usage"])
			assert.NotContains(t, streamOptions, "include_obfuscation")
		})
	}
}
