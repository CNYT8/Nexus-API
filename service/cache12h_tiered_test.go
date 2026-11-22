package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/require"
)

func TestCache12hTieredNormalization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expr   string
		p      float64
		cc     float64
		cc12h  float64
		cost   float64
		noTTL  bool
		claude bool
		cc1h   float64
	}{
		{name: "独立计费", expr: `p * 10 + cr + cc * 12.5 + cc12h * 25`, p: 1000, cc: 3000, cc12h: 4000, cost: 151500},
		{name: "旧公式折入普通写入", expr: `p * 10 + cr + cc * 12.5`, p: 1000, cc: 11000, cost: 151500},
		{name: "仅独立长缓存", expr: `p * 10 + cr + cc12h * 25`, p: 4000, cc12h: 4000, cost: 144000},
		{name: "未区分写入折入输入", expr: `p * 10 + cr`, p: 12000, cost: 124000},
		{name: "全部缓存折入输入", expr: `p * 10`, p: 16000, cost: 160000},
		{name: "读取保留在输入", expr: `p * 10 + cc12h * 25`, p: 8000, cc12h: 4000, cost: 180000},
		{name: "显式长缓存免费", expr: `p * 10 + cr + cc * 12.5 + cc12h * 0`, p: 1000, cc: 3000, cc12h: 4000, cost: 51500},
		{name: "未返回TTL明细", expr: `p * 10 + cr + cc * 12.5 + cc12h * 25`, p: 1000, cc: 7000, cost: 101500, noTTL: true},
		{name: "Claude独立缓存", expr: `p * 10 + cr + cc * 12.5 + cc1h * 20 + cc12h * 25`, p: 1000, cc: 3000, cc1h: 4000, cost: 131500, claude: true},
		{name: "Claude一小时折入普通写入", expr: `p * 10 + cr + cc * 12.5`, p: 1000, cc: 9400, cost: 131500, claude: true},
		{name: "Claude全部折入输入", expr: `p * 10`, p: 14400, cost: 144000, claude: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens: 12000,
				TotalTokens:  12000,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:     4000,
					CacheWriteTokens: 7000,
				},
			}
			if tc.claude {
				usage.PromptTokens = 1000
				usage.TotalTokens = 1000
				usage.UsageSemantic = "anthropic"
				usage.ClaudeCacheCreation5mTokens = 3000
				usage.ClaudeCacheCreation1hTokens = 4000
			} else if !tc.noTTL {
				usage.PromptTokensDetails.CacheWriteTokensDetails = &dto.CacheWriteTokenDetails{TTL30mTokens: 3000, TTL12hTokens: 4000}
			}
			params := BuildTieredTokenParams(usage, tc.claude, billingexpr.UsedVars(tc.expr))
			require.Equal(t, tc.p, params.P)
			require.Equal(t, tc.cc, params.CC)
			require.Equal(t, tc.cc1h, params.CC1h)
			require.Equal(t, tc.cc12h, params.CC12h)
			require.Equal(t, float64(12000), params.Len, "计费折算不能影响上下文长度")
			cost, _, err := billingexpr.RunExpr(tc.expr, params)
			require.NoError(t, err)
			require.Equal(t, tc.cost, cost)
			require.Equal(t, 7000, usage.PromptTokensDetails.CacheCreationTokensTotal(), "结算不能回写原始总量")
		})
	}
}

func TestCache12hReportedExpressionSettlement(t *testing.T) {
	const expression = `len <= 272000 ? tier("under-272k", p * 4 + cr * 0.4 + cc * 5 + cc12h * 10 + c * 20) : tier("over-272k", p * 8 + cr * 0.8 + cc * 10 + cc12h * 20 + c * 30)`
	for _, tc := range []struct {
		prompt int
		tier   string
		quota  int
	}{
		{prompt: 272000, tier: "under-272k", quota: 840450},
		{prompt: 272001, tier: "over-272k", quota: 1673406},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens:     tc.prompt,
				CompletionTokens: 1000,
				TotalTokens:      tc.prompt + 1000,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:            4000,
					CacheWriteTokens:        7000,
					CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{TTL30mTokens: 3000, TTL12hTokens: 4000},
				},
			}
			info := makeRelayInfo(expression, 1.5, 1000, 1000)
			params := BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
			ok, quota, result := TryTieredSettle(info, params)
			require.True(t, ok)
			require.NotNil(t, result, "真实表达式必须执行成功，不能退回预扣额度")
			require.Equal(t, tc.quota, quota)
			require.Equal(t, tc.tier, result.MatchedTier)
			require.Equal(t, tc.tier != info.TieredBillingSnapshot.EstimatedTier, result.CrossedTier)
		})
	}
}
