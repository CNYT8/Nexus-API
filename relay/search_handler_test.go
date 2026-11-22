package relay

import (
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
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type searchHandlerBillingProbe struct {
	settlements, refunds int
}

func (b *searchHandlerBillingProbe) Reserve(int) error        { return nil }
func (b *searchHandlerBillingProbe) Settle(int) error         { b.settlements++; return nil }
func (b *searchHandlerBillingProbe) Refund(*gin.Context)      { b.refunds++ }
func (b *searchHandlerBillingProbe) NeedsRefund() bool        { return b.settlements == 0 }
func (b *searchHandlerBillingProbe) GetPreConsumedQuota() int { return 25 }

func searchHandlerContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *searchHandlerBillingProbe) {
	t.Helper()
	body := `{"model":"search-model","opaque":9007199254740993}`
	request, err := dto.ParseSearchRequest(path, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://search.invalid")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "channel-secret")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
	billing := &searchHandlerBillingProbe{}
	info := &relaycommon.RelayInfo{Request: request, OriginModelName: request.Model, RequestURLPath: path,
		RelayMode: relayconstant.Path2RelayMode(path), Billing: billing,
		PriceData: types.PriceData{UsePrice: true, Quota: 25, QuotaToPreConsume: 25},
	}
	return c, w, info, billing
}

func TestSearchHandlerFailures(t *testing.T) {
	client := service.GetHttpClient()
	if client == nil {
		service.InitHttpClient()
		client = service.GetHttpClient()
	}
	previous := *client
	t.Cleanup(func() { *client = previous })
	for _, tc := range []struct {
		name, body string
		status     int
		transport  bool
		sensitive  bool
		code       types.ErrorCode
	}{
		{name: "masked transport failure", transport: true, status: 502, code: types.ErrorCodeDoRequestFailed},
		{name: "upstream rate limit", status: 429, body: `{"error":{"message":"retry later","type":"rate_limit"}}`},
		{name: "invalid success JSON", status: 200, body: `<html>not a result</html>`, code: types.ErrorCodeBadResponseBody},
		{name: "missing tool results", status: 200, body: `{}`, code: types.ErrorCodeBadResponseBody},
		{name: "error envelope with success status", status: 200, body: `{"error":{"message":"upstream-secret"}}`, code: types.ErrorCodeBadResponseBody},
		{name: "sensitive error before write", status: 200, body: `{"results":[{"snippet":"safe秘密tail"}]}`, sensitive: true, code: types.ErrorCodeSensitiveWordsDetected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w, info, billing := searchHandlerContext(t, dto.DeepSeekSearchPath)
			if tc.sensitive {
				info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"秘密"}}
			}
			calls := 0
			client.Transport = searchRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.transport {
					return nil, errors.New("proxy password=upstream-secret")
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})
			apiErr := DeepSeekToolsHelper(c, info)
			if apiErr == nil || calls != 1 || w.Body.Len() != 0 || c.Writer.Written() || billing.settlements != 0 || billing.refunds != 0 {
				t.Fatalf("error=%v calls=%d body=%q billing=%+v", apiErr, calls, w.Body.String(), billing)
			}
			if tc.code != "" && apiErr.GetErrorCode() != tc.code {
				t.Fatalf("error code=%s, want=%s", apiErr.GetErrorCode(), tc.code)
			}
			if tc.status == 429 && apiErr.StatusCode != 429 {
				t.Fatalf("lost upstream status: %d", apiErr.StatusCode)
			}
			if tc.transport && strings.Contains(apiErr.Error(), "upstream-secret") {
				t.Fatal("transport secret escaped the public error mask")
			}
			if tc.sensitive && !types.IsSkipRetryError(apiErr) {
				t.Fatal("sensitive policy error must not retry upstream work")
			}
		})
	}
}

func TestSearchHandlerPreDispatchGuards(t *testing.T) {
	client := service.GetHttpClient()
	if client == nil {
		service.InitHttpClient()
		client = service.GetHttpClient()
	}
	previous := *client
	t.Cleanup(func() { *client = previous })
	calls := 0
	client.Transport = searchRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected upstream dispatch")
	})
	for _, tc := range []struct {
		name string
		edit func(*gin.Context, *relaycommon.RelayInfo)
	}{
		{name: "missing billing session", edit: func(_ *gin.Context, info *relaycommon.RelayInfo) { info.Billing = nil }},
		{name: "wrong endpoint", edit: func(c *gin.Context, _ *relaycommon.RelayInfo) { c.Request.URL.Path = "/v2/alpha/search" }},
		{name: "unsupported native provider", edit: func(c *gin.Context, _ *relaycommon.RelayInfo) {
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		}},
		{name: "streaming override", edit: func(c *gin.Context, _ *relaycommon.RelayInfo) {
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{"stream": true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w, info, billing := searchHandlerContext(t, dto.AlphaSearchPath)
			tc.edit(c, info)
			if apiErr := AlphaSearchHelper(c, info); apiErr == nil || w.Body.Len() != 0 || billing.settlements != 0 || calls != 0 {
				t.Fatalf("pre-dispatch guard failed: error=%v body=%q billing=%+v", apiErr, w.Body.String(), billing)
			}
		})
	}
}
