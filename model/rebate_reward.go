package model

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const rebateLifetimePeriodKey = "lifetime"

// RecordWalletRebateTx creates the direct-inviter reward for one finalized
// eligible wallet settlement. Invitee and inviter caps are independent and
// excess reward is discarded at generation time. In immediate payout mode the
// non-eligible rebate lot is credited to the inviter in the same transaction;
// scheduled mode leaves a pending record for the payout task. Identical
// retries return the persisted record and never pay twice.
func RecordWalletRebateTx(tx *gorm.DB, settlement WalletConsumptionSettlement) (*RebateRecord, error) {
	if tx == nil || settlement.ID <= 0 || !isCanonicalSHA256(settlement.FinalSettlementKey) {
		return nil, errors.New("invalid rebate settlement")
	}
	active, err := walletCreditBaselineState(tx)
	if err != nil {
		return nil, err
	}
	if !active {
		// Rebates only make sense with source-attributed wallet provenance.
		// Never break settlement on a deployment that is not initialized yet.
		return nil, nil
	}

	settlementKey, err := NewRebateIdempotencyKey("rebate-record-v1", settlement.FinalSettlementKey)
	if err != nil {
		return nil, err
	}
	payoutKey, err := NewRebateIdempotencyKey("rebate-payout-v1", settlement.FinalSettlementKey)
	if err != nil {
		return nil, err
	}
	var existing RebateRecord
	err = tx.Where("settlement_key = ?", settlementKey).First(&existing).Error
	if err == nil {
		if existing.InviteeID != settlement.UserID ||
			existing.EligibleMoneyMicros != settlement.EligibleMoneyConsumedMicros ||
			existing.EligibleQuota != settlement.EligibleQuotaConsumed {
			return nil, errors.New("rebate record key reused with different immutable values")
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Idempotent retries return the original snapshot even after closure or
	// changes to rate, caps, schedule, invitation relation or quota valuation.
	setting := operation_setting.GetRebateSetting()
	if (!setting.Enabled && !setting.CountDuringClosure) || setting.RateBasisPoints <= 0 || settlement.EligibleMoneyConsumedMicros <= 0 {
		return nil, nil
	}
	if err := operation_setting.ValidateRebateSetting(setting); err != nil {
		return nil, err
	}
	var invitee User
	if err := tx.Select("id", "inviter_id").Where("id = ?", settlement.UserID).First(&invitee).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if invitee.InviterId <= 0 || invitee.InviterId == invitee.Id {
		return nil, nil
	}
	inviterID := invitee.InviterId

	if setting.InviterMinPaidMoneyMicros > 0 {
		var inviterPaidMoney int64
		if err := tx.Model(&WalletCreditLot{}).
			Where("user_id = ? AND eligible = ?", inviterID, true).
			Select("COALESCE(SUM(eligible_money_micros), 0)").Scan(&inviterPaidMoney).Error; err != nil {
			return nil, err
		}
		if inviterPaidMoney < setting.InviterMinPaidMoneyMicros {
			// Anti-farming prerequisite: a throwaway inviter account that never
			// paid must not turn invitee spending into withdrawable rewards.
			// The settlement stays auditable as a voided record.
			now := time.Now().UTC()
			record := RebateRecord{
				SettlementKey:        settlementKey,
				PayoutKey:            payoutKey,
				InviterID:            inviterID,
				InviteeID:            settlement.UserID,
				EligibleQuota:        settlement.EligibleQuotaConsumed,
				EligibleMoneyMicros:  settlement.EligibleMoneyConsumedMicros,
				RateBasisPoints:      setting.RateBasisPoints,
				Status:               RebateStatusVoided,
				CountedDuringClosure: !setting.Enabled,
				VoidedAt:             &now,
				VoidReason:           "inviter_min_paid_not_met",
				AvailableAt:          now,
				CreatedAt:            now,
				UpdatedAt:            now,
			}
			insert := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "settlement_key"}},
				DoNothing: true,
			}).Create(&record)
			if insert.Error != nil {
				return nil, insert.Error
			}
			if insert.RowsAffected != 1 {
				return nil, errors.New("rebate record created concurrently; retry whole transaction")
			}
			return &record, nil
		}
	}

	now := time.Now().UTC()
	periodKey, capType, err := rebateInviterCapIdentity(setting, now)
	if err != nil {
		return nil, err
	}

	uncappedReward, err := rebateRewardMicros(settlement.EligibleMoneyConsumedMicros, setting.RateBasisPoints)
	if err != nil {
		return nil, err
	}

	// Invitee lifetime qualifying-consumption cap.
	qualifyingMoney := settlement.EligibleMoneyConsumedMicros
	var inviteeBucket *RebateCapBucket
	if setting.InviteeEligibleMoneyLimit > 0 {
		inviteeBucket, err = rebateCapBucketTx(tx, settlement.UserID, RebateCapTypeInviteeLifetime, rebateLifetimePeriodKey, setting.InviteeEligibleMoneyLimit)
		if err != nil {
			return nil, err
		}
		remaining := int64(0)
		if inviteeBucket.LimitMicros > inviteeBucket.UsedMicros {
			remaining = inviteeBucket.LimitMicros - inviteeBucket.UsedMicros
		}
		if qualifyingMoney > remaining {
			qualifyingMoney = remaining
		}
	}
	// Keep invitee usage independent: inviter exhaustion must not restore an
	// invitee's lifetime eligibility. The new bucket is in spending units,
	// deliberately separate from the legacy inviter reward-money buckets.
	inviteeQualifyingMoney := qualifyingMoney
	var inviterEligibleBucket *RebateCapBucket
	if setting.InviterEligibleMoneyLimit > 0 {
		inviterEligibleBucket, err = rebateCapBucketTx(tx, inviterID, rebateInviterEligibleCapType, periodKey, setting.InviterEligibleMoneyLimit)
		if err != nil {
			return nil, err
		}
		qualifyingMoney = rebateCapAllowed(inviterEligibleBucket, qualifyingMoney)
	}
	cappedReward, err := rebateRewardMicros(qualifyingMoney, setting.RateBasisPoints)
	if err != nil {
		return nil, err
	}

	// Inviter reward-period cap.
	reward := cappedReward
	var inviterBucket *RebateCapBucket
	if setting.InviterRewardLimit > 0 {
		inviterBucket, err = rebateCapBucketTx(tx, inviterID, capType, periodKey, setting.InviterRewardLimit)
		if err != nil {
			return nil, err
		}
		remaining := int64(0)
		if inviterBucket.LimitMicros > inviterBucket.UsedMicros {
			remaining = inviterBucket.LimitMicros - inviterBucket.UsedMicros
		}
		if reward > remaining {
			reward = remaining
		}
	}

	// Valuation belongs to consumed lots, not today's global quota ratio.
	// Any fractional quota is held intact until an auditable carry mechanism
	// can pay it. Never misclassify rounding dust as cap-discarded money.
	rewardQuota, ratioMicros, hasDust, err := rebateQuotaFromAllocationsTx(tx, settlement, qualifyingMoney, reward, setting.RateBasisPoints)
	if err != nil {
		return nil, err
	}
	discarded := uncappedReward - reward

	record := RebateRecord{
		SettlementKey:              settlementKey,
		PayoutKey:                  payoutKey,
		InviterID:                  inviterID,
		InviteeID:                  settlement.UserID,
		EligibleQuota:              settlement.EligibleQuotaConsumed,
		EligibleMoneyMicros:        settlement.EligibleMoneyConsumedMicros,
		RateBasisPoints:            setting.RateBasisPoints,
		QuotaPerUnitMicros:         ratioMicros,
		RewardMoneyMicros:          reward,
		RewardQuota:                rewardQuota,
		DiscardedRewardMoneyMicros: discarded,
		InviteeCapLimitMicros:      setting.InviteeEligibleMoneyLimit,
		InviterCapLimitMicros:      setting.InviterRewardLimit,
		InviterCapPeriodKey:        periodKey,
		Status:                     RebateStatusPending,
		CountedDuringClosure:       !setting.Enabled,
		HeldByClosure:              !setting.Enabled,
		AvailableAt:                now,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}

	if inviteeBucket != nil {
		if inviteeQualifyingMoney > 0 {
			if _, err := rebateCapConsumeTx(tx, inviteeBucket, inviteeQualifyingMoney); err != nil {
				return nil, err
			}
		}
		record.InviteeCapUsedAfterMicros = inviteeBucket.UsedMicros
	}
	if inviterEligibleBucket != nil {
		if _, err := rebateCapConsumeTx(tx, inviterEligibleBucket, qualifyingMoney); err != nil {
			return nil, err
		}
	}
	if inviterBucket != nil {
		if reward > 0 {
			if _, err := rebateCapConsumeTx(tx, inviterBucket, reward); err != nil {
				return nil, err
			}
		}
		record.InviterCapUsedAfterMicros = inviterBucket.UsedMicros
	}

	availableAt, err := nextRebatePayoutTime(setting, now)
	if err != nil {
		return nil, err
	}
	if setting.PayoutHoldHours > 0 {
		// Chargeback risk window: a reward may not become payable before the
		// hold elapses, so operators can void fraud before any balance is paid.
		if earliest := now.Add(time.Duration(setting.PayoutHoldHours) * time.Hour); availableAt.Before(earliest) {
			availableAt = earliest
		}
	}
	record.AvailableAt = availableAt
	var inviter User
	inviterErr := tx.Select("id").Where("id = ?", inviterID).First(&inviter).Error
	if inviterErr != nil && !errors.Is(inviterErr, gorm.ErrRecordNotFound) {
		return nil, inviterErr
	}
	if errors.Is(inviterErr, gorm.ErrRecordNotFound) {
		record.Status = RebateStatusVoided
		record.VoidedAt = &now
		record.VoidReason = "inviter_deleted"
	} else if qualifyingMoney == 0 || (reward == 0 && cappedReward > 0) {
		record.Status = RebateStatusDiscarded
	} else if hasDust || reward == 0 {
		// Includes sub-micro reward: eligible amount + rate + allocations retain
		// the evidence. No worker may mark this held obligation discarded/paid.
		record.Status = RebateStatusHeld
		record.HeldByClosure = false // precision hold must survive reopening
	} else if !setting.Enabled {
		record.Status = RebateStatusHeld
	} else if setting.PayoutMode == operation_setting.RebatePayoutImmediate && setting.PayoutHoldHours == 0 {
		lotKey, err := NewWalletCreditEventKey(inviterID, WalletCreditSourceRebate, "rebate", settlement.FinalSettlementKey, "payout")
		if err != nil {
			return nil, err
		}
		lot := WalletCreditLot{
			CreditEventKey: lotKey,
			UserID:         inviterID,
			Source:         WalletCreditSourceRebate,
			Component:      "payout",
			Eligible:       false,
			ReferenceType:  "rebate",
			ReferenceID:    settlement.FinalSettlementKey,
			QuotaTotal:     rewardQuota,
			QuotaRemaining: rewardQuota,
		}
		applied, err := AddWalletCreditLotTx(tx, lot)
		if err != nil {
			return nil, err
		}
		if !applied {
			return nil, errors.New("rebate payout lot already exists for this settlement")
		}
		var payoutLot WalletCreditLot
		if err := tx.Where("credit_event_key = ?", lotKey).First(&payoutLot).Error; err != nil {
			return nil, err
		}
		record.PayoutLotID = &payoutLot.ID
		record.Status = RebateStatusPaid
		record.PaidAt = &now
	}

	insert := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "settlement_key"}},
		DoNothing: true,
	}).Create(&record)
	if insert.Error != nil {
		return nil, insert.Error
	}
	if insert.RowsAffected == 0 {
		var stored RebateRecord
		if err := lockForUpdate(tx).Where("settlement_key = ?", settlementKey).First(&stored).Error; err != nil {
			return nil, err
		}
		if stored.InviterID != inviterID || stored.InviteeID != settlement.UserID ||
			stored.EligibleMoneyMicros != settlement.EligibleMoneyConsumedMicros ||
			stored.RewardMoneyMicros != record.RewardMoneyMicros || stored.RewardQuota != record.RewardQuota {
			return nil, errors.New("rebate record key reused with different immutable values")
		}
		// Cap updates and immediate credits already occurred in this attempt.
		// Force whole-transaction rollback; returning success would consume a
		// cap twice when another transaction won the unique-key insertion.
		return nil, errors.New("rebate record created concurrently; retry whole transaction")
	}
	if insert.RowsAffected != 1 {
		return nil, errors.New("unexpected rebate record insert row count")
	}
	return &record, nil
}

func rebateRewardMicros(eligibleMoneyMicros, rateBasisPoints int64) (int64, error) {
	if eligibleMoneyMicros < 0 || rateBasisPoints < 0 || rateBasisPoints > 10_000 {
		return 0, errors.New("invalid rebate reward input")
	}
	value := decimal.NewFromInt(eligibleMoneyMicros).
		Mul(decimal.NewFromInt(rateBasisPoints)).
		Div(decimal.NewFromInt(10_000))
	if value.IsNegative() || value.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, ErrTopUpQuotaOutOfRange
	}
	return value.IntPart(), nil
}

func rebateQuotaForMoney(moneyMicros, quotaPerUnitMicros int64) (int64, error) {
	if moneyMicros < 0 || quotaPerUnitMicros <= 0 {
		return 0, errors.New("invalid rebate quota conversion input")
	}
	value := decimal.NewFromInt(moneyMicros).
		Mul(decimal.NewFromInt(quotaPerUnitMicros)).
		Div(decimal.NewFromInt(MoneyMicrosPerUnit * MoneyMicrosPerUnit))
	if value.IsNegative() || value.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, ErrTopUpQuotaOutOfRange
	}
	return value.IntPart(), nil
}

const rebateInviterEligibleCapType = "inviter_eligible"

func rebateCapAllowed(bucket *RebateCapBucket, requested int64) int64 {
	if bucket.LimitMicros == 0 {
		return requested
	}
	if bucket.UsedMicros >= bucket.LimitMicros {
		return 0
	}
	if remaining := bucket.LimitMicros - bucket.UsedMicros; requested > remaining {
		return remaining
	}
	return requested
}

// rebateQuotaFromAllocationsTx allocates the admitted base in original FIFO
// order. Each admitted part uses its source-lot ratio; fractions are carried
// at 1/10000 money-micro precision before quota conversion, not shifted into
// another lot by rounding. A mixed-ratio record has ratioSnapshot=0; its
// allocations remain valuation evidence, never an average or the current ratio.
func rebateQuotaFromAllocationsTx(tx *gorm.DB, settlement WalletConsumptionSettlement, base, reward, rate int64) (quota, ratioSnapshot int64, hasDust bool, err error) {
	if base < 0 || base > settlement.EligibleMoneyConsumedMicros || reward < 0 || rate < 0 || rate > 10_000 {
		return 0, 0, false, errors.New("invalid rebate allocation valuation")
	}
	var parts []struct {
		Money    int64
		Ratio    int64
		Owner    int
		Eligible bool
	}
	err = tx.Table("wallet_consumption_allocations AS a").
		Select("a.eligible_money_consumed_micros AS money, COALESCE(l.quota_per_unit_micros, 0) AS ratio, COALESCE(l.user_id, 0) AS owner, COALESCE(l.eligible, false) AS eligible").
		Joins("LEFT JOIN wallet_credit_lots l ON l.id = a.lot_id").
		Where("a.final_settlement_key = ? AND a.user_id = ? AND a.eligible_money_consumed_micros > 0", settlement.FinalSettlementKey, settlement.UserID).
		Order("a.id ASC").Scan(&parts).Error
	if err != nil {
		return 0, 0, false, err
	}
	var totalMoney, usedBase int64
	// Reward cap is in whole money micros; untruncated rewards keep the
	// fractional money claim as well. Both integer divisions happen at end.
	exactMoney := new(big.Int).Mul(big.NewInt(base), big.NewInt(rate))
	floorMoney := new(big.Int).Quo(new(big.Int).Set(exactMoney), big.NewInt(10_000))
	if !floorMoney.IsInt64() || reward > floorMoney.Int64() {
		return 0, 0, false, errors.New("reward exceeds eligible base")
	}
	remainingReward := new(big.Int).Set(exactMoney)
	if reward < floorMoney.Int64() {
		remainingReward.Mul(big.NewInt(reward), big.NewInt(10_000))
	}
	moneyDust := new(big.Int).Mod(new(big.Int).Set(remainingReward), big.NewInt(10_000)).Sign() != 0
	numerator := new(big.Int)
	mixed := false
	for i, part := range parts {
		if part.Ratio <= 0 || part.Owner != settlement.UserID || !part.Eligible || part.Money > math.MaxInt64-totalMoney {
			return 0, 0, false, errors.New("invalid source-lot rebate snapshot")
		}
		totalMoney += part.Money
		if i == 0 {
			ratioSnapshot = part.Ratio
		} else if ratioSnapshot != part.Ratio {
			mixed = true
		}
		admitted := min(part.Money, base-usedBase)
		usedBase += admitted
		partReward := new(big.Int).Mul(big.NewInt(admitted), big.NewInt(rate))
		if partReward.Cmp(remainingReward) > 0 {
			partReward.Set(remainingReward)
		}
		remainingReward.Sub(remainingReward, partReward)
		numerator.Add(numerator, new(big.Int).Mul(partReward, big.NewInt(part.Ratio)))
	}
	if totalMoney != settlement.EligibleMoneyConsumedMicros || remainingReward.Sign() != 0 {
		return 0, 0, false, errors.New("rebate allocations do not match settlement valuation")
	}
	if mixed {
		ratioSnapshot = 0
	}
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(numerator, big.NewInt(10_000*MoneyMicrosPerUnit*MoneyMicrosPerUnit), remainder)
	if !whole.IsInt64() {
		return 0, 0, false, ErrTopUpQuotaOutOfRange
	}
	// Preserve sub-micro reward evidence as well as sub-quota dust. Until a
	// durable fractional-money schema exists, neither may silently disappear.
	return whole.Int64(), ratioSnapshot, remainder.Sign() != 0 || moneyDust, nil
}

func rebateCapBucketTx(tx *gorm.DB, ownerID int, capType, periodKey string, limitMicros int64) (*RebateCapBucket, error) {
	if ownerID <= 0 || limitMicros < 0 {
		return nil, errors.New("invalid rebate cap bucket input")
	}
	now := time.Now().UTC()
	seed := RebateCapBucket{
		OwnerID:     ownerID,
		CapType:     capType,
		PeriodKey:   periodKey,
		LimitMicros: limitMicros,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_id"}, {Name: "cap_type"}, {Name: "period_key"}},
		DoNothing: true,
	}).Create(&seed).Error; err != nil {
		return nil, err
	}

	var stored RebateCapBucket
	if err := lockForUpdate(tx).
		Where("owner_id = ? AND cap_type = ? AND period_key = ?", ownerID, capType, periodKey).
		First(&stored).Error; err != nil {
		return nil, err
	}
	if stored.LimitMicros != limitMicros {
		result := tx.Model(&RebateCapBucket{}).
			Where("id = ? AND limit_micros = ?", stored.ID, stored.LimitMicros).
			Updates(map[string]interface{}{
				"limit_micros": limitMicros,
				"updated_at":   now,
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, errors.New("rebate cap bucket changed concurrently")
		}
		stored.LimitMicros = limitMicros
	}
	return &stored, nil
}

func rebateCapConsumeTx(tx *gorm.DB, bucket *RebateCapBucket, amountMicros int64) (*RebateCapBucket, error) {
	if tx == nil || bucket == nil || bucket.ID <= 0 || bucket.UsedMicros < 0 || bucket.LimitMicros < 0 || amountMicros < 0 {
		return nil, errors.New("invalid rebate cap consumption")
	}
	if amountMicros > math.MaxInt64-bucket.UsedMicros || (bucket.LimitMicros > 0 && amountMicros > rebateCapAllowed(bucket, amountMicros)) {
		return nil, errors.New("rebate cap consumption exceeds limit or integer range")
	}
	if amountMicros == 0 {
		return bucket, nil
	}
	result := tx.Model(&RebateCapBucket{}).
		Where("id = ? AND used_micros = ?", bucket.ID, bucket.UsedMicros).
		Updates(map[string]interface{}{
			"used_micros": gorm.Expr("used_micros + ?", amountMicros),
			"updated_at":  time.Now().UTC(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("rebate cap bucket changed concurrently")
	}
	bucket.UsedMicros += amountMicros
	return bucket, nil
}

func rebateInviterCapIdentity(setting operation_setting.RebateSetting, now time.Time) (string, string, error) {
	location, err := time.LoadLocation(setting.SiteTimezone)
	if err != nil {
		return "", "", fmt.Errorf("invalid rebate site timezone: %w", err)
	}
	local := now.In(location)
	switch setting.InviterLimitPeriod {
	case operation_setting.RebateLimitPeriodDaily:
		return local.Format("2006-01-02"), RebateCapTypeInviterDaily, nil
	case operation_setting.RebateLimitPeriodMonthly:
		return local.Format("2006-01"), RebateCapTypeInviterMonthly, nil
	case operation_setting.RebateLimitPeriodPermanent:
		return rebateLifetimePeriodKey, RebateCapTypeInviterLifetime, nil
	default:
		return "", "", errors.New("invalid rebate inviter limit period")
	}
}

// nextRebatePayoutTime is evaluated once at generation. Persisted AvailableAt
// is the schedule snapshot: edits to mode, interval, clock time or timezone
// never reschedule or revalue pending records. Boundaries are strictly after now.
func nextRebatePayoutTime(setting operation_setting.RebateSetting, now time.Time) (time.Time, error) {
	if now.IsZero() || now.Year() < 1 || now.Year() > 9998 {
		return time.Time{}, errors.New("invalid rebate payout timestamp")
	}
	if err := operation_setting.ValidateRebateSetting(setting); err != nil {
		return time.Time{}, err
	}
	if setting.PayoutMode == operation_setting.RebatePayoutImmediate {
		return now.UTC(), nil
	}
	location, err := time.LoadLocation(setting.SiteTimezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid rebate site timezone: %w", err)
	}
	if setting.PayoutMode == operation_setting.RebatePayoutCustom {
		// A fixed site-local epoch, not worker startup or record creation, aligns
		// all custom cycles. Elapsed-hour periods deliberately do not drift at DST.
		anchor := time.Date(1970, 1, 1, setting.PayoutHour, setting.PayoutMinute, 0, 0, location).Unix()
		period := int64(setting.PayoutIntervalHours) * 3600
		delta := now.Unix() - anchor
		cycles := delta / period
		if delta < 0 && delta%period != 0 {
			cycles-- // floor division also aligns dates before the epoch
		}
		return time.Unix(anchor+(cycles+1)*period, 0).UTC(), nil
	}

	// Daily compatibility uses the site's calendar, not an elapsed 24h timer.
	// Half-day has two wall-clock slots, payout_hour and (payout_hour+12)%24.
	// time.Date defines DST gap/fold normalization (one occurrence per slot).
	local := now.In(location)
	hours := []int{setting.PayoutHour}
	if setting.PayoutMode == operation_setting.RebatePayoutHalfDay {
		hours = append(hours, (setting.PayoutHour+12)%24)
	}
	var next time.Time
	for day := 0; day <= 2; day++ {
		for _, hour := range hours {
			candidate := time.Date(local.Year(), local.Month(), local.Day()+day, hour, setting.PayoutMinute, 0, 0, location)
			if candidate.After(now) && (next.IsZero() || candidate.Before(next)) {
				next = candidate
			}
		}
	}
	if next.IsZero() {
		return time.Time{}, errors.New("no next rebate payout slot")
	}
	return next.UTC(), nil
}
