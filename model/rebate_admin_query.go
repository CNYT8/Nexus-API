package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AdminRebateRecordFilter narrows the fraud-response listing. Zero values mean
// "no filter"; status must be one of the known rebate states or empty.
type AdminRebateRecordFilter struct {
	InviterID int
	Status    string
}

// AdminWalletRebateView is the operator-facing projection used to locate
// records for void/clawback. It intentionally exposes only audit fields.
type AdminWalletRebateView struct {
	ID                  int64      `json:"id"`
	SettlementKey       string     `json:"settlement_key"`
	InviterID           int        `json:"inviter_id"`
	InviterUsername     string     `json:"inviter_username"`
	InviteeID           int        `json:"invitee_id"`
	InviteeUsername     string     `json:"invitee_username"`
	EligibleMoneyMicros int64      `json:"eligible_money_micros"`
	RewardMoneyMicros   int64      `json:"reward_money_micros"`
	RewardQuota         int64      `json:"reward_quota"`
	Status              string     `json:"status"`
	HeldByClosure       bool       `json:"held_by_closure"`
	VoidedBy            int        `json:"voided_by"`
	VoidReason          string     `json:"void_reason"`
	CreatedAt           time.Time  `json:"created_at"`
	AvailableAt         time.Time  `json:"available_at"`
	PaidAt              *time.Time `json:"paid_at"`
}

const maxAdminRebatePageSize = 100

func isKnownRebateStatus(status string) bool {
	switch status {
	case RebateStatusPending, RebateStatusHeld, RebateStatusPaid, RebateStatusVoided, RebateStatusDiscarded, RebateStatusFailed:
		return true
	default:
		return false
	}
}

// AdminListWalletRebates lists rebate records for operators with bounded
// pagination and a validated status filter. It never returns wallet or user
// secrets and is scoped by an explicit inviter id when supplied.
func AdminListWalletRebates(db *gorm.DB, filter AdminRebateRecordFilter, offset, limit int) ([]AdminWalletRebateView, int64, error) {
	if db == nil || offset < 0 || limit <= 0 {
		return nil, 0, errors.New("invalid admin rebate query")
	}
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.Status != "" && !isKnownRebateStatus(filter.Status) {
		return nil, 0, errors.New("invalid admin rebate status filter")
	}
	if filter.InviterID < 0 {
		return nil, 0, errors.New("invalid admin rebate inviter filter")
	}
	limit = min(limit, maxAdminRebatePageSize)
	if offset > maxWalletRebateRecordOffset {
		offset = maxWalletRebateRecordOffset
	}

	base := db.Model(&RebateRecord{}).Where("1 = 1")
	if filter.InviterID > 0 {
		base = base.Where("inviter_id = ?", filter.InviterID)
	}
	if filter.Status != "" {
		base = base.Where("status = ?", filter.Status)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	records := make([]AdminWalletRebateView, 0)
	if total <= int64(offset) {
		return records, total, nil
	}
	query := db.Table("rebate_records AS r").
		Select(`r.id, r.settlement_key, r.inviter_id, COALESCE(i.username, '') AS inviter_username,
			r.invitee_id, COALESCE(u.username, '') AS invitee_username,
			r.eligible_money_micros, r.reward_money_micros, r.reward_quota, r.status,
			r.held_by_closure, r.voided_by, r.void_reason, r.created_at, r.available_at, r.paid_at`).
		Joins("LEFT JOIN users i ON i.id = r.inviter_id").
		Joins("LEFT JOIN users u ON u.id = r.invitee_id")
	if filter.InviterID > 0 {
		query = query.Where("r.inviter_id = ?", filter.InviterID)
	}
	if filter.Status != "" {
		query = query.Where("r.status = ?", filter.Status)
	}
	if err := query.Order("r.id DESC").Offset(offset).Limit(limit).Scan(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}
