package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCache12hQuotaInput() (*gin.Context, *relaycommon.RelayInfo, *dto.Usage) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "cache12h-quota-test",
		StartTime:               time.Now(),
		PriceData: types.PriceData{
			ModelRatio:            1,
			CompletionRatio:       2,
			CacheRatio:            0.1,
			CacheCreationRatio:    1.25,
			CacheCreation5mRatio:  1.25,
			CacheCreation1hRatio:  1.25 * types.ClaudeCacheCreation1hMultiplier,
			CacheCreation12hRatio: 1.25 * types.OpenAICacheCreation12hMultiplier,
			GroupRatioInfo:        types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	info.SetEstimatePromptTokens(12000)
	usage := &dto.Usage{
		PromptTokens:  12000,
		TotalTokens:   12000,
		UsageSemantic: "openai",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:     4000,
			CacheWriteTokens: 7000,
			CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{
				TTL30mTokens: 3000,
				TTL12hTokens: 4000,
			},
		},
	}
	return ctx, info, usage
}

func TestCalculateTextQuotaSummaryCache12h(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*relaycommon.RelayInfo, *dto.Usage)
		wantQuota int
		want12h   int
	}{
		{
			name: "短长混合只扣一次总写入",
			// (12000-4000-7000) + 4000*0.1 + 3000*1.25 + 4000*2.5。
			wantQuota: 15150,
			want12h:   4000,
		},
		{
			name: "无TTL保持短写价格",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.PromptTokensDetails.CacheWriteTokensDetails = nil
			},
			wantQuota: 10150,
		},
		{
			name: "负长写截断为零",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.PromptTokensDetails.CacheWriteTokensDetails.TTL12hTokens = -1
			},
			wantQuota: 10150,
		},
		{
			name: "长写不超过实际总写入",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.PromptTokensDetails.CacheWriteTokensDetails.TTL12hTokens = 9000
			},
			wantQuota: 18900,
			want12h:   7000,
		},
		{
			name: "短写由总量减长写而非依赖30m字段",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.PromptTokensDetails.CacheWriteTokensDetails.TTL30mTokens = 0
			},
			wantQuota: 15150,
			want12h:   4000,
		},
		{
			name: "长写使用价格快照中的独立倍率",
			prepare: func(info *relaycommon.RelayInfo, _ *dto.Usage) {
				info.PriceData.CacheCreation12hRatio = 3.5
			},
			wantQuota: 19150,
			want12h:   4000,
		},
		{
			name: "模型分组及附加倍率仍生效",
			prepare: func(info *relaycommon.RelayInfo, _ *dto.Usage) {
				info.PriceData.ModelRatio = 2
				info.PriceData.GroupRatioInfo.GroupRatio = 1.5
				info.PriceData.OtherRatios = map[string]float64{"test": 1.2}
			},
			wantQuota: 54540,
			want12h:   4000,
		},
		{
			name: "输出费用仍独立累加",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.CompletionTokens = 1000
				usage.TotalTokens = 13000
			},
			wantQuota: 17150,
			want12h:   4000,
		},
		{
			name: "Claude语义保持原短长写价格",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.UsageSemantic = "anthropic"
				usage.ClaudeCacheCreation5mTokens = 3000
				usage.ClaudeCacheCreation1hTokens = 4000
			},
			// Claude 普通输入不扣缓存，1h 保持原倍率 2，不使用 12h 倍率。
			wantQuota: 24150,
		},
		{
			name: "Claude路由保持原语义",
			prepare: func(info *relaycommon.RelayInfo, usage *dto.Usage) {
				info.FinalRequestRelayFormat = types.RelayFormatClaude
				usage.UsageSemantic = ""
				usage.ClaudeCacheCreation5mTokens = 3000
				usage.ClaudeCacheCreation1hTokens = 4000
			},
			wantQuota: 24150,
		},
		{
			name: "旧Claude派生用量不误用12h",
			prepare: func(_ *relaycommon.RelayInfo, usage *dto.Usage) {
				usage.UsageSemantic = ""
				usage.ClaudeCacheCreation5mTokens = 3000
				usage.ClaudeCacheCreation1hTokens = 4000
			},
			wantQuota: 24150,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, info, usage := newCache12hQuotaInput()
			if tt.prepare != nil {
				tt.prepare(info, usage)
			}
			summary := calculateTextQuotaSummary(ctx, info, usage)
			require.Equal(t, tt.wantQuota, summary.Quota)
			require.Equal(t, usage.PromptTokens, summary.PromptTokens)
			require.Equal(t, 7000, summary.CacheCreationTokens)
			require.Equal(t, tt.want12h, summary.CacheCreationTokens12h)
			require.Equal(t, info.PriceData.CacheCreation12hRatio, summary.CacheCreationRatio12h)
			require.Nil(t, info.QuotaClamp)

			other := map[string]interface{}{"unrelated": true}
			injectTextCacheWriteInfo(other, summary)
			require.Equal(t, true, other["unrelated"])
			require.Equal(t, 7000, other["cache_creation_tokens"])
			require.Equal(t, 1.25, other["cache_creation_ratio"])
			require.Equal(t, 7000, other["cache_write_tokens"])
			if tt.want12h > 0 {
				require.Equal(t, tt.want12h, other["cache_creation_tokens_12h"])
				require.Equal(t, info.PriceData.CacheCreation12hRatio, other["cache_creation_ratio_12h"])
			} else {
				require.NotContains(t, other, "cache_creation_tokens_12h")
				require.NotContains(t, other, "cache_creation_ratio_12h")
			}
			if usage.ClaudeCacheCreation5mTokens > 0 {
				require.Equal(t, 3000, other["cache_creation_tokens_5m"])
				require.Equal(t, 1.25, other["cache_creation_ratio_5m"])
				require.Equal(t, 4000, other["cache_creation_tokens_1h"])
				require.Equal(t, 2.0, other["cache_creation_ratio_1h"])
			}
		})
	}
}

func TestCalculateTextQuotaSummaryCache12hUsageFallback(t *testing.T) {
	t.Run("缺失用量沿用估算", func(t *testing.T) {
		ctx, info, _ := newCache12hQuotaInput()
		summary := calculateTextQuotaSummary(ctx, info, nil)
		require.Equal(t, 12000, summary.Quota)
		require.Zero(t, summary.CacheCreationTokens12h)
	})
	t.Run("显式零用量不能采用估算", func(t *testing.T) {
		ctx, info, _ := newCache12hQuotaInput()
		summary := calculateTextQuotaSummary(ctx, info, &dto.Usage{})
		require.Zero(t, summary.Quota)
		require.Zero(t, summary.TotalTokens)
		other := make(map[string]interface{})
		injectTextCacheWriteInfo(other, summary)
		require.Empty(t, other)
	})
	t.Run("显式零普通用量不因12h写入收费", func(t *testing.T) {
		ctx, info, usage := newCache12hQuotaInput()
		usage.PromptTokens = 0
		usage.TotalTokens = 0
		summary := calculateTextQuotaSummary(ctx, info, usage)
		require.Zero(t, summary.Quota)
		require.Equal(t, 4000, summary.CacheCreationTokens12h)
		require.False(t, summary.hasBillableUsage())
	})
	t.Run("只有Claude缓存仍按原语义计费", func(t *testing.T) {
		ctx, info, usage := newCache12hQuotaInput()
		usage.PromptTokens = 0
		usage.TotalTokens = 0
		usage.UsageSemantic = "anthropic"
		usage.ClaudeCacheCreation5mTokens = 3000
		usage.ClaudeCacheCreation1hTokens = 4000
		summary := calculateTextQuotaSummary(ctx, info, usage)
		require.Equal(t, 12150, summary.Quota)
		require.Zero(t, summary.CacheCreationTokens12h)
		require.True(t, summary.hasBillableUsage())
	})
}
