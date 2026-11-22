package helper

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

func TestSearchPriceData(t *testing.T) {
	for _, tc := range []struct {
		name         string
		price, ratio float64
		free, bad    bool
	}{
		{name: "normal call", price: 0.01, ratio: 1},
		{name: "member discount", price: 0.01, ratio: 0.5},
		{name: "explicit free", price: 0, ratio: 1, free: true},
		{name: "free group", price: 0.01, ratio: 0, free: true},
		{name: "minimum positive", price: 1e-15, ratio: 1},
		{name: "negative price", price: -1, ratio: 1, bad: true},
		{name: "negative group", price: 1, ratio: -1, bad: true},
		{name: "nan price", price: math.NaN(), ratio: 1, bad: true},
		{name: "nan group", price: 1, ratio: math.NaN(), bad: true},
		{name: "infinite price", price: math.Inf(1), ratio: 1, bad: true},
		{name: "infinite group", price: 1, ratio: math.Inf(1), bad: true},
		{name: "quota overflow", price: math.MaxFloat64, ratio: 2, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := types.GroupRatioInfo{GroupRatio: tc.ratio, GroupSpecialRatio: 0.75}
			data, err := searchPriceData(tc.price, group)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v, bad=%v", err, tc.bad)
			}
			if tc.bad {
				return
			}
			want, err := common.QuotaRoundStrict(tc.price * tc.ratio * common.QuotaPerUnit)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.free && want == 0 {
				want = 1
			}
			if !data.UsePrice || data.FreeModel != tc.free || data.Quota != want || data.QuotaToPreConsume != want || data.ModelPrice != tc.price || data.GroupRatioInfo != group {
				t.Fatalf("unexpected price data: %+v", data)
			}
		})
	}
}
