package gemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func geminiHistoryIDsThroughJSON(t *testing.T, ids []string) *dto.GeminiChatRequest {
	t.Helper()
	request := dto.GeneralOpenAIRequest{
		Model:    "gemini-2.0-flash",
		Messages: []dto.Message{{Role: "user", Content: "读取两条记录"}},
	}
	var calls []dto.ToolCallRequest
	for _, id := range ids {
		calls = append(calls, dto.ToolCallRequest{
			ID:   id,
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "read_record",
				Arguments: `{"key":"记录"}`,
			},
		})
	}
	assistant := dto.Message{Role: "assistant"}
	assistant.SetToolCalls(calls)
	request.Messages = append(request.Messages, assistant)
	for _, id := range ids {
		request.Messages = append(request.Messages, dto.Message{
			Role:       "tool",
			ToolCallId: id,
			Content:    `{"ok":true}`,
		})
	}

	// 先经过 Chat JSON 解码，再经过真实历史转换器和 Gemini JSON 编解码。
	wire, err := json.Marshal(request)
	require.NoError(t, err)
	var decoded dto.GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal(wire, &decoded))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: request.Model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: request.Model,
		},
	}
	converted, err := CovertOpenAI2Gemini(c, decoded, info)
	require.NoError(t, err)
	wire, err = json.Marshal(converted)
	require.NoError(t, err, "工具响应 ID 必须是合法 JSON 字符串")
	require.True(t, json.Valid(wire))
	var roundTrip dto.GeminiChatRequest
	require.NoError(t, json.Unmarshal(wire, &roundTrip))
	return &roundTrip
}

func TestGeminiHistoryToolIDsJSONRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{name: "同名函数的独立ID", ids: []string{"call-first", "call-second"}},
		{name: "引号反斜杠换行", ids: []string{"call-\"quoted\"-\\path", "call-\n\r\t-tail"}},
		{name: "JSON控制字符", ids: []string{"call-\x00\x07\x0b\x1f", "call-\b\f-tail"}},
		{name: "UTF8与分隔符", ids: []string{"调用-中文-é-🙂", "调用-\u0085\u2028\u2029-末尾"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range tc.ids {
				require.True(t, utf8.ValidString(id))
			}
			request := geminiHistoryIDsThroughJSON(t, tc.ids)
			var callIDs, responseIDs []string
			for _, content := range request.Contents {
				for _, part := range content.Parts {
					if part.FunctionCall != nil {
						require.Equal(t, "read_record", part.FunctionCall.FunctionName)
						callIDs = append(callIDs, part.FunctionCall.ID)
					}
					if part.FunctionResponse != nil {
						require.Equal(t, "read_record", part.FunctionResponse.Name)
						var id string
						require.NoError(t, json.Unmarshal(part.FunctionResponse.ID, &id))
						responseIDs = append(responseIDs, id)
					}
				}
			}
			require.Equal(t, tc.ids, callIDs)
			require.Equal(t, tc.ids, responseIDs)
		})
	}
}

func TestGeminiHistoryToolIDsConversionRoundTrip(t *testing.T) {
	ids := []string{"调用-\"首个\"-\\-🙂", "调用-\x00\x0b-\u2028-第二个"}
	geminiRequest := geminiHistoryIDsThroughJSON(t, ids)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
	}
	converted, err := service.GeminiToOpenAIRequest(geminiRequest, info)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	var roundTrip dto.GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal(wire, &roundTrip))
	var callIDs, responseIDs []string
	for _, message := range roundTrip.Messages {
		for _, call := range message.ParseToolCalls() {
			callIDs = append(callIDs, call.ID)
		}
		if message.Role == "tool" {
			responseIDs = append(responseIDs, message.ToolCallId)
		}
	}
	t.Run("调用ID", func(t *testing.T) {
		require.Equal(t, ids, callIDs, "反向转换必须保留上游ID")
	})
	t.Run("结果关联ID", func(t *testing.T) {
		require.Equal(t, ids, responseIDs, "同名函数的结果必须按原ID关联")
	})
}
