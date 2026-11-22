package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type textQuotaSummary struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	CacheTokens              int
	CacheCreationTokens      int
	CacheCreationTokens5m    int
	CacheCreationTokens1h    int
	CacheCreationTokens12h   int
	ImageTokens              int
	AudioTokens              int
	ModelName                string
	TokenName                string
	UseTimeSeconds           int64
	CompletionRatio          float64
	CacheRatio               float64
	ImageRatio               float64
	ModelRatio               float64
	GroupRatio               float64
	ModelPrice               float64
	CacheCreationRatio       float64
	CacheCreationRatio5m     float64
	CacheCreationRatio1h     float64
	CacheCreationRatio12h    float64
	Quota                    int
	IsClaudeUsageSemantic    bool
	UsageSemantic            string
	WebSearchPrice           float64
	WebSearchCallCount       int
	ClaudeWebSearchPrice     float64
	ClaudeWebSearchCallCount int
	FileSearchPrice          float64
	FileSearchCallCount      int
	AudioInputPrice          float64
	ImageGenerationCallPrice float64
	ImageGenerationCallCount int
	ToolSurchargeItems       []ToolSurchargeItem
	ToolCallSurchargeQuota   decimal.Decimal
}

// hasBillableUsage reports whether this request should incur any charge.
// Claude reports cache tokens separately from ordinary input and output;
// a request can also carry zero ordinary tokens yet still be billable via a
// tool-call surcharge or fixed model price.
func (s *textQuotaSummary) hasBillableUsage() bool {
	return s.ModelPrice != 0 || s.TotalTokens > 0 ||
		(s.IsClaudeUsageSemantic && (s.CacheTokens > 0 || s.CacheCreationTokens > 0 ||
			s.CacheCreationTokens5m > 0 || s.CacheCreationTokens1h > 0)) ||
		!s.ToolCallSurchargeQuota.IsZero()
}
func cacheWriteTokensTotal(summary textQuotaSummary) int {
	if summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0 {
		splitCacheWriteTokens := summary.CacheCreationTokens5m + summary.CacheCreationTokens1h
		if summary.CacheCreationTokens > splitCacheWriteTokens {
			return summary.CacheCreationTokens
		}
		return splitCacheWriteTokens
	}
	return summary.CacheCreationTokens
}

func injectTextCacheWriteInfo(other map[string]interface{}, summary textQuotaSummary) {
	if summary.CacheCreationTokens > 0 {
		other["cache_creation_tokens"] = summary.CacheCreationTokens
		other["cache_creation_ratio"] = summary.CacheCreationRatio
	}
	if summary.CacheCreationTokens5m > 0 {
		other["cache_creation_tokens_5m"] = summary.CacheCreationTokens5m
		other["cache_creation_ratio_5m"] = summary.CacheCreationRatio5m
	}
	if summary.CacheCreationTokens1h > 0 {
		other["cache_creation_tokens_1h"] = summary.CacheCreationTokens1h
		other["cache_creation_ratio_1h"] = summary.CacheCreationRatio1h
	}
	if summary.CacheCreationTokens12h > 0 {
		other["cache_creation_tokens_12h"] = summary.CacheCreationTokens12h
		other["cache_creation_ratio_12h"] = summary.CacheCreationRatio12h
	}
	if cacheWriteTokens := cacheWriteTokensTotal(summary); cacheWriteTokens > 0 {
		// 12h 写入已包含在总量中，不应重复累加。
		other["cache_write_tokens"] = cacheWriteTokens
	}
}

func isLegacyClaudeDerivedOpenAIUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if relayInfo == nil || usage == nil {
		return false
	}
	if relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return false
	}
	if usage.UsageSource != "" || usage.UsageSemantic != "" {
		return false
	}
	return usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0
}

// ToolSurchargeItem uses dollars per 1000 calls for every tool, including images.
// Usage.ToolUsage is authoritative when present; ResponsesUsageInfo.BuiltInTools
// and legacy context counts are compatibility fallbacks, never duplicate usage.
type ToolSurchargeItem struct {
	Name  string  `json:"name"`
	Count int     `json:"count"`
	Price float64 `json:"price"`
}

// CalculateToolUsageSurcharge returns unrounded quota including group pricing.
// Callers add it once before final rounding/settlement and log tool_surcharges.
func CalculateToolUsageSurcharge(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) (decimal.Decimal, []ToolSurchargeItem) {
	if ctx == nil || relayInfo == nil {
		return decimal.Zero, nil
	}
	summary := textQuotaSummary{ModelName: relayInfo.OriginModelName, GroupRatio: relayInfo.PriceData.GroupRatioInfo.GroupRatio}
	quota := calculateTextToolCallSurcharge(ctx, relayInfo, usage, &summary)
	return quota, summary.ToolSurchargeItems
}

func calculateTextToolCallSurcharge(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, summary *textQuotaSummary) decimal.Decimal {
	counts := make(map[string]int)
	authoritative := usage != nil && usage.ToolUsage != nil
	if authoritative {
		for name, count := range usage.ToolUsage {
			counts[name] = count
		}
	} else if relayInfo.ResponsesUsageInfo != nil {
		for name, tool := range relayInfo.ResponsesUsageInfo.BuiltInTools {
			if tool != nil && tool.CallCount > 0 {
				counts[name] = tool.CallCount
			}
		}
	} else if strings.HasSuffix(summary.ModelName, "search-preview") {
		counts["web_search_preview"] = 1
	}
	for name, key := range map[string]string{
		"web_search":    "claude_web_search_requests",
		"google_search": "google_search_requests",
	} {
		if !authoritative && counts[name] == 0 && ctx.GetInt(key) > 0 {
			counts[name] = ctx.GetInt(key)
		}
	}
	legacyImages := !authoritative && counts["image_generation"] == 0
	if legacyImages {
		counts["image_generation"] = ctx.GetInt("image_generation_call_count")
		if counts["image_generation"] <= 0 && ctx.GetBool("image_generation_call") {
			counts["image_generation"] = 1
		}
	}
	imageQuality, imageSize := ctx.GetString("image_generation_call_quality"), ctx.GetString("image_generation_call_size")
	useLegacyImagePrice := legacyImages || ctx.GetBool("image_generation_call") && imageQuality != "" && imageSize != ""
	var surcharge decimal.Decimal
	for name, count := range counts {
		if count <= 0 {
			continue
		}
		limit := relaycommon.MaxBillableToolCallCount
		if name == "image_generation" {
			limit = dto.MaxImageN
		}
		if count > limit {
			logger.LogWarn(ctx, fmt.Sprintf("tool count clamped: tool=%s count=%d", name, count))
			count = limit
		}
		price := operation_setting.GetToolPriceForModel(name, summary.ModelName)
		if name == "image_generation" && useLegacyImagePrice && !operation_setting.HasConfiguredToolPrice(name, summary.ModelName) {
			price = operation_setting.GetGPTImage1PriceOnceCall(imageQuality, imageSize) * 1000
		}
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			continue
		}
		summary.ToolSurchargeItems = append(summary.ToolSurchargeItems, ToolSurchargeItem{Name: name, Count: count, Price: price})
		surcharge = surcharge.Add(decimal.NewFromFloat(price).
			Mul(decimal.NewFromInt(int64(count))).Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
		switch name {
		case "web_search_preview":
			summary.WebSearchCallCount, summary.WebSearchPrice = count, price
		case "web_search":
			summary.ClaudeWebSearchCallCount, summary.ClaudeWebSearchPrice = count, price
		case "file_search":
			summary.FileSearchCallCount, summary.FileSearchPrice = count, price
		case "image_generation":
			summary.ImageGenerationCallCount, summary.ImageGenerationCallPrice = count, price/1000
		}
	}
	sort.Slice(summary.ToolSurchargeItems, func(i, j int) bool { return summary.ToolSurchargeItems[i].Name < summary.ToolSurchargeItems[j].Name })
	return surcharge
}

func noteQuotaClamp(relayInfo *relaycommon.RelayInfo, clamp *common.QuotaClamp) {
	if clamp == nil || relayInfo == nil {
		return
	}
	if relayInfo.QuotaClamp == nil {
		relayInfo.QuotaClamp = clamp
	}
}

// Only a successful evaluation and an AST proof of the actual price may relax
// the missing-usage floor. Display regexes and the estimated tier are not money
// authorities (call(p), for example, is usage dependent).
func isUsageIndependentSettlement(info *relaycommon.RelayInfo, result *billingexpr.TieredResult) bool {
	return result != nil && result.Clamp == nil && info.TieredBillingSnapshot != nil &&
		billingexpr.IsUsageIndependentPrice(info.TieredBillingSnapshot.ExprString, result.MatchedTier)
}

func missingSettlementUsage(usage *dto.Usage) bool {
	if usage == nil || usage.UsageSource == "responses_estimated" {
		return true
	}
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		return false
	}
	return usage.PromptTokensDetails.CachedTokens <= 0 &&
		usage.PromptTokensDetails.CacheCreationTokensTotal() <= 0 &&
		usage.ClaudeCacheCreation5mTokens <= 0 &&
		usage.ClaudeCacheCreation1hTokens <= 0
}

func composeTieredTextQuota(relayInfo *relaycommon.RelayInfo, summary textQuotaSummary, tieredQuota int, tieredResult *billingexpr.TieredResult) int {
	if summary.ToolCallSurchargeQuota.IsZero() {
		return tieredQuota
	}

	if tieredResult != nil && tieredResult.Clamp == nil && !math.IsNaN(tieredResult.ActualQuotaBeforeGroup) && !math.IsInf(tieredResult.ActualQuotaBeforeGroup, 0) {
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			quota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(tieredResult.ActualQuotaBeforeGroup).
				Mul(decimal.NewFromFloat(snap.GroupRatio)).
				Add(summary.ToolCallSurchargeQuota))
			noteQuotaClamp(relayInfo, clamp)
			return quota
		}
	}

	quota, clamp := common.QuotaFromDecimalChecked(
		decimal.NewFromInt(int64(tieredQuota)).Add(summary.ToolCallSurchargeQuota),
	)
	noteQuotaClamp(relayInfo, clamp)
	return quota
}

func calculateTextQuotaSummary(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) textQuotaSummary {
	summary := textQuotaSummary{
		ModelName:             relayInfo.OriginModelName,
		TokenName:             ctx.GetString("token_name"),
		UseTimeSeconds:        time.Now().Unix() - relayInfo.StartTime.Unix(),
		CompletionRatio:       relayInfo.PriceData.CompletionRatio,
		CacheRatio:            relayInfo.PriceData.CacheRatio,
		ImageRatio:            relayInfo.PriceData.ImageRatio,
		ModelRatio:            relayInfo.PriceData.ModelRatio,
		GroupRatio:            relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelPrice:            relayInfo.PriceData.ModelPrice,
		CacheCreationRatio:    relayInfo.PriceData.CacheCreationRatio,
		CacheCreationRatio5m:  relayInfo.PriceData.CacheCreation5mRatio,
		CacheCreationRatio1h:  relayInfo.PriceData.CacheCreation1hRatio,
		CacheCreationRatio12h: relayInfo.PriceData.CacheCreation12hRatio,
		UsageSemantic:         usageSemanticFromUsage(relayInfo, usage),
	}
	summary.IsClaudeUsageSemantic = summary.UsageSemantic == "anthropic"

	if usage == nil {
		usage = &dto.Usage{
			PromptTokens:     relayInfo.GetEstimatePromptTokens(),
			CompletionTokens: 0,
			TotalTokens:      relayInfo.GetEstimatePromptTokens(),
		}
	}

	summary.PromptTokens = usage.PromptTokens
	summary.CompletionTokens = usage.CompletionTokens
	summary.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	summary.CacheTokens = usage.PromptTokensDetails.CachedTokens
	summary.CacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokensTotal()
	summary.CacheCreationTokens5m = usage.ClaudeCacheCreation5mTokens
	summary.CacheCreationTokens1h = usage.ClaudeCacheCreation1hTokens
	summary.ImageTokens = usage.PromptTokensDetails.ImageTokens
	summary.AudioTokens = usage.PromptTokensDetails.AudioTokens
	legacyClaudeDerived := isLegacyClaudeDerivedOpenAIUsage(relayInfo, usage)
	if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
		summary.CacheCreationTokens12h = usage.PromptTokensDetails.CacheWrite12hTokens()
	}
	isOpenRouterClaudeBilling := relayInfo.ChannelMeta != nil &&
		relayInfo.ChannelType == constant.ChannelTypeOpenRouter &&
		summary.IsClaudeUsageSemantic

	if isOpenRouterClaudeBilling {
		summary.PromptTokens -= summary.CacheTokens
		isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(summary.ModelName, relayInfo.PriceData.ModelRatio)
		if summary.CacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && usage.Cost != 0 && !isUsingCustomSettings {
			maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
			if maybeCacheCreationTokens >= 0 && summary.PromptTokens >= maybeCacheCreationTokens {
				summary.CacheCreationTokens = maybeCacheCreationTokens
			}
		}
		summary.PromptTokens -= summary.CacheCreationTokens
		if summary.PromptTokens < 0 {
			summary.PromptTokens = 0
		}
	}

	dPromptTokens := decimal.NewFromInt(int64(summary.PromptTokens))
	dCacheTokens := decimal.NewFromInt(int64(summary.CacheTokens))
	dImageTokens := decimal.NewFromInt(int64(summary.ImageTokens))
	dAudioTokens := decimal.NewFromInt(int64(summary.AudioTokens))
	dCompletionTokens := decimal.NewFromInt(int64(summary.CompletionTokens))
	dCachedCreationTokens := decimal.NewFromInt(int64(summary.CacheCreationTokens))
	dCompletionRatio := decimal.NewFromFloat(summary.CompletionRatio)
	dCacheRatio := decimal.NewFromFloat(summary.CacheRatio)
	dImageRatio := decimal.NewFromFloat(summary.ImageRatio)
	dModelRatio := decimal.NewFromFloat(summary.ModelRatio)
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dModelPrice := decimal.NewFromFloat(summary.ModelPrice)
	dCacheCreationRatio := decimal.NewFromFloat(summary.CacheCreationRatio)
	dCacheCreationRatio5m := decimal.NewFromFloat(summary.CacheCreationRatio5m)
	dCacheCreationRatio1h := decimal.NewFromFloat(summary.CacheCreationRatio1h)
	dCacheCreationRatio12h := decimal.NewFromFloat(summary.CacheCreationRatio12h)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)

	ratio := dModelRatio.Mul(dGroupRatio)
	summary.ToolCallSurchargeQuota = calculateTextToolCallSurcharge(ctx, relayInfo, usage, &summary)

	var audioInputQuota decimal.Decimal
	if !relayInfo.PriceData.UsePrice {
		baseTokens := dPromptTokens

		var cachedTokensWithRatio decimal.Decimal
		if !dCacheTokens.IsZero() {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCacheTokens)
			}
			cachedTokensWithRatio = dCacheTokens.Mul(dCacheRatio)
		}

		var cachedCreationTokensWithRatio decimal.Decimal
		hasSplitCacheCreationTokens := summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0
		if !dCachedCreationTokens.IsZero() || hasSplitCacheCreationTokens {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCachedCreationTokens)
				// 总写入量已包含 12h，普通输入只扣除一次总写入量。
				dCachedCreationTokens12h := decimal.NewFromInt(int64(summary.CacheCreationTokens12h))
				cachedCreationTokensWithRatio = dCachedCreationTokens.Sub(dCachedCreationTokens12h).Mul(dCacheCreationRatio)
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(dCachedCreationTokens12h.Mul(dCacheCreationRatio12h))
			} else {
				remaining := summary.CacheCreationTokens - summary.CacheCreationTokens5m - summary.CacheCreationTokens1h
				if remaining < 0 {
					remaining = 0
				}
				cachedCreationTokensWithRatio = decimal.NewFromInt(int64(remaining)).Mul(dCacheCreationRatio)
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens5m)).Mul(dCacheCreationRatio5m))
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens1h)).Mul(dCacheCreationRatio1h))
			}
		}

		var imageTokensWithRatio decimal.Decimal
		if !dImageTokens.IsZero() {
			baseTokens = baseTokens.Sub(dImageTokens)
			imageTokensWithRatio = dImageTokens.Mul(dImageRatio)
		}

		if !dAudioTokens.IsZero() {
			summary.AudioInputPrice = operation_setting.GetGeminiInputAudioPricePerMillionTokens(summary.ModelName)
			if summary.AudioInputPrice > 0 {
				baseTokens = baseTokens.Sub(dAudioTokens)
				audioInputQuota = decimal.NewFromFloat(summary.AudioInputPrice).
					Div(decimal.NewFromInt(1000000)).Mul(dAudioTokens).Mul(dGroupRatio).Mul(dQuotaPerUnit)
			}
		}

		if baseTokens.IsNegative() {
			baseTokens = decimal.Zero
		}

		promptQuota := baseTokens.Add(cachedTokensWithRatio).Add(imageTokensWithRatio).Add(cachedCreationTokensWithRatio)
		completionQuota := dCompletionTokens.Mul(dCompletionRatio)
		quotaCalculateDecimal := promptQuota.Add(completionQuota).Mul(ratio)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ToolCallSurchargeQuota)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)

		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)

		if !ratio.IsZero() && quotaCalculateDecimal.LessThanOrEqual(decimal.Zero) {
			quotaCalculateDecimal = decimal.NewFromInt(1)
		}
		quota, clamp := common.QuotaFromDecimalChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	} else {
		quotaCalculateDecimal := dModelPrice.Mul(dQuotaPerUnit).Mul(dGroupRatio)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ToolCallSurchargeQuota)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)
		quota, clamp := common.QuotaFromDecimalChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	}

	if !summary.hasBillableUsage() {
		summary.Quota = 0
	} else if !ratio.IsZero() && summary.Quota == 0 {
		summary.Quota = 1
	}
	return summary
}

func usageSemanticFromUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) string {
	if usage != nil && usage.UsageSemantic != "" {
		return usage.UsageSemantic
	}
	if relayInfo != nil && relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return "anthropic"
	}
	return "openai"
}

func PostTextConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []string) {
	originUsage := usage
	if usage == nil {
		extraContent = append(extraContent, "上游无计费信息")
	}
	usage = normalizeChannelSystemPromptUsage(relayInfo, usage)
	if originUsage != nil {
		ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, relayInfo.GetFinalRequestRelayFormat())
	}

	adminRejectReason := common.GetContextKeyString(ctx, constant.ContextKeyAdminRejectReason)
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	snap := relayInfo.TieredBillingSnapshot
	missingUsage := missingSettlementUsage(originUsage) || !summary.hasBillableUsage()

	// Evaluate all tiered expressions with the existing prompt estimate when
	// usage is missing, but do not mistake that estimate for reported usage.
	billingUsage := usage
	if billingUsage == nil && snap != nil {
		billingUsage = &dto.Usage{
			PromptTokens:     summary.PromptTokens,
			CompletionTokens: summary.CompletionTokens,
			TotalTokens:      summary.TotalTokens,
		}
	}

	var tieredResult *billingexpr.TieredResult
	tieredBillingApplied := false
	usedConservative := false
	if billingUsage != nil {
		var tieredUsedVars map[string]bool
		if snap != nil {
			tieredUsedVars = billingexpr.UsedVars(snap.ExprString)
		}
		tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(billingUsage, summary.IsClaudeUsageSemantic, tieredUsedVars))
		if tieredOk {
			tieredBillingApplied = true
			tieredResult = tieredRes
			if snap.GroupRatio == 0 {
				tieredQuota = 0
				tieredRes = nil
			}
			if missingUsage && !isUsageIndependentSettlement(relayInfo, tieredRes) {
				// The reservation covers the expression, not separately metered
				// tools. Apply the floor BEFORE adding their surcharge.
				if floor := conservativeSettlementQuota(relayInfo); floor >= tieredQuota {
					usedConservative = true
					tieredQuota = floor
					tieredRes = nil // compose from the retained base, not the evaluated base
				}
			}
			summary.Quota = composeTieredTextQuota(relayInfo, summary, tieredQuota, tieredRes)
			// Tiered expressions replace legacy token/audio prices altogether;
			// tools remain separate. Do not log an uncharged legacy audio fee.
			summary.AudioInputPrice = 0
		}
	}

	if summary.WebSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Web Search 调用 %d 次，调用花费 %s", summary.WebSearchCallCount, decimal.NewFromFloat(summary.WebSearchPrice).Mul(decimal.NewFromInt(int64(summary.WebSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}
	if summary.ClaudeWebSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s", summary.ClaudeWebSearchCallCount, decimal.NewFromFloat(summary.ClaudeWebSearchPrice).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromInt(int64(summary.ClaudeWebSearchCallCount))).String()))
	}
	if summary.FileSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("File Search 调用 %d 次，调用花费 %s", summary.FileSearchCallCount, decimal.NewFromFloat(summary.FileSearchPrice).Mul(decimal.NewFromInt(int64(summary.FileSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Audio Input 花费 %s", decimal.NewFromFloat(summary.AudioInputPrice).Div(decimal.NewFromInt(1000000)).Mul(decimal.NewFromInt(int64(summary.AudioTokens))).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}
	if summary.ImageGenerationCallPrice > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Image Generation 调用 %d 次，花费 %s", summary.ImageGenerationCallCount, decimal.NewFromFloat(summary.ImageGenerationCallPrice).Mul(decimal.NewFromInt(int64(summary.ImageGenerationCallCount))).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).String()))
	}

	if missingUsage {
		if !tieredBillingApplied {
			usedConservative = true
			floor := conservativeSettlementQuota(relayInfo)
			floorWithTools := composeTieredTextQuota(relayInfo, summary, floor, nil)
			if floorWithTools > summary.Quota {
				summary.Quota = floorWithTools
			}
		}
		retainedQuota := summary.Quota
		if usedConservative {
			extraContent = append(extraContent, "上游没有返回计费信息，无法精确结算，按预扣额度保守计费")
			logger.LogError(ctx, fmt.Sprintf("missing usage, settle with conservative quota, userId %d, channelId %d, tokenId %d, model %s, quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, summary.ModelName, retainedQuota))
		}
		if retainedQuota > 0 {
			model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, retainedQuota)
			model.UpdateChannelUsedQuota(relayInfo.ChannelId, retainedQuota)
		}
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, summary.Quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, summary.Quota)
	}

	if err := SettleBilling(ctx, relayInfo, summary.Quota); err != nil {
		logger.LogError(ctx, "error settling billing: "+err.Error())
	}

	logModel := summary.ModelName
	if strings.HasPrefix(logModel, "gpt-4-gizmo") {
		logModel = "gpt-4-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}
	if strings.HasPrefix(logModel, "gpt-4o-gizmo") {
		logModel = "gpt-4o-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}

	logContent := strings.Join(extraContent, ", ")
	var other map[string]interface{}
	if summary.IsClaudeUsageSemantic {
		other = GenerateClaudeOtherInfo(ctx, relayInfo,
			summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio,
			summary.CacheTokens, summary.CacheRatio,
			summary.CacheCreationTokens, summary.CacheCreationRatio,
			summary.CacheCreationTokens5m, summary.CacheCreationRatio5m,
			summary.CacheCreationTokens1h, summary.CacheCreationRatio1h,
			summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
		other["usage_semantic"] = "anthropic"
	} else {
		other = GenerateTextOtherInfo(ctx, relayInfo, summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio, summary.CacheTokens, summary.CacheRatio, summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	if adminRejectReason != "" {
		model.SetLogOtherAdminField(other, "reject_reason", adminRejectReason)
	}
	if summary.ImageTokens != 0 {
		other["image"] = true
		other["image_ratio"] = summary.ImageRatio
		other["image_output"] = summary.ImageTokens
	}
	if summary.WebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = summary.WebSearchCallCount
		other["web_search_price"] = summary.WebSearchPrice
	} else if summary.ClaudeWebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = summary.ClaudeWebSearchCallCount
		other["web_search_price"] = summary.ClaudeWebSearchPrice
	}
	if summary.FileSearchCallCount > 0 {
		other["file_search"] = true
		other["file_search_call_count"] = summary.FileSearchCallCount
		other["file_search_price"] = summary.FileSearchPrice
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		other["audio_input_seperate_price"] = true
		other["audio_input_token_count"] = summary.AudioTokens
		other["audio_input_price"] = summary.AudioInputPrice
	}
	if summary.ImageGenerationCallPrice > 0 {
		other["image_generation_call"] = true
		other["image_generation_call_price"] = summary.ImageGenerationCallPrice
		other["image_generation_call_count"] = summary.ImageGenerationCallCount
	}
	if len(summary.ToolSurchargeItems) > 0 {
		other["tool_surcharges"] = summary.ToolSurchargeItems
	}
	injectTextCacheWriteInfo(other, summary)
	if relayInfo.GetFinalRequestRelayFormat() != types.RelayFormatClaude && usage != nil && usage.UsageSource != "" && usage.InputTokens > 0 {
		// input_tokens_total: explicit normalized total input used by the usage log UI.
		// Only write this field when upstream/current conversion has already provided a
		// reliable total input value and tagged the usage source. Do not infer it from
		// prompt/cache fields here, otherwise old upstream payloads may be double-counted.
		other["input_tokens_total"] = usage.InputTokens
	}
	if tieredBillingApplied {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)

	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     summary.PromptTokens,
		CompletionTokens: summary.CompletionTokens,
		ModelName:        logModel,
		TokenName:        summary.TokenName,
		Quota:            summary.Quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(summary.UseTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	gopool.Go(func() {
		perfmetrics.RecordRelaySample(relayInfo, true, int64(summary.PromptTokens), int64(summary.CompletionTokens))
	})
}
