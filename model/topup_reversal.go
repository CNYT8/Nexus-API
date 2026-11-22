package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrTopUpPaymentUnbound   = errors.New("top-up has no verified payment binding or original credit snapshot")
	ErrTopUpReversalConflict = errors.New("top-up reversal identity or immutable amount conflict")
	ErrTopUpReversalHeld     = errors.New("top-up reversal waits for active wallet reservations")
)

// TopUpPaymentIdentity is populated ONLY by a signature-verified provider
// adapter. PaidMinor is in the currency's minor units, never site-money units.
type TopUpPaymentIdentity struct {
	Provider   string
	CheckoutID string
	PaymentID  string
	Currency   string
	PaidMinor  int64
}

// TopUpPaymentBinding separates provider cash from immutable credited quota.
// CheckoutID is persisted from the server-side payment creation response.
// An untrusted metadata/reference string is never sufficient to bind payment.
type TopUpPaymentBinding struct {
	ID              int64   `gorm:"primaryKey"`
	TradeNo         string  `gorm:"type:varchar(128);not null;uniqueIndex"`
	Provider        string  `gorm:"type:varchar(32);not null"`
	CheckoutID      string  `gorm:"type:varchar(191);not null"`
	CheckoutKey     string  `gorm:"type:char(64);not null;uniqueIndex"`
	PaymentID       string  `gorm:"type:varchar(191);not null;default:''"`
	PaymentKey      *string `gorm:"type:char(64);uniqueIndex"`
	Currency        string  `gorm:"type:varchar(8);not null;default:''"`
	PaidMinor       int64   `gorm:"not null;default:0"`
	UserID          int     `gorm:"not null;index"`
	CreditedQuota   int64   `gorm:"not null;default:0"`
	LotID           int64   `gorm:"not null;default:0"`
	ReversalPending bool    `gorm:"not null;default:false;index"`
	// Independent from accounting pending: unknown refund/dispute overlap
	// remains quarantined even after the certain lower bound has been debited.
	ReversalAmbiguous bool `gorm:"not null;default:false;index"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// State is per verified provider object (refund total, refund item or dispute).
// Terminal dispute outcomes dominate earlier notifications, including ties.
type TopUpReversalState struct {
	ID          int64  `gorm:"primaryKey"`
	BindingID   int64  `gorm:"not null;index"`
	ObjectKey   string `gorm:"type:char(64);not null;uniqueIndex"`
	Kind        string `gorm:"type:varchar(24);not null"`
	GroupID     string `gorm:"type:varchar(191);not null;default:''"`
	AmountMinor int64  `gorm:"not null"`
	OccurredAt  int64  `gorm:"not null"`
	Terminal    bool   `gorm:"not null;default:false"`
	Released    bool   `gorm:"not null;default:false"`
}

// A receipt commits with verified state and source quarantine, not an ACK.
// ReversalPending stays true until ALL wallet/reward mutations commit; receipt
// replay must retry that second phase and must never acknowledge pending work.
type TopUpReversalEvent struct {
	ID          int64  `gorm:"primaryKey"`
	EventKey    string `gorm:"type:char(64);not null;uniqueIndex"`
	Fingerprint string `gorm:"type:char(64);not null"`
	BindingID   int64  `gorm:"not null;index"`
	CreatedAt   time.Time
}

// VerifiedTopUpReversal must not be exposed by an authenticated user API.
// refund_total is cumulative; refund_item is one immutable successful refund.
// All deliveries of an item must use the same stable provider ObjectID.
type VerifiedTopUpReversal struct {
	Provider  string
	PaymentID string
	EventID   string
	ObjectID  string
	// Disputes only: immutable root provider dispute ID. Empty means ObjectID.
	// Later lifecycle rounds use the first round's ID, never a payment ID.
	GroupID       string
	Kind          string
	Currency      string
	AmountMinor   int64
	OriginalMinor int64
	OccurredAt    int64
	Terminal      bool
	Released      bool
}

// TopUpProviderMinorAmount converts an exact positive decimal provider amount.
// It never rounds, never accepts scientific notation, and never uses float64.
func TopUpProviderMinorAmount(amount string, decimalPlaces int32) (int64, error) {
	if amount == "" || len(amount) > 64 || decimalPlaces < 0 || decimalPlaces > 3 {
		return 0, ErrTopUpReversalConflict
	}
	parts := strings.Split(amount, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, ErrTopUpReversalConflict
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return 0, ErrTopUpReversalConflict
		}
	}
	if len(fraction) > int(decimalPlaces) {
		return 0, ErrTopUpReversalConflict
	}
	digits := parts[0] + fraction
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, ErrTopUpReversalConflict
		}
	}
	digits += strings.Repeat("0", int(decimalPlaces)-len(fraction))
	digits = strings.TrimLeft(digits, "0")
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || value <= 0 {
		return 0, ErrTopUpReversalConflict
	}
	return value, nil
}

// Old internal callers can finish only orders without a checkout binding. A
// newly registered order may never bypass its verified provider identity.
func bindTopUpPaymentIfProvidedTx(tx *gorm.DB, order *TopUp, identities []TopUpPaymentIdentity, creditedQuota int64) error {
	if len(identities) == 1 {
		return BindTopUpPaymentTx(tx, order, identities[0], creditedQuota)
	}
	if len(identities) != 0 {
		return ErrTopUpReversalConflict
	}
	var binding TopUpPaymentBinding
	err := walletCurrentTx(tx).Where("trade_no = ?", order.TradeNo).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrTopUpPaymentUnbound
}

func topUpPaymentKey(provider, id string) (string, error) {
	if provider == "" || id == "" || len(provider) > 32 || len(id) > 191 || strings.TrimSpace(id) != id {
		return "", ErrTopUpReversalConflict
	}
	return NewRebateIdempotencyKey("provider-payment-v1", provider, id)
}

// RegisterTopUpCheckout must finish before returning the checkout URL. It
// never credits quota and never accepts an identity taken from webhook metadata.
func RegisterTopUpCheckout(tradeNo, provider, checkoutID string) error {
	key, err := topUpPaymentKey(provider, checkoutID)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
			return err
		}
		if order.PaymentProvider != provider || order.Status != common.TopUpStatusPending {
			return ErrTopUpReversalConflict
		}
		var prior TopUpPaymentBinding
		err := walletCurrentTx(tx).Where("trade_no = ?", tradeNo).First(&prior).Error
		if err == nil {
			if prior.Provider != provider || prior.CheckoutID != checkoutID || prior.UserID != order.UserId {
				return ErrTopUpReversalConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&TopUpPaymentBinding{TradeNo: tradeNo, Provider: provider, CheckoutID: checkoutID, CheckoutKey: key, UserID: order.UserId}).Error
	})
}

// BindTopUpPaymentTx runs inside the successful recharge transaction AFTER
// CreditTopUpTx. creditedQuota=0 is only for a duplicate callback: it may use an
// existing original lot, but never recomputes a historical credit at today's ratio.
func BindTopUpPaymentTx(tx *gorm.DB, order *TopUp, identity TopUpPaymentIdentity, creditedQuota int64) error {
	if tx == nil || order == nil || identity.Provider != order.PaymentProvider || identity.PaidMinor <= 0 || creditedQuota < 0 {
		return ErrTopUpReversalConflict
	}
	identity.Currency = strings.ToUpper(strings.TrimSpace(identity.Currency))
	if len(identity.Currency) != 3 {
		return ErrPaymentCurrencyMismatch
	}
	key, err := topUpPaymentKey(identity.Provider, identity.PaymentID)
	if err != nil {
		return err
	}
	var binding TopUpPaymentBinding
	if err := walletCurrentTx(tx).Where("trade_no = ?", order.TradeNo).First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Historical success is an ACK-only replay. Metadata cannot establish
			// checkout/payment identity, reconstruct quota, or credit again.
			if creditedQuota == 0 && order.Status == common.TopUpStatusSuccess {
				return nil
			}
			return ErrTopUpPaymentUnbound
		}
		return err
	}
	if binding.Provider != identity.Provider || binding.CheckoutID != identity.CheckoutID || binding.UserID != order.UserId {
		return ErrTopUpReversalConflict
	}
	if binding.PaymentID != "" {
		if binding.PaymentID != identity.PaymentID || binding.Currency != identity.Currency || binding.PaidMinor != identity.PaidMinor || (creditedQuota > 0 && binding.CreditedQuota != creditedQuota) {
			return ErrTopUpReversalConflict
		}
		return nil
	}
	var lot WalletCreditLot
	err = walletCurrentTx(tx).Where("user_id = ? AND source = ? AND component = ? AND reference_type = ? AND reference_id = ?", order.UserId, WalletCreditSourceTopUp, "paid", "topup", order.TradeNo).First(&lot).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		if creditedQuota > 0 && lot.QuotaTotal != creditedQuota {
			return ErrTopUpReversalConflict
		}
		creditedQuota = lot.QuotaTotal
	}
	if creditedQuota <= 0 {
		return ErrTopUpPaymentUnbound
	}
	return tx.Model(&binding).Updates(map[string]interface{}{
		"payment_id": identity.PaymentID, "payment_key": key, "currency": identity.Currency,
		"paid_minor": identity.PaidMinor, "credited_quota": creditedQuota, "lot_id": lot.ID,
	}).Error
}

func nextTopUpReversalState(old TopUpReversalState, event VerifiedTopUpReversal) (TopUpReversalState, error) {
	if event.AmountMinor < 0 || event.OccurredAt <= 0 {
		return old, ErrTopUpReversalConflict
	}
	if event.Kind == "refund_item" {
		if event.AmountMinor <= 0 || event.Released || !event.Terminal {
			return old, ErrTopUpReversalConflict
		}
		if old.ID != 0 && old.AmountMinor != event.AmountMinor {
			return old, ErrTopUpReversalConflict
		}
		old.AmountMinor, old.Terminal = event.AmountMinor, true
		old.OccurredAt = max(old.OccurredAt, event.OccurredAt)
		return old, nil
	}
	if event.Kind == "refund_total" {
		if event.Released || !event.Terminal {
			return old, ErrTopUpReversalConflict
		}
		if event.AmountMinor > old.AmountMinor {
			old.AmountMinor = event.AmountMinor
		}
		old.Terminal = true
		old.OccurredAt = max(old.OccurredAt, event.OccurredAt)
		return old, nil
	}
	if event.Kind != "dispute" || event.AmountMinor <= 0 || (event.Released && !event.Terminal) {
		return old, ErrTopUpReversalConflict
	}
	// A later round can reserve its root before the root's webhook arrives.
	// Only that zero-valued, unobserved root may acquire its first amount.
	reservedRoot := old.ID != 0 && old.Kind == "dispute" && old.GroupID != "" && old.AmountMinor == 0 && old.OccurredAt == 0 && !old.Terminal && !old.Released
	if old.ID != 0 && !reservedRoot && old.AmountMinor != event.AmountMinor {
		return old, ErrTopUpReversalConflict
	}
	if old.Terminal {
		if event.Terminal && old.Released != event.Released {
			return old, ErrTopUpReversalConflict
		}
		return old, nil
	}
	if !event.Terminal && event.OccurredAt < old.OccurredAt {
		return old, nil
	}
	old.AmountMinor, old.OccurredAt = event.AmountMinor, event.OccurredAt
	old.Terminal, old.Released = event.Terminal, event.Released
	return old, nil
}

// topUpReversedMinor is only the certain lower bound, NOT proof that all
// reversal exposure has been accounted for. Callers must inspect ambiguity.
func topUpReversedMinor(states []TopUpReversalState, paid int64) (int64, error) {
	minor, _, err := topUpReversalExposure(states, paid)
	return minor, err
}

func topUpReversalExposure(states []TopUpReversalState, paid int64) (int64, int64, error) {
	if paid <= 0 {
		return 0, 0, ErrTopUpReversalConflict
	}
	var refunded, items, disputed int64
	disputeGroups := make(map[string]int64)
	for _, state := range states {
		if state.AmountMinor < 0 || state.AmountMinor > paid {
			return 0, 0, ErrTopUpReversalConflict
		}
		var err error
		switch state.Kind {
		case "refund_total":
			refunded, err = walletCheckedAdd(refunded, state.AmountMinor)
		case "refund_item":
			if !state.Terminal || state.Released || state.AmountMinor == 0 {
				return 0, 0, ErrTopUpReversalConflict
			}
			items, err = walletCheckedAdd(items, state.AmountMinor)
		case "dispute":
			if !state.Released {
				if state.GroupID == "" {
					// Pre-grouping states are independent provider dispute objects.
					disputed, err = walletCheckedAdd(disputed, state.AmountMinor)
				} else {
					disputeGroups[state.GroupID] = max(disputeGroups[state.GroupID], state.AmountMinor)
				}
			}
		default:
			return 0, 0, ErrTopUpReversalConflict
		}
		if err != nil {
			return 0, 0, err
		}
	}
	for _, principal := range disputeGroups {
		var err error
		disputed, err = walletCheckedAdd(disputed, principal)
		if err != nil {
			return 0, 0, err
		}
	}
	if refunded > paid || items > paid || disputed > paid {
		return 0, 0, ErrTopUpReversalConflict
	}
	// Refund totals/items are the same cash principal. Dispute groups remain
	// independent of one another, but may overlap refunds by an unknown amount.
	refund := max(refunded, items)
	certain := max(refund, disputed)
	possible := paid
	if disputed <= paid-refund {
		possible = refund + disputed
	}
	return certain, possible, nil
}

// ApplyVerifiedTopUpReversal is callable only from verified provider adapters.
// Lock order: top-up -> baseline gate -> wallet owner -> binding -> source
// lot/allocations -> rebate record -> inviter wallet. Payouts lock the invitee
// before the record too. Cross-invitation deadlocks surface for safe retry.
// Phase one persists validated state + source quarantine; phase two commits
// ALL balance/debt/reward changes together. A durable receipt is NOT an ACK.
func ApplyVerifiedTopUpReversal(db *gorm.DB, event VerifiedTopUpReversal) error {
	if db == nil || event.EventID == "" || len(event.EventID) > 191 || event.ObjectID == "" || len(event.ObjectID) > 191 {
		return ErrTopUpReversalConflict
	}
	if strings.TrimSpace(event.ObjectID) != event.ObjectID || len(event.GroupID) > 191 || strings.TrimSpace(event.GroupID) != event.GroupID || (event.Kind != "dispute" && event.GroupID != "") {
		return ErrTopUpReversalConflict
	}
	if event.Kind == "dispute" && event.GroupID == "" {
		event.GroupID = event.ObjectID
	}
	key, err := topUpPaymentKey(event.Provider, event.PaymentID)
	if err != nil {
		return err
	}
	event.Currency = strings.ToUpper(strings.TrimSpace(event.Currency))
	eventKey, err := NewRebateIdempotencyKey("topup-reversal-event-v1", event.Provider, event.EventID)
	if err != nil {
		return err
	}
	fingerprint, err := NewRebateIdempotencyKey("topup-reversal-payload-v1", event.Provider, event.PaymentID, event.ObjectID, event.Kind, event.Currency, fmt.Sprint(event.AmountMinor), fmt.Sprint(event.OriginalMinor), fmt.Sprint(event.OccurredAt), fmt.Sprint(event.Terminal), fmt.Sprint(event.Released))
	if err != nil {
		return err
	}
	// Preserve existing receipt fingerprints for independent disputes. A child
	// round adds its immutable root, so a replay cannot silently change groups.
	if event.Kind == "dispute" && event.GroupID != event.ObjectID {
		fingerprint, err = NewRebateIdempotencyKey("topup-reversal-grouped-payload-v1", fingerprint, event.GroupID)
		if err != nil {
			return err
		}
	}
	var hint TopUpPaymentBinding
	if err := db.Where("payment_key = ?", key).First(&hint).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTopUpPaymentUnbound
		}
		return err
	}
	changedUsers := map[int]bool{}
	err = db.Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := lockForUpdate(tx).Where("trade_no = ?", hint.TradeNo).First(&order).Error; err != nil {
			return err
		}
		if _, err := LockWalletCreditBaselineTx(tx); err != nil {
			return err
		}
		if _, err := walletLockUserTx(tx, order.UserId); err != nil {
			return err
		}
		var binding TopUpPaymentBinding
		if err := walletCurrentTx(tx).Where("id = ?", hint.ID).First(&binding).Error; err != nil {
			return err
		}
		if order.Status != common.TopUpStatusSuccess || binding.UserID != order.UserId || binding.Provider != event.Provider || binding.PaymentID != event.PaymentID || binding.CreditedQuota <= 0 || binding.PaidMinor <= 0 {
			return ErrTopUpReversalConflict
		}
		// A missing original lot cannot safely support quarantine, debt or a
		// later baseline transition. Reject even before the ledger is active.
		if binding.LotID <= 0 {
			return ErrTopUpPaymentUnbound
		}
		if binding.Currency != event.Currency {
			return ErrPaymentCurrencyMismatch
		}
		if event.AmountMinor > binding.PaidMinor || event.OriginalMinor < 0 || (event.OriginalMinor != 0 && event.OriginalMinor != binding.PaidMinor) {
			return ErrTopUpReversalConflict
		}
		var receipt TopUpReversalEvent
		err := walletCurrentTx(tx).Where("event_key = ?", eventKey).First(&receipt).Error
		if err == nil {
			if receipt.BindingID != binding.ID || receipt.Fingerprint != fingerprint {
				return ErrTopUpReversalConflict
			}
			// Upgrade old max-only receipts too; a stored receipt alone is not
			// evidence that today's quarantine/accounting projection is complete.
			return stageTopUpReversalTx(tx, &binding, changedUsers)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		objectKey, err := NewRebateIdempotencyKey("topup-reversal-object-v1", event.Provider, event.Kind, event.ObjectID)
		if err != nil {
			return err
		}
		var state TopUpReversalState
		err = walletCurrentTx(tx).Where("object_key = ?", objectKey).First(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if state.ID != 0 && (state.BindingID != binding.ID || state.Kind != event.Kind) {
			return ErrTopUpReversalConflict
		}
		if event.Kind == "dispute" {
			if err := bindTopUpDisputeGroupTx(tx, binding.ID, event, &state); err != nil {
				return err
			}
		}
		state, err = nextTopUpReversalState(state, event)
		if err != nil {
			return err
		}
		state.BindingID, state.ObjectKey, state.Kind = binding.ID, objectKey, event.Kind
		if err := tx.Save(&state).Error; err != nil {
			return err
		}
		if err := stageTopUpReversalTx(tx, &binding, changedUsers); err != nil {
			return err
		}
		return tx.Create(&TopUpReversalEvent{EventKey: eventKey, Fingerprint: fingerprint, BindingID: binding.ID, CreatedAt: time.Now().UTC()}).Error
	})
	if err != nil {
		return err
	}
	for userID := range changedUsers {
		_ = walletAfterCommit(db, userID, nil)
	}
	// Receipt replay MUST retry accounting, including after a process crash.
	return ResumePendingTopUpReversal(db, hint.ID)
}

// Stage source and related reward quarantine atomically with the verified
// state. Ambiguity freezes this source, not unrelated lots or uncertain debt.
func stageTopUpReversalTx(tx *gorm.DB, binding *TopUpPaymentBinding, changed map[int]bool) error {
	if binding.LotID <= 0 {
		return ErrTopUpPaymentUnbound
	}
	var states []TopUpReversalState
	if err := walletCurrentTx(tx).Where("binding_id = ?", binding.ID).Find(&states).Error; err != nil {
		return err
	}
	minor, possible, err := topUpReversalExposure(states, binding.PaidMinor)
	if err != nil {
		return err
	}
	target, err := walletFloorProduct(binding.CreditedQuota, minor, binding.PaidMinor)
	if err != nil {
		return err
	}
	quarantine, err := walletFloorProduct(binding.CreditedQuota, possible, binding.PaidMinor)
	if err != nil {
		return err
	}
	ambiguous := possible > minor
	key, err := NewRebateIdempotencyKey("topup-source-reversal-v1", fmt.Sprint(binding.ID))
	if err != nil {
		return err
	}
	if err := stageWalletSourceReversalTx(tx, key, binding.UserID, binding.LotID, binding.CreditedQuota, target, quarantine, changed); err != nil {
		return err
	}
	return tx.Model(binding).Updates(map[string]interface{}{"reversal_pending": true, "reversal_ambiguous": ambiguous}).Error
}

// bindTopUpDisputeGroupTx uses the globally unique provider/root object key
// to bind every lifecycle round to one payment, even if a child arrives first.
// A placeholder has no observed principal or outcome; it cannot debit cash.
func bindTopUpDisputeGroupTx(tx *gorm.DB, bindingID int64, event VerifiedTopUpReversal, state *TopUpReversalState) error {
	if state.ID != 0 {
		group := state.GroupID
		if group == "" {
			group = event.ObjectID // legacy objects were independent roots
		}
		if group != event.GroupID {
			return ErrTopUpReversalConflict
		}
	}
	state.GroupID = event.GroupID
	if event.GroupID == event.ObjectID {
		return nil
	}
	rootKey, err := NewRebateIdempotencyKey("topup-reversal-object-v1", event.Provider, "dispute", event.GroupID)
	if err != nil {
		return err
	}
	var root TopUpReversalState
	err = walletCurrentTx(tx).Where("object_key = ?", rootKey).First(&root).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A concurrent other-payment claim loses the unique root key; its whole
		// transaction rolls back and the provider can safely retry.
		return tx.Create(&TopUpReversalState{BindingID: bindingID, ObjectKey: rootKey, Kind: "dispute", GroupID: event.GroupID}).Error
	}
	if err != nil {
		return err
	}
	if root.BindingID != bindingID || root.Kind != "dispute" || (root.GroupID != "" && root.GroupID != event.GroupID) {
		return ErrTopUpReversalConflict
	}
	if root.GroupID == "" {
		// Upgrade a pre-grouping root before aggregating the child, otherwise its
		// principal would also be counted as an independent dispute.
		return tx.Model(&root).Update("group_id", event.GroupID).Error
	}
	return nil
}

// ResumePendingTopUpReversal is an internal recovery interface for a persisted
// binding, not a user-callable refund API. It accepts no new amount/identity.
// Provider retries and maintenance can use it after holds finish or a crash.
func ResumePendingTopUpReversal(db *gorm.DB, bindingID int64) error {
	if db == nil || bindingID <= 0 {
		return ErrTopUpReversalConflict
	}
	var hint TopUpPaymentBinding
	if err := db.Where("id = ?", bindingID).First(&hint).Error; err != nil {
		return err
	}
	changedUsers := map[int]bool{}
	err := db.Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := lockForUpdate(tx).Where("trade_no = ?", hint.TradeNo).First(&order).Error; err != nil {
			return err
		}
		if _, err := LockWalletCreditBaselineTx(tx); err != nil {
			return err
		}
		if _, err := walletLockUserTx(tx, order.UserId); err != nil {
			return err
		}
		var binding TopUpPaymentBinding
		if err := walletCurrentTx(tx).Where("id = ?", bindingID).First(&binding).Error; err != nil {
			return err
		}
		if binding.LotID <= 0 {
			return ErrTopUpPaymentUnbound
		}
		if !binding.ReversalPending {
			return nil
		}
		if order.Status != common.TopUpStatusSuccess || binding.UserID != order.UserId || binding.CreditedQuota <= 0 || binding.PaidMinor <= 0 {
			return ErrTopUpReversalConflict
		}
		if err := stageTopUpReversalTx(tx, &binding, changedUsers); err != nil {
			return err
		}
		var states []TopUpReversalState
		if err := walletCurrentTx(tx).Where("binding_id = ?", binding.ID).Find(&states).Error; err != nil {
			return err
		}
		minor, possible, err := topUpReversalExposure(states, binding.PaidMinor)
		if err != nil {
			return err
		}
		target, err := walletFloorProduct(binding.CreditedQuota, minor, binding.PaidMinor)
		if err != nil {
			return err
		}
		quarantine, err := walletFloorProduct(binding.CreditedQuota, possible, binding.PaidMinor)
		if err != nil {
			return err
		}
		key, err := NewRebateIdempotencyKey("topup-source-reversal-v1", fmt.Sprint(binding.ID))
		if err != nil {
			return err
		}
		if err := reverseWalletSourceTx(tx, key, binding.UserID, binding.LotID, binding.CreditedQuota, target, quarantine, changedUsers); err != nil {
			return err
		}
		return tx.Model(&binding).Update("reversal_pending", false).Error
	})
	if err == nil {
		for userID := range changedUsers {
			_ = walletAfterCommit(db, userID, nil)
		}
	}
	return err
}
