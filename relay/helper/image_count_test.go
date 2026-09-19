package helper

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageIngressQuantityBeforePricing(t *testing.T) {
	for _, channel := range []int{constant.ChannelTypeAli, constant.ChannelTypeOpenAI} {
		for _, form := range []bool{false, true} {
			for _, n := range []string{"0", "-1", "1.5", "129", "18446744073709551616", `"3"`} {
				parameters := `{"n":` + n + `}`
				body := bytes.NewBufferString(`{"model":"z-image","n":2,"parameters":` + parameters + `}`)
				contentType := "application/json"
				if form {
					body.Reset()
					w := multipart.NewWriter(body)
					require.NoError(t, w.WriteField("model", "z-image"))
					require.NoError(t, w.WriteField("n", "2"))
					require.NoError(t, w.WriteField("parameters", parameters))
					require.NoError(t, w.Close())
					contentType = w.FormDataContentType()
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", body)
				c.Request.Header.Set("Content-Type", contentType)
				common.SetContextKey(c, constant.ContextKeyChannelType, channel)
				_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
				require.Error(t, err, "channel=%d multipart=%v parameters=%s", channel, form, parameters)
				common.CleanupBodyStorage(c)
			}
		}
	}
}

func TestImageIngressDiskJSONRejectsTrailingData(t *testing.T) {
	saved := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
	t.Cleanup(func() { common.SetDiskCacheConfig(saved) })
	for _, body := range []string{`{"model":"z-image","n":2} {"n":129}`, `{"model":"z-image","n":2} broken`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
		require.Error(t, err)
		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		require.True(t, storage.IsDisk())
		common.CleanupBodyStorage(c)
	}
}

func TestImageLegacyIngressReservation(t *testing.T) {
	saved := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"z-image":0.04,"alias-image":0.04,"dall-e-3":0.04}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(saved)) })
	for _, tc := range []struct {
		body, mapping              string
		channel, count, multiplier int
	}{
		{`{"model":"z-image","n":2,"parameters":{"n":4}}`, "", constant.ChannelTypeAli, 4, 1},
		{`{"model":"z-image","n":2,"parameters":{}}`, "", constant.ChannelTypeAli, 2, 1},
		{`{"model":"z-image","parameters":{"n":3,"prompt_extend":true}}`, "", constant.ChannelTypeAli, 3, 2},
		{`{"model":"alias-image","parameters":{"n":3,"prompt_extend":true}}`, `{"alias-image":"z-image"}`, constant.ChannelTypeAli, 3, 2},
		{`{"model":"z-image","n":2,"parameters":{"n":4,"prompt_extend":true}}`, "", constant.ChannelTypeOpenAI, 2, 1},
		{`{"model":"dall-e-3","n":2,"quality":"hd","size":"1024x1792"}`, "", constant.ChannelTypeOpenAI, 2, 3},
	} {
		t.Run(tc.body, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channel)
			c.Set("model_mapping", tc.mapping)
			req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
			require.NoError(t, err)
			defer common.CleanupBodyStorage(c)
			info := &relaycommon.RelayInfo{Request: req, OriginModelName: req.Model, UserGroup: "default", UsingGroup: "default"}
			price, err := ModelPriceHelper(c, info, 0, req.GetTokenCountMeta())
			require.NoError(t, err)
			require.Equal(t, common.QuotaFromFloat(0.04*float64(tc.count*tc.multiplier)*common.QuotaPerUnit), price.QuotaToPreConsume)
			require.Nil(t, info.ChannelMeta, "pricing must not initialize attempt state")
		})
	}
}
