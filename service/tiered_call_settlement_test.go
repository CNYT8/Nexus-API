package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	tieredCallSettlementUserID    = 211
	tieredCallSettlementChannelID = 211
)

const (
	pureCallExpr = `tier("request", call(0.01))`
	hybridExpr   = `tier("mix", call(0.01) + p * 2 + c * 10)`
	branchExpr   = `len <= 32000 ? tier("short", call(0.01) + p * 2) : tier("long", p * 10)`
)

func newTieredCallSettlementContext(t *testing.T, expr string, groupRatio float64, preConsumed int, estimatePrompt int) (*gin.Context, *relaycommon.RelayInfo, *capturingBillingSettler) {
	t.Helper()
	seedUser(t, tieredCallSettlementUserID, 0)
	seedChannel(t, tieredCallSettlementChannelID)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	billing := &capturingBillingSettler{preConsumed: preConsumed}
	info := &relaycommon.RelayInfo{
		UserId:                tieredCallSettlementUserID,
		ChannelMeta:           &relaycommon.ChannelMeta{ChannelId: tieredCallSettlementChannelID, UpstreamModelName: "gpt-4o-mini"},
		OriginModelName:       "tiered-call-test",
		Billing:               billing,
		FinalPreConsumedQuota: preConsumed,
		StartTime:             time.Now(),
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: groupRatio},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:              billing_setting.BillingModeTieredExpr,
			ExprString:               expr,
			ExprHash:                 billingexpr.ExprHashString(expr),
			GroupRatio:               groupRatio,
			QuotaPerUnit:             common.QuotaPerUnit,
			EstimatedQuotaAfterGroup: preConsumed,
		},
	}
	info.SetEstimatePromptTokens(estimatePrompt)
	return ctx, info, billing
}

func getUserUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&user).Error)
	return user.UsedQuota
}

func TestTieredCallSettlementMissingUsage(t *testing.T) {
	t.Run("missing usage pure call bills the call price", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, pureCallExpr, 1, 300, 100)
		PostTextConsumeQuota(ctx, info, nil, nil)
		require.Equal(t, []int{5000}, billing.settled)
		require.Equal(t, 5000, getUserUsedQuota(t, tieredCallSettlementUserID))
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 5000, log.Quota)
	})

	t.Run("zero usage pure call bills the call price", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, pureCallExpr, 1, 700, 0)
		PostTextConsumeQuota(ctx, info, &dto.Usage{}, nil)
		require.Equal(t, []int{5000}, billing.settled)
		require.Equal(t, 5000, getUserUsedQuota(t, tieredCallSettlementUserID))
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 5000, log.Quota)
		require.NotContains(t, log.Content, "保守")
	})

	t.Run("explicit call zero stays free", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, `tier("free", call(0))`, 1, 700, 0)
		PostTextConsumeQuota(ctx, info, &dto.Usage{}, nil)
		require.Equal(t, []int{0}, billing.settled)
		require.Equal(t, 0, getUserUsedQuota(t, tieredCallSettlementUserID))
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 0, log.Quota)
		require.NotContains(t, log.Content, "保守")
	})

	t.Run("hybrid missing usage keeps the conservative floor", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, hybridExpr, 1, 20000, 0)
		PostTextConsumeQuota(ctx, info, nil, nil)
		require.Equal(t, []int{20000}, billing.settled)
		require.Equal(t, 20000, getUserUsedQuota(t, tieredCallSettlementUserID))
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 20000, log.Quota)
		require.Contains(t, log.Content, "保守")
	})

	t.Run("tool surcharge is not overwritten by the reservation fallback", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, hybridExpr, 1, 300, 0)
		info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 1},
			},
		}
		PostTextConsumeQuota(ctx, info, nil, nil)
		// call(0.01) = 5000 quota plus one web_search_preview call ($10/1K) = 5000.
		require.Equal(t, []int{10000}, billing.settled)
		require.Equal(t, 10000, getUserUsedQuota(t, tieredCallSettlementUserID))
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 10000, log.Quota)
		require.NotContains(t, log.Content, "保守")
	})

	t.Run("free group settles zero", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, pureCallExpr, 0, 700, 0)
		PostTextConsumeQuota(ctx, info, &dto.Usage{}, nil)
		require.Equal(t, []int{0}, billing.settled)
		require.Equal(t, 0, getUserUsedQuota(t, tieredCallSettlementUserID))
	})
}

func TestTieredCallSettlementBranchSwitch(t *testing.T) {
	t.Run("estimate short actual long settles the long tier", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, branchExpr, 1, 5000, 100)
		PostTextConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 50000, TotalTokens: 50000}, nil)
		require.Equal(t, []int{250000}, billing.settled)
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 250000, log.Quota)
		var other map[string]any
		require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
		require.Equal(t, "long", other["matched_tier"])
	})

	t.Run("estimate long actual short settles the short tier", func(t *testing.T) {
		truncate(t)
		ctx, info, billing := newTieredCallSettlementContext(t, branchExpr, 1, 250000, 50000)
		PostTextConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 100, TotalTokens: 100}, nil)
		require.Equal(t, []int{5100}, billing.settled)
		log := getLastLog(t)
		require.NotNil(t, log)
		require.Equal(t, 5100, log.Quota)
		var other map[string]any
		require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
		require.Equal(t, "short", other["matched_tier"])
	})
}

func TestTieredSettlementConservativeMatrix(t *testing.T) {
	const mixedBranches = `len < 1000 ? tier("request", call(0)) : tier("tokens", p*2)`
	for _, tc := range []struct {
		name, expr              string
		estimate, reserve, want int
		usage                   *dto.Usage
		tool, free              bool
	}{
		{name: "reversed multiplication", expr: `tier("a", call(1)+2*p)`, reserve: 900000, want: 900000, usage: &dto.Usage{}},
		{name: "token dependent call", expr: `tier("a", call(p))`, reserve: 900000, want: 900000, usage: &dto.Usage{}},
		{name: "nil hybrid keeps output reserve", expr: hybridExpr, estimate: 100, reserve: 20000, want: 20000},
		{name: "nil token keeps output reserve", expr: `tier("a", p*2+c*10)`, estimate: 100, reserve: 20000, want: 20000},
		{name: "token only in selection", expr: `len+c < 1000 ? tier("a", call(0)) : tier("b", call(1))`, reserve: 20000, want: 0, usage: &dto.Usage{}},
		{name: "token reserve to actual free call", expr: mixedBranches, estimate: 50000, reserve: 50000, want: 0, usage: &dto.Usage{}},
		{name: "nil usage selects token tier", expr: mixedBranches, estimate: 50000, reserve: 60000, want: 60000},
		{name: "call reserve to actual token tier", expr: mixedBranches, estimate: 100, reserve: 100, want: 50000, usage: &dto.Usage{PromptTokens: 50000, TotalTokens: 50000}},
		{name: "evaluation error keeps reserve", expr: `tier("a", param("missing")*p)`, estimate: 100, reserve: 20000, want: 20000},
		{name: "free evaluation error", expr: `tier("a", param("missing")*p)`, reserve: 20000, free: true, want: 0},
		{name: "explicit zero", expr: `tier("a", call(0))`, reserve: 20000, want: 0},
		{name: "overflow saturates", expr: `tier("a", call(1e20))`, reserve: 20000, want: 2147483647},
		{name: "estimated Responses keeps floor", expr: hybridExpr, reserve: 20000, want: 20000, usage: &dto.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, UsageSource: "responses_estimated"}},
	} {
		for _, audio := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/audio=%v", tc.name, audio), func(t *testing.T) {
				truncate(t)
				group := 1.0
				if tc.free {
					group = 0
				}
				ctx, info, billing := newTieredCallSettlementContext(t, tc.expr, group, tc.reserve, tc.estimate)
				info.TieredBillingSnapshot.EstimatedTier = "estimated"
				if audio {
					PostAudioConsumeQuota(ctx, info, tc.usage, "")
				} else {
					PostTextConsumeQuota(ctx, info, tc.usage, nil)
				}
				require.Equal(t, []int{tc.want}, billing.settled)
				log := getLastLog(t)
				require.Equal(t, tc.want, log.Quota)
				if strings.Contains(tc.name, "evaluation error") {
					var other map[string]any
					require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
					require.Equal(t, "estimated", other["matched_tier"])
				}
			})
		}
	}
}

func TestTieredSettlementToolFeesAndMultipliers(t *testing.T) {
	for _, tc := range []struct {
		name, expr    string
		reserve, want int
		group         float64
	}{
		{"hybrid reserve plus tool", hybridExpr, 20000, 25000, 1},
		{"free call still pays tool", `tier("a",call(0))`, 20000, 5000, 1},
		{"request multiplier excludes tool", pureCallExpr + ` * (param("fast") == true ? 2 : 1)`, 100, 22500, 1.5},
		{"free group", hybridExpr, 20000, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			ctx, info, billing := newTieredCallSettlementContext(t, tc.expr, tc.group, tc.reserve, 0)
			info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(`{"fast":true}`)}
			info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{dto.BuildInToolWebSearchPreview: {CallCount: 1}}}
			PostTextConsumeQuota(ctx, info, nil, nil)
			require.Equal(t, []int{tc.want}, billing.settled)
		})
	}
}

func TestTieredSettlementRealWalletAndFailureBarrier(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("DB failure=%v", fail), func(t *testing.T) {
			truncate(t)
			ctx, info, _ := newTieredCallSettlementContext(t, pureCallExpr, 1, 2000, 100)
			seedToken(t, 211, 211, "tiered-real", 10000)
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 211).Update("quota", 10000).Error)
			info.Billing = nil
			info.TokenId = 211
			info.TokenKey = "tiered-real"
			info.UserSetting.BillingPreference = "wallet_only"
			require.Nil(t, PreConsumeBilling(ctx, 2000, info))
			require.Equal(t, 8000, getUserQuota(t, 211))
			require.Equal(t, 8000, getTokenRemainQuota(t, 211))
			session := info.Billing.(*BillingSession)
			if fail {
				const callback = "tiered-fail-settlement"
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "users" {
						tx.AddError(errors.New("database unavailable"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
			}
			PostTextConsumeQuota(ctx, info, nil, nil)
			if fail {
				require.Error(t, session.Settle(5000))
				require.Equal(t, 8000, getUserQuota(t, 211))
				require.Equal(t, 8000, getTokenRemainQuota(t, 211))
			} else {
				require.NoError(t, session.Settle(5000))
				require.Equal(t, 5000, getUserQuota(t, 211))
				require.Equal(t, 5000, getTokenRemainQuota(t, 211))
			}
			session.Refund(ctx)
			require.False(t, session.NeedsRefund())
			require.False(t, session.refunded)
		})
	}
}
