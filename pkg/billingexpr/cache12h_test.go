package billingexpr_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/require"
)

// 保留线上报错原文，确保预扣和最终结算都能编译同一表达式。
const gpt56SolCacheExpr = `len <= 272000 ? tier("under-272k", p * 4 + cr * 0.4 + cc * 5 + cc12h * 10 + c * 20) : tier("over-272k", p * 8 + cr * 0.8 + cc * 10 + cc12h * 20 + c * 30)`

func TestCache12hReportedExpression(t *testing.T) {
	for _, tc := range []struct {
		name  string
		len   float64
		cc12h float64
		tier  string
		cost  float64
	}{
		{name: "阈值以内无长缓存", len: 272000, tier: "under-272k", cost: 449000},
		{name: "超过阈值无长缓存", len: 272001, tier: "over-272k", cost: 888000},
		{name: "阈值以内含长缓存", len: 272000, cc12h: 2000, tier: "under-272k", cost: 469000},
		{name: "超过阈值含长缓存", len: 272001, cc12h: 2000, tier: "over-272k", cost: 928000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := billingexpr.TokenParams{P: 100000, C: 1000, Len: tc.len, CR: 10000, CC: 5000, CC12h: tc.cc12h}
			cost, trace, err := billingexpr.RunExpr(gpt56SolCacheExpr, params)
			require.NoError(t, err)
			require.Equal(t, tc.cost, cost)
			require.Equal(t, tc.tier, trace.MatchedTier)

			snapshot := &billingexpr.BillingSnapshot{
				ExprString:    gpt56SolCacheExpr,
				ExprHash:      billingexpr.ExprHashString(gpt56SolCacheExpr),
				GroupRatio:    1.5,
				QuotaPerUnit:  500000,
				EstimatedTier: "under-272k",
			}
			result, err := billingexpr.ComputeTieredQuota(snapshot, params)
			require.NoError(t, err)
			require.Equal(t, tc.cost/2, result.ActualQuotaBeforeGroup)
			require.Equal(t, int(tc.cost*0.75), result.ActualQuotaAfterGroup)
			require.Equal(t, tc.tier, result.MatchedTier)
			require.Equal(t, tc.tier != snapshot.EstimatedTier, result.CrossedTier)
		})
	}
}

func TestCache12hExplicitZeroRemainsOptIn(t *testing.T) {
	const expression = `p * 4 + cc12h * 0`
	require.True(t, billingexpr.UsedVars(expression)["cc12h"], "显式零价格仍需从输入中扣除长缓存，不能走旧公式回退")
	cost, _, err := billingexpr.RunExpr(expression, billingexpr.TokenParams{P: 1000, CC12h: 4000})
	require.NoError(t, err)
	require.Equal(t, float64(4000), cost)
}
