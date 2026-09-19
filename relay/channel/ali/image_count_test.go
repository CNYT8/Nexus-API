package ali

import (
	"bytes"
	"fmt"
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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAliMultipartValidatedQuantity(t *testing.T) {
	for _, convert := range []func(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (*AliImageRequest, error){oaiFormEdit2AliImageEdit, oaiFormEdit2WanxImageEdit} {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		require.NoError(t, w.WriteField("model", "z-image"))
		require.NoError(t, w.WriteField("n", "2"))
		require.NoError(t, w.WriteField("parameters", `{"n":3,"prompt_extend":false}`))
		part, err := w.CreateFormFile("image", "input.png")
		require.NoError(t, err)
		_, err = io.WriteString(part, "fixture image")
		require.NoError(t, err)
		require.NoError(t, w.Close())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", w.FormDataContentType())
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAli)
		req, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.NoError(t, err)
		converted, err := convert(c, &relaycommon.RelayInfo{Request: req}, *req)
		require.NoError(t, err)
		require.Equal(t, uint(3), *converted.Parameters.N)
		require.NotNil(t, converted.Parameters.PromptExtend)
		require.False(t, *converted.Parameters.PromptExtend)
		require.Equal(t, uint(2), *req.N)
		common.CleanupBodyStorage(c)
	}
}

func TestAliReturnedImageQuantity(t *testing.T) {
	for _, tc := range []struct{ usage, images, want int }{
		{2, 1, 2}, {0, 2, 2}, {-1, 2, 2}, {129, 2, 2},
		{0, 0, 4}, {-1, 0, 4}, {129, 0, 4}, {0, 129, 4},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			images := make([]string, tc.images)
			for i := range images {
				images[i] = `{"url":"https://example.invalid/image.png"}`
			}
			body := fmt.Sprintf(`{"output":{"results":[%s]},"usage":{"image_count":%d}}`, strings.Join(images, ","), tc.usage)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAli}, ImageRequestCount: 4}
			info.PriceData.UsePrice = true
			info.PriceData.AddOtherRatio("n", 4)
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			apiErr, _ := aliImageHandler(&Adaptor{IsSyncImageModel: true}, c, resp, info)
			require.Nil(t, apiErr)
			require.Equal(t, float64(tc.want), info.PriceData.OtherRatios["n"])
			require.Equal(t, 4, info.RequestedImageCount())
		})
	}
}
