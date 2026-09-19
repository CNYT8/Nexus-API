package relay

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type imageReservation struct{ held, limit int }

func (s *imageReservation) Reserve(quota int) error {
	if quota > s.limit {
		return errors.New("insufficient image quota")
	}
	s.held = max(s.held, quota)
	return nil
}
func (s *imageReservation) GetPreConsumedQuota() int { return s.held }
func (*imageReservation) Settle(int) error           { return nil }
func (*imageReservation) Refund(*gin.Context)        {}
func (*imageReservation) NeedsRefund() bool          { return false }

func TestImageOutboundReservationBlocksSubmission(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name, body         string
		channel            int
		pass, insufficient bool
		override           any
		count, status      int
	}{
		{name: "Ali provider count", body: `{"model":"z-image","n":2,"parameters":{"n":4}}`, count: 4},
		{name: "Ali empty parameters", body: `{"model":"z-image","n":2,"parameters":{}}`, count: 2},
		{name: "Ali override", body: `{"model":"z-image","n":1}`, override: 4, count: 4},
		{name: "OpenAI override", body: `{"model":"z-image","n":1}`, channel: constant.ChannelTypeOpenAI, override: 4, count: 4},
		{name: "pass through", body: `{"model":"z-image","n":2,"parameters":{}}`, pass: true, count: 2},
		{name: "provider pass through", body: `{"model":"z-image","n":2,"parameters":{"n":4}}`, pass: true, count: 4},
		{name: "zero override", body: `{"model":"z-image","n":1}`, override: 0, status: 400},
		{name: "oversized override", body: `{"model":"z-image","n":1}`, override: 129, status: 400},
		{name: "negative override", body: `{"model":"z-image","n":1}`, override: -1, status: 400},
		{name: "fractional override", body: `{"model":"z-image","n":1}`, override: 1.5, status: 400},
		{name: "string override", body: `{"model":"z-image","n":1}`, override: "4", status: 400},
		{name: "insufficient", body: `{"model":"z-image","n":1}`, override: 4, insufficient: true, status: 403},
		{name: "invalid outbound JSON", body: `{"model":"z-image","n":2} broken`, pass: true, status: 400},
		{name: "null outbound JSON", body: `null`, pass: true, status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan []byte, 1)
			reservation := &imageReservation{held: 20000, limit: 500000}
			if tc.insufficient {
				reservation.limit = reservation.held
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- body
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture failure","type":"upstream_error"}}`)
			}))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			defer common.CleanupBodyStorage(c)
			channel := tc.channel
			if channel == 0 {
				channel = constant.ChannelTypeAli
			}
			common.SetContextKey(c, constant.ContextKeyChannelType, channel)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "z-image")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: tc.pass})
			if tc.override != nil {
				path := "parameters.n"
				if channel == constant.ChannelTypeOpenAI {
					path = "n"
				}
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{map[string]any{"path": path, "mode": "set", "value": tc.override}}})
			}
			// The malformed-body cases deliberately bypass ingress to exercise
			// the independent final outbound trust boundary.
			req := &dto.ImageRequest{Model: "z-image", N: common.GetPointer(uint(1))}
			if !strings.Contains(tc.name, "outbound JSON") {
				var err error
				req, err = helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
				require.NoError(t, err)
			}
			info := &relaycommon.RelayInfo{Request: req, OriginModelName: "z-image", RelayMode: relayconstant.RelayModeImagesGenerations,
				RequestURLPath: c.Request.URL.Path, Billing: reservation,
				PriceData: types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			}
			apiErr := ImageHelper(c, info)
			require.NotNil(t, apiErr)
			if tc.status != 0 {
				require.Equal(t, tc.status, apiErr.StatusCode, "%v", apiErr)
				require.Empty(t, received, "failed reservation/validation must not send upstream")
				return
			}
			require.Equal(t, 502, apiErr.StatusCode, "%v", apiErr)
			require.Len(t, received, 1)
			path := "parameters.n"
			if channel == constant.ChannelTypeOpenAI {
				path = "n"
			}
			require.Equal(t, int64(tc.count), gjson.GetBytes(<-received, path).Int())
			require.Equal(t, tc.count*20000, info.PriceData.QuotaToPreConsume)
			require.Equal(t, info.PriceData.QuotaToPreConsume, reservation.held)
		})
	}
}

func TestImageRetryDropsAliQuantityAndPromptExtension(t *testing.T) {
	service.InitHttpClient()
	received := make(chan []byte, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(502)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
	}))
	defer upstream.Close()
	body := `{"model":"z-image","n":1,"parameters":{"n":4,"prompt_extend":true}}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAli)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "z-image")
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	req, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
	require.NoError(t, err)
	defer common.CleanupBodyStorage(c)
	reservation := &imageReservation{held: 20000, limit: 200000}
	info := &relaycommon.RelayInfo{Request: req, OriginModelName: "z-image", RequestURLPath: c.Request.URL.Path, RelayMode: relayconstant.RelayModeImagesGenerations, Billing: reservation,
		PriceData: types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}}
	apiErr := ImageHelper(c, info)
	require.NotNil(t, apiErr)
	require.Equal(t, 502, apiErr.StatusCode)
	require.Equal(t, 160000, reservation.held)
	require.Equal(t, int64(4), gjson.GetBytes(<-received, "parameters.n").Int())
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	apiErr = ImageHelper(c, info)
	require.NotNil(t, apiErr)
	require.Equal(t, 502, apiErr.StatusCode)
	require.Equal(t, int64(1), gjson.GetBytes(<-received, "n").Int())
	require.Equal(t, 20000, info.PriceData.QuotaToPreConsume)
	require.Equal(t, 160000, reservation.held)
	require.Equal(t, float64(1), info.PriceData.OtherRatios["prompt_extend"])
	require.Equal(t, uint(4), *req.BillingParameters.N)
	require.True(t, *req.BillingParameters.PromptExtend)
}

func TestImageMultipartOutboundAndRetry(t *testing.T) {
	service.InitHttpClient()
	for _, pass := range []bool{false, true} {
		for _, insufficient := range []bool{false, true} {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("model", "z-image"))
			require.NoError(t, writer.WriteField("n", "4"))
			part, err := writer.CreateFormFile("image", "image.png")
			require.NoError(t, err)
			_, err = io.WriteString(part, "fixture image")
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			received := make(chan []byte, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payload, _ := io.ReadAll(r.Body)
				received <- payload
				w.WriteHeader(502)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
			}))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
			contentType := writer.FormDataContentType()
			c.Request.Header.Set("Content-Type", contentType)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "z-image")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: pass})
			req, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
			require.NoError(t, err)
			// The final form still sends four; do not trust a stale DTO count.
			req.N = common.GetPointer(uint(1))
			reservation := &imageReservation{held: 20000, limit: 200000}
			if insufficient {
				reservation.limit = reservation.held
			}
			info := &relaycommon.RelayInfo{Request: req, OriginModelName: "z-image", RequestURLPath: c.Request.URL.Path, RelayMode: relayconstant.RelayModeImagesEdits, Billing: reservation,
				PriceData: types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			}
			for attempt := 0; attempt < 2; attempt++ {
				info.PriceData.AddOtherRatio("n", 128)
				info.PriceData.AddOtherRatio("prompt_extend", 2)
				apiErr := ImageHelper(c, info)
				require.NotNil(t, apiErr)
				require.Equal(t, contentType, c.Request.Header.Get("Content-Type"))
				if insufficient {
					require.Equal(t, 403, apiErr.StatusCode)
					require.Empty(t, received)
				} else {
					require.Equal(t, 502, apiErr.StatusCode, "%v", apiErr)
					require.Equal(t, 80000, reservation.held)
					require.Equal(t, float64(1), info.PriceData.OtherRatios["prompt_extend"])
					payload := <-received
					require.Contains(t, string(payload), "fixture image")
					if pass {
						require.Equal(t, body.Bytes(), payload)
					}
				}
			}
			common.CleanupBodyStorage(c)
			upstream.Close()
		}
	}
}
