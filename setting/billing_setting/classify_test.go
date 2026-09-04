package billing_setting

import "testing"

func TestClassifyTieredExprQuotaType(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want int
	}{
		{"pure token flat", `tier("base", p * 2.5 + c * 15)`, TieredQuotaToken},
		{"pure token with cache", `tier("base", p * 3 + c * 15 + cr * 0.3 + cc * 3.75 + cc1h * 6)`, TieredQuotaToken},
		{"tiered token", `len <= 200000 ? tier("a", p * 3 + c * 15) : tier("b", p * 6 + c * 22.5)`, TieredQuotaToken},
		{"multimodal token", `tier("base", p * 0.43 + c * 3.06 + img * 0.78 + ai * 3.81 + ao * 15.11)`, TieredQuotaToken},
		{"pure call flat", `tier("base", call(5))`, TieredQuotaCall},
		{"tiered call", `len <= 128000 ? tier("short", call(3)) : tier("long", call(5))`, TieredQuotaCall},
		{"handwritten per-call const", `len + c <= 250000 ? tier("under_250k", 0.25 * 1000000) : tier("over_250k", 0.35 * 1000000)`, TieredQuotaCall},
		{"call plus tokens", `tier("base", call(3) + p * 2 + c * 6)`, TieredQuotaHybrid},
		{"tier mixing call and token tiers", `len <= 100000 ? tier("a", call(1)) : tier("b", p * 2 + c * 4)`, TieredQuotaHybrid},
		{"request rules ignored for classification", `tier("base", call(5))|||when(header("x") has "y") * 2`, TieredQuotaCall},
		{"call with token in request rule only", `tier("base", p * 2 + c * 4)|||when(param("tier") == "fast") * 6`, TieredQuotaToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyTieredExprQuotaType(tc.expr); got != tc.want {
				t.Errorf("ClassifyTieredExprQuotaType(%q) = %d, want %d", tc.expr, got, tc.want)
			}
		})
	}
}
