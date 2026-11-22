package service

import (
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// TieredResultWrapper wraps billingexpr.TieredResult for use at the service layer.
type TieredResultWrapper = billingexpr.TieredResult

// BuildTieredTokenParams constructs billingexpr.TokenParams from a dto.Usage,
// normalizing P and C so they mean "tokens not separately priced by the
// expression". Sub-categories (cache, image, audio) are only subtracted
// when the expression references them via their own variable.
//
// GPT-format APIs report prompt_tokens / completion_tokens as totals that
// include all sub-categories (cache, image, audio). Claude-format APIs
// report them as text-only. This function normalizes to text-only when
// sub-categories are separately priced.
func BuildTieredTokenParams(usage *dto.Usage, isClaudeUsageSemantic bool, usedVars map[string]bool) billingexpr.TokenParams {
	p := float64(usage.PromptTokens)
	c := float64(usage.CompletionTokens)
	cr := float64(usage.PromptTokensDetails.CachedTokens)
	cc5m := float64(usage.PromptTokensDetails.CacheCreationTokensTotal())
	cc1h := float64(0)
	cc12h := float64(0)

	if usage.UsageSemantic == "anthropic" {
		// Upstreams may report the cache-write total without a full 5m/1h
		// split; the unlabelled remainder is billed as 5m writes.
		split5m, split1h := NormalizeCacheCreationSplit(
			usage.PromptTokensDetails.CacheCreationTokensTotal(),
			usage.ClaudeCacheCreation5mTokens,
			usage.ClaudeCacheCreation1hTokens,
		)
		cc5m = float64(split5m)
		cc1h = float64(split1h)
	}

	img := float64(usage.PromptTokensDetails.ImageTokens)
	ai := float64(usage.PromptTokensDetails.AudioTokens)
	imgO := float64(usage.CompletionTokenDetails.ImageTokens)
	ao := float64(usage.CompletionTokenDetails.AudioTokens)

	// len = total input context length for tier condition evaluation.
	// Non-Claude: prompt_tokens already includes everything.
	// Claude: input_tokens is text-only, so add cache read + cache creation.
	inputLen := p
	if isClaudeUsageSemantic {
		inputLen = p + cr + cc5m + cc1h
	}

	if isClaudeUsageSemantic {
		// 未单独定价的缓存并入输入；一小时写入按短缓存价格折算。
		if !usedVars["cr"] {
			p += cr
		}
		if !usedVars["cc1h"] {
			cc5m += cc1h * types.ClaudeCacheCreation1hMultiplier
			cc1h = 0
		}
		if !usedVars["cc"] {
			p += cc5m
			cc5m = 0
		}
	} else {
		if usedVars["cr"] {
			p -= cr
		}
		// 输入总量中的写入只扣一次，旧公式中的长缓存则按短缓存的两倍折算。
		cc12h = float64(usage.PromptTokensDetails.CacheWrite12hTokens())
		cc5m -= cc12h
		ccTokens := cc5m
		if usedVars["cc12h"] {
			p -= cc12h
		} else {
			ccTokens += cc12h
			cc5m += cc12h * types.OpenAICacheCreation12hMultiplier
			cc12h = 0
		}
		if usedVars["cc"] {
			p -= ccTokens
		} else {
			p += cc5m - ccTokens
			cc5m = 0
		}
		if usedVars["cc1h"] {
			p -= cc1h
		}
		if usedVars["img"] {
			p -= img
		}
		if usedVars["ai"] {
			p -= ai
		}
		if usedVars["img_o"] {
			c -= imgO
		}
		if usedVars["ao"] {
			c -= ao
		}
	}

	if p < 0 {
		p = 0
	}
	if c < 0 {
		c = 0
	}

	return billingexpr.TokenParams{
		P:     p,
		C:     c,
		Len:   inputLen,
		CR:    cr,
		CC:    cc5m,
		CC1h:  cc1h,
		CC12h: cc12h,
		Img:   img,
		ImgO:  imgO,
		AI:    ai,
		AO:    ao,
	}
}

func refreshTieredBillingGroup(relayInfo *relaycommon.RelayInfo) (*billingexpr.BillingSnapshot, error) {
	if relayInfo == nil {
		return nil, nil
	}
	snapshot := relayInfo.TieredBillingSnapshot
	if snapshot == nil || snapshot.BillingMode != "tiered_expr" {
		return nil, nil
	}

	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	if snapshot.GroupRatio == groupRatio {
		return snapshot, nil
	}

	estimatedAfterGroup := snapshot.EstimatedQuotaBeforeGroup * groupRatio
	estimatedQuota, err := billingexpr.QuotaRoundStrict(estimatedAfterGroup)
	if err != nil {
		return nil, err
	}
	snapshot.GroupRatio = groupRatio
	snapshot.EstimatedQuotaAfterGroup = estimatedQuota
	return snapshot, nil
}

// PrepareTieredBillingForSelectedGroup refreshes routing-dependent billing
// before each upstream attempt and reserves any higher estimate before send.
func PrepareTieredBillingForSelectedGroup(c *gin.Context, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	snapshot, err := refreshTieredBillingGroup(relayInfo)
	if err != nil {
		return types.NewErrorWithStatusCode(
			err,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if snapshot == nil || snapshot.GroupRatio == 0 {
		return nil
	}

	relayInfo.PriceData.FreeModel = false
	if relayInfo.Billing == nil {
		return PreConsumeBilling(c, snapshot.EstimatedQuotaAfterGroup, relayInfo)
	}
	if err := relayInfo.Billing.Reserve(snapshot.EstimatedQuotaAfterGroup); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	relayInfo.FinalPreConsumedQuota = relayInfo.Billing.GetPreConsumedQuota()
	return nil
}

// TryTieredSettle checks if the request uses tiered_expr billing and, if so,
// computes the actual quota using the selected group's BillingSnapshot. Returns:
//   - ok=true, quota, result  when tiered billing applies
//   - ok=false, 0, nil        when it doesn't (caller should fall through to existing logic)
func TryTieredSettle(relayInfo *relaycommon.RelayInfo, params billingexpr.TokenParams) (ok bool, quota int, result *billingexpr.TieredResult) {
	snap := relayInfo.TieredBillingSnapshot
	if snap == nil || snap.BillingMode != "tiered_expr" {
		return false, 0, nil
	}

	requestInput := billingexpr.RequestInput{}
	if relayInfo.BillingRequestInput != nil {
		requestInput = *relayInfo.BillingRequestInput
	}

	tr, err := billingexpr.ComputeTieredQuotaWithRequest(snap, params, requestInput)
	if err != nil {
		quota = relayInfo.FinalPreConsumedQuota
		if quota <= 0 {
			quota = snap.EstimatedQuotaAfterGroup
		}
		return true, quota, nil
	}

	noteQuotaClamp(relayInfo, tr.Clamp)
	return true, tr.ActualQuotaAfterGroup, &tr
}
