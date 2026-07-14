package billingexpr

import "github.com/QuantumNous/new-api/common"

// QuotaRound converts a float64 quota value to int using half-away-from-zero
// rounding. Every tiered billing path (pre-consume, settlement, breakdown
// validation, log fields) MUST use this function to avoid +-1 discrepancies.
//
// The result saturates at int32 bounds: quota columns are 32-bit integers in
// the database, and an oversized expression result must never wrap around
// and turn a charge into a credit.
func QuotaRound(f float64) int {
	return common.QuotaRound(f)
}

func QuotaRoundStrict(f float64) (int, error) {
	return common.QuotaRoundStrict(f)
}
