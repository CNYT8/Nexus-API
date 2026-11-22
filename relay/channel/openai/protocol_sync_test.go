package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProtocolSyncRealtimeBetaOnlyForUpstreamPreview(t *testing.T) {
	adaptor := &Adaptor{}
	for _, tc := range []struct {
		name, upstream, origin string
		preview                bool
	}{
		{"preview", "gpt-4o-realtime-preview", "gpt-4o-realtime-preview", true},
		{"dated preview", "gpt-4o-mini-realtime-preview-2024-12-17", "alias", true},
		{"GA", "gpt-realtime", "gpt-realtime", false},
		{"GA mini", "gpt-realtime-mini", "alias", false},
		{"mapped to GA", "gpt-realtime", "gpt-4o-realtime-preview", false},
		{"mapped to preview", "gpt-4o-realtime-preview", "gpt-realtime", true},
		{"unrelated preview", "gpt-4o-preview", "alias", false},
	} {
		for _, subprotocol := range []bool{false, true} {
			name := tc.name + "/header"
			if subprotocol {
				name = tc.name + "/subprotocol"
			}
			t.Run(name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
				if subprotocol {
					c.Request.Header.Set("Sec-WebSocket-Protocol", "realtime,openai-insecure-api-key.client,openai-beta.realtime-v1")
				}
				info := &relaycommon.RelayInfo{
					RelayMode: relayconstant.RelayModeRealtime, OriginModelName: tc.origin,
					ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: tc.upstream, ApiKey: "upstream-key", Organization: "org-test"},
				}
				header := make(http.Header)
				require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
				require.Equal(t, "org-test", header.Get("OpenAI-Organization"))
				if subprotocol {
					want := []string{"realtime", "openai-insecure-api-key.upstream-key"}
					if tc.preview {
						want = append(want, "openai-beta.realtime-v1")
					}
					require.Equal(t, want, strings.Split(header.Get("Sec-WebSocket-Protocol"), ","))
					require.Empty(t, header.Get("Authorization"))
					require.Empty(t, header.Get("OpenAI-Beta"))
				} else {
					require.Equal(t, "Bearer upstream-key", header.Get("Authorization"))
					require.Empty(t, header.Get("Sec-WebSocket-Protocol"))
					if tc.preview {
						require.Equal(t, "realtime=v1", header.Get("OpenAI-Beta"))
					} else {
						require.Empty(t, header.Get("OpenAI-Beta"))
					}
				}
			})
		}
	}
}

func TestProtocolSyncRealtimeKeepsAuthorizationOverrideAndAzureHeaders(t *testing.T) {
	adaptor := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeRealtime,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o-realtime-preview", ApiKey: "channel-key",
			HeadersOverride: map[string]interface{}{"aUtHoRiZaTiOn": "Bearer override"},
		},
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer override")
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	require.Equal(t, "Bearer override", header.Get("Authorization"))
	require.Equal(t, "realtime=v1", header.Get("OpenAI-Beta"))

	info.ChannelType = constant.ChannelTypeAzure
	info.HeadersOverride = nil
	header = make(http.Header)
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	require.Equal(t, "channel-key", header.Get("api-key"))
	require.Empty(t, header.Get("Authorization"))
	require.Empty(t, header.Get("OpenAI-Beta"))

	info.ChannelType = constant.ChannelTypeOpenAI
	info.RelayMode = relayconstant.RelayModeChatCompletions
	header = make(http.Header)
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	require.Equal(t, "Bearer channel-key", header.Get("Authorization"))
	require.Empty(t, header.Get("OpenAI-Beta"), "preview model names must not add realtime headers to other modes")
}
