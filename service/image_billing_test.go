package service

import (
	"bytes"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageBillingWalletReserveSettleAndRefund(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		wallet, token        int
		refund, insufficient bool
	}{
		{"settle", 200000, 200000, false, false},
		{"refund", 200000, 200000, true, false},
		{"wallet insufficient", 50000, 200000, true, true},
		{"token insufficient rolls back wallet", 200000, 50000, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			seedUser(t, 801, tc.wallet)
			seedToken(t, 801, 801, "image-billing-token", tc.token)
			seedChannel(t, 801)
			ctx, info := newWalletBillingTestContext(801, 801, "image-billing-token", tc.wallet)
			info.Request = &dto.ImageRequest{Model: "z-image", N: common.GetPointer(uint(1))}
			info.OriginModelName = "z-image"
			info.StartTime = time.Now()
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 801, ChannelType: constant.ChannelTypeAli, UpstreamModelName: "z-image"}
			info.PriceData = types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}
			require.Nil(t, PreConsumeBilling(ctx, 20000, info))
			apiErr := PrepareImageBillingForRequest(ctx, info, 4, false)
			if tc.insufficient {
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
				require.Equal(t, 20000, info.Billing.GetPreConsumedQuota())
				require.Equal(t, tc.wallet-20000, getUserQuota(t, 801))
				require.Equal(t, tc.token-20000, getTokenRemainQuota(t, 801))
			} else {
				require.Nil(t, apiErr)
				require.Equal(t, 80000, info.Billing.GetPreConsumedQuota())
				require.Equal(t, tc.wallet-80000, getUserQuota(t, 801))
				require.Equal(t, tc.token-80000, getTokenRemainQuota(t, 801))
			}
			if tc.refund {
				info.Billing.Refund(ctx)
				info.Billing.Refund(ctx)
				require.Eventually(t, func() bool { return getUserQuota(t, 801) == tc.wallet && getTokenRemainQuota(t, 801) == tc.token }, time.Second, time.Millisecond)
			} else {
				info.UpdateImageCount(2)
				PostTextConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 1, TotalTokens: 1}, nil)
				require.Equal(t, tc.wallet-40000, getUserQuota(t, 801))
				require.Equal(t, tc.token-40000, getTokenRemainQuota(t, 801))
				require.Equal(t, 40000, getLastLog(t).Quota)
				require.NoError(t, info.Billing.Settle(40000))
				info.Billing.Refund(ctx)
				require.Equal(t, tc.wallet-40000, getUserQuota(t, 801))
			}
		})
	}
}

func TestImageConcurrentSupplementCannotOverdrawWalletOrToken(t *testing.T) {
	truncate(t)
	seedUser(t, 803, 100000)
	seedToken(t, 803, 803, "image-concurrent-token", 100000)
	contexts := make([]*gin.Context, 2)
	infos := make([]*relaycommon.RelayInfo, 2)
	for i := range infos {
		contexts[i], infos[i] = newWalletBillingTestContext(803, 803, "image-concurrent-token", 100000)
		infos[i].ChannelMeta = &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}
		infos[i].PriceData = types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}
		require.Nil(t, PreConsumeBilling(contexts[i], 20000, infos[i]))
	}
	start := make(chan struct{})
	results := make(chan *types.NewAPIError, 2)
	for i := range infos {
		go func(i int) {
			<-start
			results <- PrepareImageBillingForRequest(contexts[i], infos[i], 4, false)
		}(i)
	}
	close(start)
	winners := 0
	for range infos {
		if err := <-results; err == nil { winners++ } else { require.Equal(t, http.StatusForbidden, err.StatusCode) }
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 0, getUserQuota(t, 803))
	require.Equal(t, 0, getTokenRemainQuota(t, 803))
	for i, info := range infos { info.Billing.Refund(contexts[i]); info.Billing.Refund(contexts[i]) }
	require.Eventually(t, func() bool { return getUserQuota(t, 803) == 100000 && getTokenRemainQuota(t, 803) == 100000 }, time.Second, time.Millisecond)
}

func TestImageBillingRetryAndNumericBounds(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	billing := &capturingBillingSettler{preConsumed: 20000}
	info := &relaycommon.RelayInfo{Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAli, UpstreamModelName: "z-image"},
		PriceData: types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}}
	require.Nil(t, PrepareImageBillingForRequest(c, info, 4, true))
	require.Equal(t, 160000, billing.preConsumed)
	info.ChannelType = constant.ChannelTypeOpenAI
	require.Nil(t, PrepareImageBillingForRequest(c, info, 1, true))
	require.Equal(t, 20000, info.PriceData.QuotaToPreConsume)
	require.Equal(t, 160000, billing.preConsumed, "retry must not refund held funds before settlement")
	require.Equal(t, float64(1), info.PriceData.OtherRatios["n"])
	require.Equal(t, float64(1), info.PriceData.OtherRatios["prompt_extend"])
	for _, count := range []int{-1, 0, 129, math.MaxInt} {
		require.NotNil(t, PrepareImageBillingForRequest(c, info, count, false))
	}
	for _, price := range []float64{-1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		info.PriceData.ModelPrice = price
		require.NotNil(t, PrepareImageBillingForRequest(c, info, 128, false))
	}
	info.PriceData.ModelPrice = 0.04
	info.PriceData.GroupRatioInfo.GroupRatio = 0
	info.Billing = nil
	require.Nil(t, PrepareImageBillingForRequest(c, info, 128, true))
	require.Nil(t, info.Billing)
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{}
	info.PriceData.AddOtherRatio("n", 4)
	info.PriceData.AddOtherRatio("prompt_extend", 2)
	require.Nil(t, PrepareImageBillingForRequest(c, info, 2, true))
	require.Empty(t, info.PriceData.OtherRatios)
}

func TestImageBillingMultipartFinalBodyValidation(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("n", "2"))
	require.NoError(t, w.WriteField("parameters", `{"n":3,"prompt_extend":true}`))
	require.NoError(t, w.Close())
	req, err := ImageBillingRequestFromMultipart(bytes.NewReader(body.Bytes()), w.FormDataContentType())
	require.NoError(t, err)
	n, err := req.ImageCount(true)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	_, err = ImageBillingRequestFromMultipart(bytes.NewReader(body.Bytes()[:body.Len()-10]), w.FormDataContentType())
	require.Error(t, err)
	_, err = ImageBillingRequestFromMultipart(strings.NewReader("broken"), "multipart/form-data")
	require.Error(t, err)
}

func TestImageConvertedProtocolQuantities(t *testing.T) {
	for _, tc := range []struct {
		channel int
		body    string
		want    int
		bad     bool
	}{
		{constant.ChannelTypeGemini, `{"parameters":{"sampleCount":4}}`, 4, false},
		{constant.ChannelTypeVertexAi, `{"parameters":{"sampleCount":3}}`, 3, false},
		{constant.ChannelTypeReplicate, `{"input":{"num_outputs":5}}`, 5, false},
		{constant.ChannelTypeGemini, `{"parameters":{"sampleCount":129}}`, 0, true},
		{constant.ChannelTypeReplicate, `{"input":{"num_outputs":0}}`, 0, true},
	} {
		req, err := ImageBillingRequestFromJSON([]byte(tc.body), tc.channel)
		if tc.bad {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		count, err := req.ImageCount(false)
		require.NoError(t, err)
		require.Equal(t, tc.want, count)
	}
}

func TestImageBillingSubscriptionSupplementIsBounded(t *testing.T) {
	truncate(t)
	seedSubscription(t, 802, 802, 50000, 20000)
	info := &relaycommon.RelayInfo{IsPlayground: true, SubscriptionId: 802,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAli, UpstreamModelName: "z-image"},
		PriceData:   types.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}}
	funding := &SubscriptionFunding{subscriptionId: 802}
	info.Billing = &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: 20000}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	apiErr := PrepareImageBillingForRequest(c, info, 4, false)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	require.Equal(t, int64(20000), getSubscriptionUsed(t, 802))
	require.Equal(t, 20000, info.Billing.GetPreConsumedQuota())
	var sub model.UserSubscription
	require.NoError(t, model.DB.First(&sub, 802).Error)
	funding.periodStart = sub.QuotaPeriodStart
	require.Nil(t, PrepareImageBillingForRequest(c, info, 2, false))
	require.Equal(t, int64(40000), getSubscriptionUsed(t, 802))
}
