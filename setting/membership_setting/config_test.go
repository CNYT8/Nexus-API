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

func TestHiddenTierNeverAutoGranted(t *testing.T) {
	if err := UpdateTiersByJSONString(`[
		{"id":"normal","name":"Normal","enabled":true,"auto_upgrade_enabled":true,"threshold_amount":100},
		{"id":"secret","name":"Secret","enabled":true,"auto_upgrade_enabled":true,"hidden":true,"threshold_amount":1000}
	]`); err != nil {
		t.Fatal(err)
	}

	tier, ok := ResolveAutoTierByAmount(5000)
	assert.True(t, ok, "a non-hidden auto tier should match")
	assert.Equal(t, "normal", tier.Id, "hidden tier must never be auto-granted even with higher threshold")
}

func TestHiddenTierExcludedFromNextTier(t *testing.T) {
	if err := UpdateTiersByJSONString(`[
		{"id":"normal","name":"Normal","enabled":true,"auto_upgrade_enabled":true,"threshold_amount":100},
		{"id":"secret","name":"Secret","enabled":true,"hidden":true,"threshold_amount":1000},
		{"id":"gold","name":"Gold","enabled":true,"auto_upgrade_enabled":true,"threshold_amount":2000}
	]`); err != nil {
		t.Fatal(err)
	}

	next, ok := NextTierByAmount(150)
	assert.True(t, ok)
	assert.Equal(t, "gold", next.Id, "next tier should skip hidden tiers")
}

func TestHiddenTierDiscountStillApplies(t *testing.T) {
	if err := UpdateTiersByJSONString(`[
		{"id":"secret","name":"Secret","enabled":true,"hidden":true,"discount_all_groups":true,"all_group_discount":0.5}
	]`); err != nil {
		t.Fatal(err)
	}

	discount, ok := GetTierDiscount("secret", "default")
	assert.True(t, ok, "hidden tier must keep applying its discount to members")
	assert.Equal(t, 0.5, discount.Multiplier)
}
