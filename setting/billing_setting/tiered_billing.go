package billing_setting

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// Quota type classification for tiered billing expressions, consumed by the
// pricing API so both frontends share one source of truth.
const (
	TieredQuotaToken  = 0 // every tier prices tokens only
	TieredQuotaCall   = 1 // every tier is a fixed per-request charge
	TieredQuotaHybrid = 2 // some tiers (or tier terms) mix per-call and per-token
)

var (
	tieredCallPattern = regexp.MustCompile(`\bcall\s*\(`)
	// A per-request charge may also be written by hand as "0.25 * 1000000"
	// (the raw 1M-scale form) instead of call(0.25).
	tieredCallConstPattern = regexp.MustCompile(`\b\d+(?:\.\d+)?\s*\*\s*(?:1_?000_?000|1000000|1e6)\b`)
	tieredTokenPattern    = regexp.MustCompile(`\b(?:p|c|cr|cc|cc1h|img|img_o|ai|ao)\b\s*\*`)
)

// ClassifyTieredExprQuotaType inspects a tiered billing expression and
// returns the pricing quota type shown in the model plaza:
//
//	TieredQuotaToken  - no per-call terms anywhere (pure per-token pricing)
//	TieredQuotaCall   - only per-call terms, no token variables (pure per-request)
//	TieredQuotaHybrid - both per-call and per-token terms are present
//
// Request rules after "|||" are ignored for classification.
func ClassifyTieredExprQuotaType(expr string) int {
	body := expr
	if idx := strings.Index(body, "|||"); idx >= 0 {
		body = body[:idx]
	}
	hasCall := tieredCallPattern.MatchString(body) || tieredCallConstPattern.MatchString(body)
	hasToken := tieredTokenPattern.MatchString(body)
	switch {
	case hasCall && hasToken:
		return TieredQuotaHybrid
	case hasCall:
		return TieredQuotaCall
	default:
		return TieredQuotaToken
	}
}

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	return lo.Assign(billingSetting.BillingExpr)
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 2)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}
