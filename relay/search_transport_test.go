package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/relay/channel/deepseek"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type searchTestAdaptor struct{}

func (*searchTestAdaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return info.ChannelBaseUrl + info.RequestURLPath, nil
}
func (*searchTestAdaptor) SetupRequestHeader(_ *gin.Context, header *http.Header, _ *relaycommon.RelayInfo) error {
	header.Set("Authorization", "Bearer channel-secret")
	return nil
}

type searchRoundTripper func(*http.Request) (*http.Response, error)

func (f searchRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSearchTransport(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		channel          int
	}{
		{name: "Codex alpha", path: dto.AlphaSearchPath, want: "/backend-api/codex/alpha/search", channel: constant.ChannelTypeCodex},
		{name: "DeepSeek search", path: dto.DeepSeekSearchPath, want: dto.DeepSeekSearchPath, channel: constant.ChannelTypeDeepSeek},
		{name: "DeepSeek fetch", path: dto.DeepSeekFetchPath, want: dto.DeepSeekFetchPath, channel: constant.ChannelTypeDeepSeek},
		{name: "compatible alpha", path: dto.AlphaSearchPath, want: dto.AlphaSearchPath, channel: constant.ChannelTypeOpenAI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(`{}`)).WithContext(ctx)
			info := &relaycommon.RelayInfo{RequestURLPath: tc.path, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: tc.channel, ChannelBaseUrl: "https://upstream.invalid",
				HeadersOverride: map[string]interface{}{"X-Search-Trace": "configured"},
			}}
			req, err := buildSearchHTTPRequest(c, &searchTestAdaptor{}, info, tc.path, []byte(`{"opaque":9007199254740993}`))
			if err != nil {
				t.Fatal(err)
			}
			defer req.Body.Close()
			body, err := io.ReadAll(req.Body)
			if err != nil || string(body) != `{"opaque":9007199254740993}` || req.URL.Path != tc.want || req.Method != "POST" || req.Header.Get("Authorization") != "Bearer channel-secret" || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("X-Search-Trace") != "configured" {
				t.Fatalf("unexpected request=%+v, body=%s, error=%v", req, body, err)
			}
			cancel()
			if req.Context().Err() != context.Canceled {
				t.Fatal("search request lost downstream cancellation")
			}
		})
	}
	for _, tc := range []struct {
		channel int
		path    string
	}{
		{constant.ChannelTypeCodex, dto.DeepSeekSearchPath},
		{constant.ChannelTypeDeepSeek, dto.AlphaSearchPath},
		{constant.ChannelTypeAnthropic, dto.DeepSeekFetchPath},
		{constant.ChannelTypeOpenAI, "/v2/alpha/search"},
	} {
		if searchChannelSupported(tc.channel, tc.path) {
			t.Fatalf("unsupported channel/path accepted: %+v", tc)
		}
	}
}

func TestSearchNativeAdapterHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, path, key, authorization, account string
		channel                                 int
		adaptor                                 searchTransportAdaptor
	}{
		{name: "Codex OAuth", path: dto.AlphaSearchPath, key: `{"access_token":"access","account_id":"account"}`, authorization: "Bearer access", account: "account", channel: constant.ChannelTypeCodex, adaptor: &codex.Adaptor{}},
		{name: "DeepSeek bearer", path: dto.DeepSeekSearchPath, key: "channel-secret", authorization: "Bearer channel-secret", channel: constant.ChannelTypeDeepSeek, adaptor: &deepseek.Adaptor{}},
		{name: "compatible bearer", path: dto.AlphaSearchPath, key: "proxy-secret", authorization: "Bearer proxy-secret", channel: constant.ChannelTypeOpenAI, adaptor: &openai.Adaptor{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(`{}`))
			info := &relaycommon.RelayInfo{RequestURLPath: tc.path, RelayMode: relayconstant.Path2RelayMode(tc.path), ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tc.channel, ChannelBaseUrl: "https://upstream.invalid", ApiKey: tc.key}}
			req, err := buildSearchHTTPRequest(c, tc.adaptor, info, tc.path, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			defer req.Body.Close()
			if req.Header.Get("Authorization") != tc.authorization || req.Header.Get("chatgpt-account-id") != tc.account {
				t.Fatalf("native authentication not reused: %v", req.Header)
			}
		})
	}
}

func TestSearchTransportDoesNotFollowRedirect(t *testing.T) {
	client := service.GetHttpClient()
	if client == nil {
		service.InitHttpClient()
		client = service.GetHttpClient()
	}
	previous := *client
	t.Cleanup(func() { *client = previous })
	calls, redirects := 0, 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { redirects++; return nil }
	client.Transport = searchRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://other.invalid/credentials"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: req}, nil
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", dto.DeepSeekSearchPath, strings.NewReader(`{}`))
	info := &relaycommon.RelayInfo{RequestURLPath: dto.DeepSeekSearchPath, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeDeepSeek, ChannelBaseUrl: "https://upstream.invalid"}}
	response, err := doSearchRequest(c, &searchTestAdaptor{}, info, dto.DeepSeekSearchPath, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if calls != 1 || redirects != 0 || response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("calls=%d, redirect callbacks=%d, status=%d", calls, redirects, response.StatusCode)
	}
}
