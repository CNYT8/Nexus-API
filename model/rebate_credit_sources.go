package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// CreditWalletSourceTx records one server-owned non-top-up wallet credit. The
// legacy path remains available until the wallet baseline is initialized; once
// active, every credit is represented by an immutable source lot.
func CreditWalletSourceTx(tx *gorm.DB, userID int, source WalletCreditSource, component, referenceType, referenceID string, quota int) (bool, error) {
	if tx == nil || userID <= 0 || quota <= 0 || strings.TrimSpace(component) == "" || strings.TrimSpace(referenceType) == "" || strings.TrimSpace(referenceID) == "" {
		return false, errors.New("invalid wallet credit source")
	}
	active, err := walletCreditBaselineState(tx)
	if err != nil {
		return false, err
	}
	if !active {
		// Preserve the legacy quota-only semantics exactly, including NULL
		// quota normalization and stored quota-limit checks.
		if err := addUserQuotaTx(tx, userID, quota); err != nil {
			return false, err
		}
		return true, nil
	}

	// Redeemed codes and administrator ordinary balance additions are
	// rebate-qualifying; explicit gift grants (component=="gift") remain non-eligible.
	eligible := (source == WalletCreditSourceRedemption && component == "redeemed") ||
		(source == WalletCreditSourceAdmin && component == "ordinary")
	moneyMicros := int64(0)
	ratioMicros := int64(0)
	if eligible {
		moneyMicros, ratioMicros, err = EligibleMoneyValuationForQuota(int64(quota))
		if err != nil {
			return false, err
		}
	}
	key, err := NewWalletCreditEventKey(userID, source, referenceType, referenceID, component)
	if err != nil {
		return false, err
	}
	return AddWalletCreditLotTx(tx, WalletCreditLot{
		CreditEventKey:         key,
		UserID:                 userID,
		Source:                 source,
		Component:              component,
		Eligible:               eligible,
		ReferenceType:          referenceType,
		ReferenceID:            referenceID,
		QuotaTotal:             int64(quota),
		QuotaRemaining:         int64(quota),
		EligibleMoneyMicros:    moneyMicros,
		EligibleMoneyRemaining: moneyMicros,
		QuotaPerUnitMicros:     ratioMicros,
	})
}

func walletCreditReferenceID(id int) string {
	return strconv.Itoa(id)
}

func invalidateWalletCreditCache(userID int) {
	if err := InvalidateUserCache(userID); err != nil {
		common.SysError("failed to invalidate wallet credit cache: " + err.Error())
	}
}

// CreditWalletSourceUniqueTx credits a legacy caller that has no durable
// business identity. Every successful call creates exactly one lot; a caller
// that retries an ambiguous commit duplicates the mutation, exactly like the
// legacy quota-only writer did, so residual legacy paths use it knowingly.
func CreditWalletSourceUniqueTx(tx *gorm.DB, userID int, source WalletCreditSource, component string, quota int) (bool, error) {
	reference, err := newWalletServerReference(component)
	if err != nil {
		return false, err
	}
	return CreditWalletSourceTx(tx, userID, source, component, "server_generated", reference, quota)
}

func newWalletServerReference(kind string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return kind + ":" + hex.EncodeToString(entropy[:]), nil
}

// NewWalletAdjustmentKey derives a canonical debit key from the supplied
// durable identity components. Callers without any durable identity use
// newWalletUniqueAdjustmentKey, which appends a random server reference so one
// logical mutation never shares a key with another.
func NewWalletAdjustmentKey(userID int, kind string, identity ...string) (string, error) {
	parts := []string{"wallet-adjustment-v1", kind, "user:" + strconv.Itoa(userID)}
	parts = append(parts, identity...)
	return NewRebateIdempotencyKey(parts...)
}

func newWalletUniqueAdjustmentKey(userID int, kind string) (string, error) {
	reference, err := newWalletServerReference(kind)
	if err != nil {
		return "", err
	}
	return NewWalletAdjustmentKey(userID, kind, reference)
}

// AdjustWalletDebitUniqueTx executes one non-consumption debit whose caller has
// no durable business identity. It still retires source lots, so wallet
// provenance and rebate counting stay correct after a manual adjustment.
func AdjustWalletDebitUniqueTx(tx *gorm.DB, userID int, quota int64, kind string) error {
	key, err := newWalletUniqueAdjustmentKey(userID, kind)
	if err != nil {
		return err
	}
	return AdjustWalletDebitTx(tx, key, userID, quota)
}
