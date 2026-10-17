package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// WalletRebateSummary is the authorized self view of invitation rebates. It
// only aggregates records owned by the requesting user.
type WalletRebateSummary struct {
	Enabled                          bool   `json:"enabled"`
	RateBasisPoints                  int64  `json:"rate_basis_points"`
	PayoutMode                       string `json:"payout_mode"`
	PayoutIntervalHours              int    `json:"payout_interval_hours"`
	PayoutHour                       int    `json:"payout_hour"`
	PayoutMinute                     int    `json:"payout_minute"`
	SiteTimezone                     string `json:"site_timezone"`
	CountDuringClosure               bool   `json:"count_during_closure"`
	PendingMoneyMicros               int64  `json:"pending_money_micros"`
	InviterEligibleMoneyLimitMicros  int64  `json:"inviter_eligible_money_limit_micros"`
	InviterPeriodEligibleMoneyMicros int64  `json:"inviter_period_eligible_money_micros"`
	PendingQuota                     int64  `json:"pending_quota"`
	PaidQuota                        int64  `json:"paid_quota"`
	PaidMoneyMicros                  int64  `json:"paid_money_micros"`
	DiscardedMoneyMicros             int64  `json:"discarded_money_micros"`
	RewardCount                      int64  `json:"reward_count"`
	InviteeEligibleMoneyMicros       int64  `json:"invitee_eligible_money_micros"`
	InviteeEligibleMoneyLimitMicros  int64  `json:"invitee_eligible_money_limit_micros"`
	InviterRewardLimitMicros         int64  `json:"inviter_reward_limit_micros"`
	InviterLimitPeriod               string `json:"inviter_limit_period"`
	InviterPeriodKey                 string `json:"inviter_period_key"`
	InviterPeriodUsedMicros          int64  `json:"inviter_period_used_micros"`
}

// GetWalletRebateSummary aggregates the user's own rebate records plus the cap
// buckets that apply to the user as invitee and as inviter.
func GetWalletRebateSummary(db *gorm.DB, userID int) (*WalletRebateSummary, error) {
	if db == nil || userID <= 0 {
		return nil, errors.New("invalid rebate summary query")
	}
	setting := operation_setting.GetRebateSetting()
	summary := &WalletRebateSummary{
		Enabled:                         setting.Enabled,
		RateBasisPoints:                 setting.RateBasisPoints,
		PayoutMode:                      setting.PayoutMode,
		PayoutIntervalHours:             setting.PayoutIntervalHours,
		PayoutHour:                      setting.PayoutHour,
		PayoutMinute:                    setting.PayoutMinute,
		SiteTimezone:                    setting.SiteTimezone,
		CountDuringClosure:              setting.CountDuringClosure,
		InviterEligibleMoneyLimitMicros: setting.InviterEligibleMoneyLimit,
		InviteeEligibleMoneyLimitMicros: setting.InviteeEligibleMoneyLimit,
		InviterRewardLimitMicros:        setting.InviterRewardLimit,
		InviterLimitPeriod:              setting.InviterLimitPeriod,
	}

	var aggregate struct {
		PendingQuota         int64
		PendingMoneyMicros   int64
		PaidQuota            int64
		PaidMoneyMicros      int64
		DiscardedMoneyMicros int64
		RewardCount          int64
	}
	err := db.Model(&RebateRecord{}).
		Select(`COALESCE(SUM(CASE WHEN status IN (?, ?) THEN reward_quota ELSE 0 END), 0) AS pending_quota,
			COALESCE(SUM(CASE WHEN status IN (?, ?) THEN reward_money_micros ELSE 0 END), 0) AS pending_money_micros,
			COALESCE(SUM(CASE WHEN status = ? THEN reward_quota ELSE 0 END), 0) AS paid_quota,
			COALESCE(SUM(CASE WHEN status = ? THEN reward_money_micros ELSE 0 END), 0) AS paid_money_micros,
			COALESCE(SUM(discarded_reward_money_micros), 0) AS discarded_money_micros,
			COUNT(*) AS reward_count`,
			RebateStatusPending, RebateStatusHeld, RebateStatusPending, RebateStatusHeld, RebateStatusPaid, RebateStatusPaid).
		Where("inviter_id = ?", userID).
		Scan(&aggregate).Error
	if err != nil {
		return nil, err
	}
	summary.PendingMoneyMicros = aggregate.PendingMoneyMicros
	summary.PendingQuota = aggregate.PendingQuota
	summary.PaidQuota = aggregate.PaidQuota
	summary.PaidMoneyMicros = aggregate.PaidMoneyMicros
	summary.DiscardedMoneyMicros = aggregate.DiscardedMoneyMicros
	summary.RewardCount = aggregate.RewardCount

	var inviteeBucket RebateCapBucket
	err = db.Where("owner_id = ? AND cap_type = ? AND period_key = ?", userID, RebateCapTypeInviteeLifetime, rebateLifetimePeriodKey).
		First(&inviteeBucket).Error
	if err == nil {
		summary.InviteeEligibleMoneyMicros = inviteeBucket.UsedMicros
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	periodKey, capType, err := rebateInviterCapIdentity(setting, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	summary.InviterPeriodKey = periodKey
	var inviterBucket RebateCapBucket
	err = db.Where("owner_id = ? AND cap_type = ? AND period_key = ?", userID, capType, periodKey).
		First(&inviterBucket).Error
	if err == nil {
		summary.InviterPeriodUsedMicros = inviterBucket.UsedMicros
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var eligibleBucket RebateCapBucket
	err = db.Where("owner_id = ? AND cap_type = ? AND period_key = ?", userID, rebateInviterEligibleCapType, periodKey).First(&eligibleBucket).Error
	if err == nil {
		summary.InviterPeriodEligibleMoneyMicros = eligibleBucket.UsedMicros
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return summary, nil
}

// WalletRebateRecordView is the authorized self view of one rebate record.
type WalletRebateRecordView struct {
	ID                   int64      `json:"id"`
	InviteeID            int        `json:"invitee_id"`
	InviteeUsername      string     `json:"invitee_username"`
	EligibleMoneyMicros  int64      `json:"eligible_money_micros"`
	RewardMoneyMicros    int64      `json:"reward_money_micros"`
	RewardQuota          int64      `json:"reward_quota"`
	Status               string     `json:"status"`
	CountedDuringClosure bool       `json:"counted_during_closure"`
	HeldByClosure        bool       `json:"held_by_closure"`
	VoidedAt             *time.Time `json:"voided_at"`
	VoidReason           string     `json:"void_reason"`
	CreatedAt            time.Time  `json:"created_at"`
	AvailableAt          time.Time  `json:"available_at"`
	PaidAt               *time.Time `json:"paid_at"`
}

// maxWalletRebateRecordOffset bounds pagination so a client cannot force an
// unbounded OFFSET scan on a large rebate table.
const maxWalletRebateRecordOffset = 100_000

// GetWalletRebateRecords returns the requesting user's own rebate records,
// newest first, with the invitee display name only.
func GetWalletRebateRecords(db *gorm.DB, userID, offset, limit int) ([]WalletRebateRecordView, int64, error) {
	if db == nil || userID <= 0 || offset < 0 || limit <= 0 {
		return nil, 0, errors.New("invalid rebate record query")
	}
	limit = min(limit, 100)
	if offset > maxWalletRebateRecordOffset {
		offset = maxWalletRebateRecordOffset
	}
	var total int64
	if err := db.Model(&RebateRecord{}).Where("inviter_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	records := make([]WalletRebateRecordView, 0)
	if total <= int64(offset) {
		return records, total, nil
	}
	err := db.Table("rebate_records AS r").
		Select("r.id, r.invitee_id, COALESCE(u.username, '') AS invitee_username, r.eligible_money_micros, r.reward_money_micros, r.reward_quota, r.status, r.counted_during_closure, r.held_by_closure, r.voided_at, r.void_reason, r.created_at, r.available_at, r.paid_at").
		Joins("LEFT JOIN users u ON u.id = r.invitee_id").
		Where("r.inviter_id = ?", userID).
		Order("r.id DESC").
		Offset(offset).
		Limit(limit).
		Scan(&records).Error
	if err != nil {
		return nil, 0, err
	}
	return records, total, nil
}
