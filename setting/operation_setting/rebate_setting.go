package operation_setting

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

// RebateSetting stores fixed-point values to avoid float rounding in financial
// rules. RateBasisPoints=100 means 1%; money caps use 1e-6 site-money units.
type RebateSetting struct {
	Enabled            bool   `json:"enabled"`
	RateBasisPoints    int64  `json:"rate_basis_points"`
	CountDuringClosure bool   `json:"count_during_closure"`
	PayoutMode         string `json:"payout_mode"`
	// Custom intervals use elapsed hours from the site-local 1970-01-01
	// payout_hour:payout_minute anchor. Scheduled remains local daily payout;
	// half_day uses two local daily slots twelve wall-clock hours apart.
	// Only newly generated records use changes; pending AvailableAt is frozen.
	PayoutIntervalHours       int   `json:"payout_interval_hours"`
	PayoutHour                int   `json:"payout_hour"`
	PayoutMinute              int   `json:"payout_minute"`
	InviteeEligibleMoneyLimit int64 `json:"invitee_eligible_money_limit_micros"`
	// InviteeEligibleMoneyLimit is lifetime per invitee. The inviter's base
	// limit aggregates eligible spending across invitees for InviterLimitPeriod.
	InviterEligibleMoneyLimit int64 `json:"inviter_eligible_money_limit_micros"`
	// Keep this legacy limit in actual reward-money units, not spending units.
	InviterRewardLimit int64  `json:"inviter_reward_limit_micros"`
	InviterLimitPeriod string `json:"inviter_limit_period"`
	SiteTimezone       string `json:"site_timezone"`
	// InviterMinPaidMoneyMicros is an anti-farming prerequisite: the inviter
	// must have received at least this much eligible paid top-up money before
	// any reward is recorded. 0 disables the prerequisite.
	InviterMinPaidMoneyMicros int64 `json:"inviter_min_paid_money_micros"`
	// PayoutHoldHours keeps every newly recorded reward pending for at least
	// this many hours before it can be paid, giving operators a chargeback
	// detection window. 0 disables the hold and keeps immediate payouts
	// immediate.
	PayoutHoldHours int `json:"payout_hold_hours"`
}

const (
	RebatePayoutImmediate = "immediate"
	RebatePayoutScheduled = "scheduled"
	RebatePayoutHalfDay   = "half_day"
	RebatePayoutCustom    = "custom"

	RebateLimitPeriodDaily     = "daily"
	RebateLimitPeriodMonthly   = "monthly"
	RebateLimitPeriodPermanent = "permanent"
)

var (
	rebateSettingMu sync.RWMutex
	rebateSetting   = RebateSetting{
		PayoutMode:          RebatePayoutImmediate,
		PayoutIntervalHours: 24,
		PayoutHour:          7,
		PayoutMinute:        0,
		InviterLimitPeriod:  RebateLimitPeriodMonthly,
		SiteTimezone:        "UTC",
	}
)

// GetRebateSetting returns a value snapshot; callers cannot mutate global
// configuration or observe a partially updated setting.
func GetRebateSetting() RebateSetting {
	rebateSettingMu.RLock()
	defer rebateSettingMu.RUnlock()
	return rebateSetting
}

// UpdateRebateSetting validates then atomically replaces the active snapshot.
func UpdateRebateSetting(value RebateSetting) error {
	if err := ValidateRebateSetting(value); err != nil {
		return err
	}
	rebateSettingMu.Lock()
	if value.PayoutIntervalHours == 0 {
		// Older settings and non-custom struct literals have no interval field.
		value.PayoutIntervalHours = 24
	}
	rebateSetting = value
	rebateSettingMu.Unlock()
	return nil
}

// ValidateRebateSettingField validates a single option against the current
// snapshot without changing active settings or racing with concurrent updates.
func ValidateRebateSettingField(field, value string) error {
	return ValidateRebateSettingFields(map[string]string{field: value})
}

// ValidateRebateSettingFields validates an option batch against one settings
// snapshot. All fields are applied before checking cross-field invariants.
func ValidateRebateSettingFields(fields map[string]string) error {
	rebateSettingMu.RLock()
	defer rebateSettingMu.RUnlock()
	_, err := applyRebateSettingFields(rebateSetting, fields)
	return err
}

// UpdateRebateSettingField serializes field-level updates so simultaneous
// administrator option changes cannot overwrite unrelated fields.
func UpdateRebateSettingField(field, value string) error {
	return UpdateRebateSettingFields(map[string]string{field: value})
}

// UpdateRebateSettingFields applies a complete persisted option batch as one
// snapshot, avoiding transient partial settings and lost concurrent updates.
func UpdateRebateSettingFields(fields map[string]string) error {
	rebateSettingMu.Lock()
	defer rebateSettingMu.Unlock()

	updated, err := applyRebateSettingFields(rebateSetting, fields)
	if err != nil {
		return err
	}
	rebateSetting = updated
	return nil
}

func applyRebateSettingFields(current RebateSetting, fields map[string]string) (RebateSetting, error) {
	for field, value := range fields {
		var err error
		switch field {
		case "enabled":
			current.Enabled, err = strconv.ParseBool(value)
		case "rate_basis_points":
			current.RateBasisPoints, err = strconv.ParseInt(value, 10, 64)
		case "count_during_closure":
			current.CountDuringClosure, err = strconv.ParseBool(value)
		case "payout_mode":
			current.PayoutMode = value
		case "payout_interval_hours":
			var parsed int64
			parsed, err = strconv.ParseInt(value, 10, 32)
			current.PayoutIntervalHours = int(parsed)
			if err == nil && (parsed < 1 || parsed > 720) {
				return RebateSetting{}, errors.New("rebate payout interval must be between 1 and 720 hours")
			}
		case "payout_hour":
			var parsed int64
			parsed, err = strconv.ParseInt(value, 10, 32)
			current.PayoutHour = int(parsed)
		case "payout_minute":
			var parsed int64
			parsed, err = strconv.ParseInt(value, 10, 32)
			current.PayoutMinute = int(parsed)
		case "invitee_eligible_money_limit_micros":
			current.InviteeEligibleMoneyLimit, err = strconv.ParseInt(value, 10, 64)
		case "inviter_eligible_money_limit_micros":
			current.InviterEligibleMoneyLimit, err = strconv.ParseInt(value, 10, 64)
		case "inviter_reward_limit_micros":
			current.InviterRewardLimit, err = strconv.ParseInt(value, 10, 64)
		case "inviter_min_paid_money_micros":
			current.InviterMinPaidMoneyMicros, err = strconv.ParseInt(value, 10, 64)
		case "payout_hold_hours":
			var parsed int64
			parsed, err = strconv.ParseInt(value, 10, 32)
			current.PayoutHoldHours = int(parsed)
			if err == nil && (parsed < 0 || parsed > 720) {
				return RebateSetting{}, errors.New("rebate payout hold must be between 0 and 720 hours")
			}
		case "inviter_limit_period":
			current.InviterLimitPeriod = value
		case "site_timezone":
			current.SiteTimezone = value
		default:
			return RebateSetting{}, errors.New("unknown rebate setting field")
		}
		if err != nil {
			return RebateSetting{}, errors.New("invalid rebate setting value")
		}
	}
	if err := ValidateRebateSetting(current); err != nil {
		return RebateSetting{}, err
	}
	return current, nil
}

func ValidateRebateSetting(value RebateSetting) error {
	if value.RateBasisPoints < 0 || value.RateBasisPoints > 10_000 {
		return errors.New("rebate rate must be between 0 and 10000 basis points")
	}
	switch value.PayoutMode {
	case RebatePayoutImmediate, RebatePayoutScheduled, RebatePayoutHalfDay, RebatePayoutCustom:
	default:
		return errors.New("invalid rebate payout mode")
	}
	if value.PayoutIntervalHours < 0 || value.PayoutIntervalHours > 720 ||
		(value.PayoutMode == RebatePayoutCustom && value.PayoutIntervalHours == 0) {
		return errors.New("rebate payout interval must be between 1 and 720 hours")
	}
	if value.PayoutHour < 0 || value.PayoutHour > 23 || value.PayoutMinute < 0 || value.PayoutMinute > 59 {
		return errors.New("invalid rebate payout time")
	}
	if value.InviteeEligibleMoneyLimit < 0 || value.InviterEligibleMoneyLimit < 0 || value.InviterRewardLimit < 0 {
		return errors.New("rebate limits must be non-negative fixed-point values")
	}
	if value.InviterMinPaidMoneyMicros < 0 {
		return errors.New("rebate inviter paid prerequisite must be non-negative")
	}
	if value.PayoutHoldHours < 0 || value.PayoutHoldHours > 720 {
		return errors.New("rebate payout hold must be between 0 and 720 hours")
	}
	switch value.InviterLimitPeriod {
	case RebateLimitPeriodDaily, RebateLimitPeriodMonthly, RebateLimitPeriodPermanent:
	default:
		return errors.New("invalid inviter rebate limit period")
	}
	if value.SiteTimezone != "" {
		if _, err := time.LoadLocation(value.SiteTimezone); err != nil {
			return errors.New("invalid rebate site timezone")
		}
	}
	return nil
}
