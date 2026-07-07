package common

import (
	"math"

	"github.com/shopspring/decimal"
)

const (
	MaxQuota = math.MaxInt32
	MinQuota = math.MinInt32
)

func saturateQuota(value float64) int {
	switch {
	case math.IsNaN(value):
		return 0
	case value >= MaxQuota:
		return MaxQuota
	case value <= MinQuota:
		return MinQuota
	default:
		return int(value)
	}
}

// QuotaFromFloat converts a computed quota value to int with saturation.
// Quota products can include user-controlled multipliers (image n, video
// seconds, resolution ratios); an oversized product must never wrap around
// and turn a charge into a credit. The bound is int32 because quota columns
// (user/token/log) are 32-bit integers in the database.
func QuotaFromFloat(value float64) int {
	return saturateQuota(value)
}

// QuotaRound converts a float64 quota value to int using half-away-from-zero
// rounding, with saturation.
func QuotaRound(value float64) int {
	return saturateQuota(math.Round(value))
}

// QuotaFromDecimal converts a computed quota decimal to int with saturation.
// The decimal is rounded before conversion.
func QuotaFromDecimal(d decimal.Decimal) int {
	f, _ := d.Round(0).Float64()
	return saturateQuota(f)
}
