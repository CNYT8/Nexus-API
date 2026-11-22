package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperCache12hRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalModelRatio := ratio_setting.ModelRatio2JSONString()
	originalCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	originalGroupRatio := ratio_setting.GroupRatio2JSONString()
	originalCreateCacheRatio := ratio_setting.CreateCacheRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletionRatio))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatio))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(originalCreateCacheRatio))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"cache12h-price-test":2}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"cache12h-price-test":3}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	tests := []struct {
		name      string
		config    string
		wantShort float64
		want1h    float64
		want12h   float64
	}{
		{"默认短写倍率", `{}`, 1.25, 2, 2.5},
		{"自定义短写倍率", `{"cache12h-price-test":3.75}`, 3.75, 6, 7.5},
		{"零短写倍率", `{"cache12h-price-test":0}`, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(tt.config))
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Set("group", "default")
			info := &relaycommon.RelayInfo{
				OriginModelName: "cache12h-price-test",
				UserGroup:       "default",
				UsingGroup:      "default",
				Request:         &dto.GeneralOpenAIRequest{},
			}
			price, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})
			require.NoError(t, err)
			require.False(t, price.UsePrice)
			require.Equal(t, tt.wantShort, price.CacheCreationRatio)
			require.Equal(t, tt.wantShort, price.CacheCreation5mRatio)
			require.Equal(t, tt.want1h, price.CacheCreation1hRatio)
			require.Equal(t, tt.want12h, price.CacheCreation12hRatio)
			// 预扣仍为普通输入与输出估算，不额外重复预扣缓存写入。
			require.Equal(t, 2600, price.QuotaToPreConsume)
		})
	}
}
