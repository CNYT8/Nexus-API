package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsUsageIndependentPrice(t *testing.T) {
	for _, tc := range []struct {
		expression, tier string
		want             bool
	}{
		{`tier("a", call(0))`, "a", true},
		{`v1:tier("a", call(0.01))`, "a", true},
		{`tier("a", 0.25 * 1000000)`, "a", true},
		{`call(1)+2*p`, "", false},
		{`tier("a", call(1)+2*p)`, "a", false},
		{`tier("a", call(p))`, "a", false},
		{`tier("a", call(1)+p*0)`, "a", false},
		{`len+c <= 250000 ? tier("a", call(1)) : tier("b", call(2))`, "a", true},
		{`p < 10 ? tier("a", call(0)) : tier("b", 2*p)`, "a", true},
		{`p < 10 ? tier("a", call(0)) : tier("b", 2*p)`, "b", false},
		{`p < 10 ? tier("a", call(0)) : tier("a", 2*p)`, "a", false},
		{`tier("a", call(1)) * (param("fast") == true ? 2 : 1)`, "a", true},
		{`tier("a", call(1)) * (p > 100 ? 2 : 1)`, "a", false},
		{`tier("a", p > 100 ? call(2) : call(1))`, "a", false},
		{`p > 100 ? call(2) : call(1)`, "", false},
		{`tier("a", call(1)) + tier("b", p)`, "b", false},
		{`tier("a", tier("b", p))`, "a", false},
		{`tier(string(p), call(0))`, "0", false},
		{`let n = p; tier("a", call(n))`, "a", false},
		{`tier("a", max(call(1), call(2)))`, "a", true},
		{`tier("a", call(1))`, "missing", false},
		{`not valid (`, "", false},
	} {
		t.Run(tc.expression+"/"+tc.tier, func(t *testing.T) {
			require.Equal(t, tc.want, IsUsageIndependentPrice(tc.expression, tc.tier))
		})
	}
}
