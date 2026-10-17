package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// MoneyMicrosPerUnit defines fixed-point money storage: one site-money unit is
// represented by 1,000,000 integer micros. Accounting values are never stored
// as database floats or parsed from display strings.
const MoneyMicrosPerUnit int64 = 1_000_000

// WalletCreditSource identifies the origin of a wallet credit. A paid and a
// gifted part of one top-up must be represented as independent lots.
type WalletCreditSource string

const (
	WalletCreditSourceLegacy     WalletCreditSource = "legacy"
	WalletCreditSourceTrial      WalletCreditSource = "trial"
	WalletCreditSourceTopUp      WalletCreditSource = "topup"
	WalletCreditSourcePromotion  WalletCreditSource = "promotion"
	WalletCreditSourceRedemption WalletCreditSource = "redemption"
	WalletCreditSourceAdmin      WalletCreditSource = "admin"
	WalletCreditSourceAffiliate  WalletCreditSource = "affiliate"
	WalletCreditSourceRebate     WalletCreditSource = "rebate"
)

// WalletCreditLot is a source-attributed balance component. Its key is unique
// per event component, and its remaining balances must only be changed in the
// same transaction as the corresponding wallet settlement/allocation.
type WalletCreditLot struct {
	ID                     int64              `gorm:"primaryKey" json:"id"`
	CreditEventKey         string             `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	UserID                 int                `gorm:"not null;index:idx_wallet_lot_user_created,priority:1" json:"user_id"`
	Source                 WalletCreditSource `gorm:"type:varchar(32);not null" json:"source"`
	Component              string             `gorm:"type:varchar(24);not null" json:"component"`
	Eligible               bool               `gorm:"not null;default:false" json:"eligible"`
	ReferenceType          string             `gorm:"type:varchar(32);not null" json:"reference_type"`
	ReferenceID            string             `gorm:"type:varchar(128);not null" json:"reference_id"`
	QuotaTotal             int64              `gorm:"not null" json:"quota_total"`
	QuotaRemaining         int64              `gorm:"not null" json:"quota_remaining"`
	EligibleMoneyMicros    int64              `gorm:"not null;default:0" json:"eligible_money_micros"`
	EligibleMoneyRemaining int64              `gorm:"not null;default:0" json:"eligible_money_remaining_micros"`
	QuotaPerUnitMicros     int64              `gorm:"not null;default:0" json:"quota_per_unit_micros"`
	CreatedAt              time.Time          `gorm:"not null;index:idx_wallet_lot_user_created,priority:2" json:"created_at"`
	UpdatedAt              time.Time          `gorm:"not null" json:"updated_at"`
}

func (lot *WalletCreditLot) BeforeCreate(_ *gorm.DB) error {
	if lot.UserID <= 0 || !isCanonicalSHA256(lot.CreditEventKey) || strings.TrimSpace(lot.ReferenceType) == "" || strings.TrimSpace(lot.ReferenceID) == "" {
		return errors.New("invalid wallet credit identity")
	}
	if lot.QuotaTotal <= 0 || lot.QuotaRemaining != lot.QuotaTotal {
		return errors.New("new wallet credit lot must start with its full quota balance")
	}
	if lot.EligibleMoneyMicros < 0 || lot.EligibleMoneyRemaining != lot.EligibleMoneyMicros || lot.QuotaPerUnitMicros < 0 {
		return errors.New("invalid wallet credit valuation")
	}
	if lot.Eligible && lot.QuotaPerUnitMicros <= 0 {
		return errors.New("eligible wallet credit requires a positive quota-ratio snapshot")
	}
	switch lot.Source {
	case WalletCreditSourceLegacy:
		if lot.Eligible || lot.Component != "legacy_baseline" || lot.EligibleMoneyMicros != 0 || lot.EligibleMoneyRemaining != 0 {
			return errors.New("legacy wallet credits must remain non-eligible")
		}
	case WalletCreditSourceTrial:
		if lot.Eligible || lot.Component != "registration_trial" || lot.EligibleMoneyMicros != 0 || lot.EligibleMoneyRemaining != 0 {
			return errors.New("registration trial credits must remain non-eligible")
		}
	case WalletCreditSourceTopUp:
		if lot.Eligible != (lot.Component == "paid") || (lot.Component != "paid" && lot.Component != "gift") {
			return errors.New("top-up credit component eligibility is invalid")
		}
	case WalletCreditSourcePromotion:
		if lot.Eligible || lot.EligibleMoneyMicros != 0 || lot.EligibleMoneyRemaining != 0 {
			return errors.New("promotion wallet credits must remain non-eligible")
		}
	case WalletCreditSourceRedemption:
		if !lot.Eligible || lot.Component != "redeemed" {
			return errors.New("redemption wallet credits must use the eligible redeemed component")
		}
	case WalletCreditSourceAdmin:
		if lot.Eligible != (lot.Component == "ordinary") || (lot.Component != "ordinary" && lot.Component != "gift") {
			return errors.New("administrator credit component eligibility is invalid")
		}
	case WalletCreditSourceAffiliate, WalletCreditSourceRebate:
		if lot.Eligible || lot.EligibleMoneyMicros != 0 || lot.EligibleMoneyRemaining != 0 {
			return errors.New("affiliate and rebate wallet credits must remain non-eligible")
		}
	default:
		return errors.New("unknown wallet credit source")
	}
	if !lot.Eligible && (lot.EligibleMoneyMicros != 0 || lot.EligibleMoneyRemaining != 0) {
		return errors.New("non-eligible wallet credit cannot carry eligible money")
	}
	return nil
}

// WalletConsumptionSettlement is the idempotency header for a final wallet
// settlement. One header can own multiple lot allocations, but a settlement key
// can never debit the wallet twice.
type WalletConsumptionSettlement struct {
	ID                          int64  `gorm:"primaryKey" json:"id"`
	FinalSettlementKey          string `gorm:"type:char(64);not null;uniqueIndex" json:"final_settlement_key"`
	UserID                      int    `gorm:"not null;index" json:"user_id"`
	PreConsumedQuota            int64  `gorm:"not null;default:0" json:"pre_consumed_quota"`
	WalletQuota                 int64  `gorm:"not null" json:"wallet_quota"`
	EligibleQuotaConsumed       int64  `gorm:"not null;default:0" json:"eligible_quota_consumed"`
	EligibleMoneyConsumedMicros int64  `gorm:"not null;default:0" json:"eligible_money_consumed_micros"`
	UnallocatedQuota            int64  `gorm:"not null;default:0" json:"unallocated_quota"`
	DebtRepaidQuota             int64  `gorm:"not null;default:0" json:"debt_repaid_quota"`
	// Empty means ordinary consumption (including pre-migration settlements).
	AccountingKind string    `gorm:"type:varchar(24);not null;default:''" json:"accounting_kind"`
	FinalizedAt    time.Time `gorm:"not null" json:"finalized_at"`
}

func (settlement *WalletConsumptionSettlement) BeforeCreate(_ *gorm.DB) error {
	if !isCanonicalSHA256(settlement.FinalSettlementKey) || settlement.UserID <= 0 || settlement.PreConsumedQuota < 0 || settlement.WalletQuota < 0 || settlement.FinalizedAt.IsZero() {
		return errors.New("invalid wallet consumption settlement")
	}
	if settlement.EligibleQuotaConsumed < 0 || settlement.EligibleQuotaConsumed > settlement.WalletQuota || settlement.EligibleMoneyConsumedMicros < 0 || settlement.UnallocatedQuota < 0 || settlement.UnallocatedQuota > settlement.WalletQuota-settlement.EligibleQuotaConsumed {
		return errors.New("invalid wallet consumption eligibility")
	}
	if settlement.DebtRepaidQuota < 0 || settlement.DebtRepaidQuota > settlement.UnallocatedQuota {
		return errors.New("invalid wallet debt repayment")
	}
	return nil
}

// WalletConsumptionAllocation is an auditable link from one finalized billing
// settlement to one source lot. The composite unique index blocks retry-based
// duplicate consumption of the same lot for the same settlement.
type WalletConsumptionAllocation struct {
	ID                          int64     `gorm:"primaryKey" json:"id"`
	FinalSettlementKey          string    `gorm:"type:char(64);not null;uniqueIndex:ux_wallet_alloc_settlement_lot,priority:1" json:"final_settlement_key"`
	LotID                       int64     `gorm:"not null;uniqueIndex:ux_wallet_alloc_settlement_lot,priority:2" json:"lot_id"`
	UserID                      int       `gorm:"not null;index" json:"user_id"`
	QuotaConsumed               int64     `gorm:"not null" json:"quota_consumed"`
	EligibleQuotaConsumed       int64     `gorm:"not null;default:0" json:"eligible_quota_consumed"`
	EligibleMoneyConsumedMicros int64     `gorm:"not null;default:0" json:"eligible_money_consumed_micros"`
	CreatedAt                   time.Time `gorm:"not null" json:"created_at"`
}

func (allocation *WalletConsumptionAllocation) BeforeCreate(_ *gorm.DB) error {
	if !isCanonicalSHA256(allocation.FinalSettlementKey) || allocation.LotID <= 0 || allocation.UserID <= 0 || allocation.QuotaConsumed <= 0 {
		return errors.New("invalid wallet consumption allocation")
	}
	if allocation.EligibleQuotaConsumed < 0 || allocation.EligibleQuotaConsumed > allocation.QuotaConsumed || allocation.EligibleMoneyConsumedMicros < 0 {
		return errors.New("invalid wallet allocation eligibility")
	}
	return nil
}

// RebateCapBucket holds an atomically updated invitee lifetime contribution or
// inviter reward cap. Zero limit means that this cap is not configured.
type RebateCapBucket struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	OwnerID     int       `gorm:"not null;uniqueIndex:ux_rebate_cap_owner_type_period,priority:1" json:"owner_id"`
	CapType     string    `gorm:"type:varchar(24);not null;uniqueIndex:ux_rebate_cap_owner_type_period,priority:2" json:"cap_type"`
	PeriodKey   string    `gorm:"type:varchar(32);not null;uniqueIndex:ux_rebate_cap_owner_type_period,priority:3" json:"period_key"`
	LimitMicros int64     `gorm:"not null;default:0" json:"limit_micros"`
	UsedMicros  int64     `gorm:"not null;default:0" json:"used_micros"`
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`
}

func (bucket *RebateCapBucket) BeforeCreate(_ *gorm.DB) error {
	if bucket.OwnerID <= 0 || bucket.CapType == "" || bucket.PeriodKey == "" || bucket.LimitMicros < 0 || bucket.UsedMicros < 0 {
		return errors.New("invalid rebate cap bucket")
	}
	return nil
}

// RebateSystemState stores singleton activation/migration markers without
// modifying legacy user or invitation fields.
type RebateSystemState struct {
	Key       string    `gorm:"primaryKey;type:varchar(64)" json:"key"`
	Value     string    `gorm:"type:varchar(128);not null" json:"value"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// RebateRecord stores a direct-inviter reward calculation snapshot and its
// payout state. Money amounts use site-money micros; rate uses basis points.
type RebateRecord struct {
	ID                         int64      `gorm:"primaryKey" json:"id"`
	SettlementKey              string     `gorm:"type:char(64);not null;uniqueIndex" json:"settlement_key"`
	PayoutKey                  string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	InviterID                  int        `gorm:"not null;index:idx_rebate_inviter_status_created,priority:1" json:"inviter_id"`
	InviteeID                  int        `gorm:"not null;index:idx_rebate_invitee_created,priority:1" json:"invitee_id"`
	EligibleQuota              int64      `gorm:"not null;default:0" json:"eligible_quota"`
	EligibleMoneyMicros        int64      `gorm:"not null;default:0" json:"eligible_money_micros"`
	RateBasisPoints            int64      `gorm:"not null;default:0" json:"rate_basis_points"`
	QuotaPerUnitMicros         int64      `gorm:"not null;default:0" json:"quota_per_unit_micros"`
	RewardMoneyMicros          int64      `gorm:"not null;default:0" json:"reward_money_micros"`
	RewardQuota                int64      `gorm:"not null;default:0" json:"reward_quota"`
	DiscardedRewardMoneyMicros int64      `gorm:"not null;default:0" json:"discarded_reward_money_micros"`
	InviteeCapLimitMicros      int64      `gorm:"not null;default:0" json:"invitee_cap_limit_micros"`
	InviteeCapUsedAfterMicros  int64      `gorm:"not null;default:0" json:"invitee_cap_used_after_micros"`
	InviterCapLimitMicros      int64      `gorm:"not null;default:0" json:"inviter_cap_limit_micros"`
	InviterCapUsedAfterMicros  int64      `gorm:"not null;default:0" json:"inviter_cap_used_after_micros"`
	InviterCapPeriodKey        string     `gorm:"type:varchar(32);not null;default:''" json:"inviter_cap_period_key"`
	Status                     string     `gorm:"type:varchar(24);not null;index:idx_rebate_due,priority:1;index:idx_rebate_inviter_status_created,priority:2" json:"status"`
	AvailableAt                time.Time  `gorm:"not null;index:idx_rebate_due,priority:2" json:"available_at"`
	CountedDuringClosure       bool       `gorm:"not null;default:false" json:"counted_during_closure"`
	HeldByClosure              bool       `gorm:"not null;default:false" json:"held_by_closure"`
	PayoutLotID                *int64     `json:"payout_lot_id"`
	PaidAt                     *time.Time `json:"paid_at"`
	VoidedAt                   *time.Time `json:"voided_at"`
	VoidedBy                   int        `gorm:"not null;default:0" json:"voided_by"`
	VoidReason                 string     `gorm:"type:varchar(128);not null;default:''" json:"void_reason"`
	CreatedAt                  time.Time  `gorm:"not null;index:idx_rebate_invitee_created,priority:2;index:idx_rebate_inviter_status_created,priority:3" json:"created_at"`
	UpdatedAt                  time.Time  `gorm:"not null" json:"updated_at"`
}

const (
	RebateStatusPending   = "pending"
	RebateStatusHeld      = "held"
	RebateStatusPaid      = "paid"
	RebateStatusVoided    = "voided"
	RebateStatusDiscarded = "discarded"
	RebateStatusFailed    = "failed"

	RebateCapTypeInviteeLifetime = "invitee_lifetime"
	RebateCapTypeInviterDaily    = "inviter_daily"
	RebateCapTypeInviterMonthly  = "inviter_monthly"
	RebateCapTypeInviterLifetime = "inviter_lifetime"
)

func (record *RebateRecord) BeforeCreate(_ *gorm.DB) error {
	if record.InviterID <= 0 || record.InviteeID <= 0 || record.InviterID == record.InviteeID {
		return errors.New("invalid rebate participants")
	}
	if !isCanonicalSHA256(record.SettlementKey) || !isCanonicalSHA256(record.PayoutKey) {
		return errors.New("invalid rebate idempotency key")
	}
	if record.EligibleQuota < 0 || record.EligibleMoneyMicros < 0 || record.RateBasisPoints < 0 || record.RateBasisPoints > 10_000 || record.QuotaPerUnitMicros < 0 || record.RewardMoneyMicros < 0 || record.RewardQuota < 0 || record.DiscardedRewardMoneyMicros < 0 {
		return errors.New("invalid rebate amounts")
	}
	if record.InviteeCapLimitMicros < 0 || record.InviteeCapUsedAfterMicros < 0 || record.InviterCapLimitMicros < 0 || record.InviterCapUsedAfterMicros < 0 {
		return errors.New("invalid rebate cap snapshot")
	}
	switch record.Status {
	case RebateStatusPending, RebateStatusHeld, RebateStatusPaid, RebateStatusVoided, RebateStatusDiscarded, RebateStatusFailed:
	default:
		return errors.New("invalid rebate status")
	}
	if record.AvailableAt.IsZero() {
		return errors.New("rebate available time is required")
	}
	return nil
}

func isCanonicalSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// NewRebateIdempotencyKey creates a canonical ASCII key from server-owned
// identity components. Reservations and their final settlement must share the
// same server-owned durable billing identity; never use client-provided IDs.
func NewRebateIdempotencyKey(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", errors.New("idempotency key requires identity components")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return "", errors.New("idempotency key components must not be empty")
		}
	}
	payload, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// NewWalletFinalSettlementKey derives the stable server-owned identity of one
// finalized wallet billing operation. The durable billing ID must identify the
// same billable operation across process restarts and retries; transient
// in-memory identities must never be used.
func NewWalletFinalSettlementKey(userID int, billingKind, durableBillingID string) (string, error) {
	if userID <= 0 || strings.TrimSpace(billingKind) == "" || strings.TrimSpace(durableBillingID) == "" {
		return "", errors.New("wallet final settlement requires a user and a durable billing id")
	}
	return NewRebateIdempotencyKey("wallet-final-v1", fmt.Sprintf("user:%d", userID), billingKind, durableBillingID)
}

// NewWalletCreditEventKey creates a unique stable key per event component so
// one top-up can safely create separate paid and gifted lots.
func NewWalletCreditEventKey(userID int, source WalletCreditSource, referenceType, referenceID, component string) (string, error) {
	if userID <= 0 || strings.TrimSpace(string(source)) == "" || strings.TrimSpace(referenceType) == "" || strings.TrimSpace(referenceID) == "" || strings.TrimSpace(component) == "" {
		return "", errors.New("invalid wallet credit event identity")
	}
	return NewRebateIdempotencyKey(fmt.Sprintf("user:%d", userID), string(source), referenceType, referenceID, component)
}
