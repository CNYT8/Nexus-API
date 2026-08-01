package membership_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeTiersSupportsMembershipMultiplierUpToLimit(t *testing.T) {
	tiers := NormalizeTiers([]Tier{{
		Id:               "pro",
		Name:             "Pro",
		AllGroupDiscount: 1.5,
		GroupDiscounts:   []GroupDiscount{{Group: "vip", Discount: 1000}},
	}})

	if assert.Len(t, tiers, 1) {
		assert.Equal(t, 1.5, tiers[0].AllGroupDiscount)
		if assert.Len(t, tiers[0].GroupDiscounts, 1) {
			assert.Equal(t, float64(1000), tiers[0].GroupDiscounts[0].Discount)
		}
	}
}

func TestNormalizeTiersFallsBackForUnsafeMembershipMultiplier(t *testing.T) {
	for _, multiplier := range []float64{0, -1, 1000.0001, math.NaN(), math.Inf(1)} {
		t.Run("unsafe", func(t *testing.T) {
			tiers := NormalizeTiers([]Tier{{
				Id:               "pro",
				Name:             "Pro",
				AllGroupDiscount: multiplier,
			}})

			if assert.Len(t, tiers, 1) {
				assert.Equal(t, float64(1), tiers[0].AllGroupDiscount)
			}
		})
	}
}
