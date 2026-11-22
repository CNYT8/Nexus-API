package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type compactionMappingTransport struct {
	body  []byte
	url   string
	calls int
}

func (s *compactionMappingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	s.calls++
	s.url = request.URL.String()
	defer request.Body.Close()
	var err error
	s.body, err = io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	// 截获真实出站正文后停止，不访问网络，也不进入响应计费流程。
	return nil, errors.New("compact 回归已截获出站请求")
}

func captureCompactionMappedRequest(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	require.NotNil(t, client)
	transport := &compactionMappingTransport{}
	previousTransport := client.Transport
	client.Transport = transport
	t.Cleanup(func() { client.Transport = previousTransport })

	settings := model_setting.GetGlobalSettings()
	previousPassThrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() { settings.PassThroughRequestEnabled = previousPassThrough })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://compact.example.invalid")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "client-compact")
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: true})
	c.Set("model_mapping", `{"client-compact":"gpt-5.4"}`)
	var request dto.OpenAIResponsesCompactionRequest
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	info := &relaycommon.RelayInfo{
		Request:         &request,
		OriginModelName: request.Model,
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		RelayFormat:     types.RelayFormatOpenAIResponsesCompaction,
		RequestURLPath:  c.Request.URL.Path,
	}
	apiErr := ResponsesHelper(c, info)
	require.NotNil(t, apiErr, "内存传输层应主动终止请求")
	require.Equal(t, 1, transport.calls, "必须经过真实 relay 映射和出站构造：%v", apiErr)
	require.Equal(t, "https://compact.example.invalid/v1/responses/compact", transport.url)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(transport.body, &fields))
	require.JSONEq(t, `"gpt-5.4"`, string(fields["model"]), "模型映射必须实际生效")
	return fields
}

func TestResponsesCompactionRelayPreservesExtendedFields(t *testing.T) {
	const body = `{
		"model":"client-compact",
		"input":[{"role":"user","content":"压缩已有上下文"}],
		"tools":[{"type":"function","name":"lookup","strict":false,"parameters":{"type":"object","properties":{"key":{"type":"string"}}}}],
		"reasoning":{"effort":"low","summary":"auto"},
		"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}},
		"parallel_tool_calls":false,
		"service_tier":"priority",
		"prompt_cache_retention":"24h",
		"max_output_tokens":0
	}`
	mapped := captureCompactionMappedRequest(t, body)
	var original map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &original))
	for _, field := range []string{
		"input", "tools", "reasoning", "text", "parallel_tool_calls",
		"service_tier", "prompt_cache_retention", "max_output_tokens",
	} {
		t.Run(field, func(t *testing.T) {
			require.Contains(t, mapped, field, "compact 到 Responses 映射不得丢失字段")
			require.JSONEq(t, string(original[field]), string(mapped[field]))
		})
	}
}

func TestResponsesCompactionRelayMaxOutputTokensPresence(t *testing.T) {
	for _, tc := range []struct {
		name, body, expected string
		present              bool
	}{
		{name: "缺省", body: `{"model":"client-compact","input":"hello"}`},
		{name: "显式零", body: `{"model":"client-compact","input":"hello","max_output_tokens":0}`, expected: "0", present: true},
		{name: "正数", body: `{"model":"client-compact","input":"hello","max_output_tokens":64}`, expected: "64", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapped := captureCompactionMappedRequest(t, tc.body)
			value, present := mapped["max_output_tokens"]
			require.Equal(t, tc.present, present, "实际出站正文必须区分显式零与缺省")
			if tc.present {
				require.JSONEq(t, tc.expected, string(value))
			}
		})
	}
}
