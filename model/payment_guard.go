package model

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrPaymentConfirmationInvalid = errors.New("payment confirmation invalid")
	ErrPaymentAmountMismatch      = errors.New("payment amount mismatch")
	ErrPaymentCurrencyMismatch    = errors.New("payment currency mismatch")
	ErrPaymentOrderMismatch       = errors.New("payment order mismatch")

	// Top-up quota sentinels are shared by the controller pre-payment checks
	// and the atomic settlement guard so callers can distinguish invalid
	// input, a missing user and a wallet ceiling rejection with errors.Is.
	ErrInvalidTopUpQuota       = errors.New("无效的充值额度")
	ErrTopUpQuotaOutOfRange    = errors.New("充值额度超出允许范围")
	ErrTopUpUserNotFound       = errors.New("充值用户不存在")
	ErrTopUpQuotaLimitExceeded = errors.New("本次充值后额度将超出允许范围")
)

// PaymentConfirmation contains provider-signed values that must match the
// immutable local order before any balance or subscription is granted.
type PaymentConfirmation struct {
	Amount           string
	Currency         string
	ExpectedCurrency string
	DecimalPlaces    int32
	// AllowOverpayment accepts provider-added tax while still rejecting an
	// underpayment. Gateways without external tax calculation remain exact.
	AllowOverpayment bool
	// UseGoFloatRounding matches strconv/fmt formatting used by legacy Epay
	// and Waffo order creation at exact half-way values.
	UseGoFloatRounding bool
}

func (confirmation PaymentConfirmation) Validate(expectedAmount float64) error {
	if confirmation.DecimalPlaces < 0 || confirmation.DecimalPlaces > 6 {
		return ErrPaymentConfirmationInvalid
	}
	if math.IsNaN(expectedAmount) || math.IsInf(expectedAmount, 0) || expectedAmount <= 0 {
		return ErrPaymentConfirmationInvalid
	}

	actualAmount, err := decimal.NewFromString(strings.TrimSpace(confirmation.Amount))
	if err != nil || actualAmount.LessThanOrEqual(decimal.Zero) {
		return ErrPaymentConfirmationInvalid
	}
	expected := decimal.NewFromFloat(expectedAmount).Round(confirmation.DecimalPlaces)
	if confirmation.UseGoFloatRounding {
		expectedText := strconv.FormatFloat(expectedAmount, 'f', int(confirmation.DecimalPlaces), 64)
		expected, err = decimal.NewFromString(expectedText)
		if err != nil {
			return ErrPaymentConfirmationInvalid
		}
	}
	if confirmation.AllowOverpayment {
		if actualAmount.LessThan(expected) {
			return ErrPaymentAmountMismatch
		}
	} else if !actualAmount.Equal(expected) {
		return ErrPaymentAmountMismatch
	}

	expectedCurrency := strings.ToUpper(strings.TrimSpace(confirmation.ExpectedCurrency))
	if expectedCurrency == "" {
		return nil
	}
	actualCurrency := strings.ToUpper(strings.TrimSpace(confirmation.Currency))
	if actualCurrency == "" || actualCurrency != expectedCurrency {
		return ErrPaymentCurrencyMismatch
	}
	return nil
}

// User.Quota is explicitly stored as SQL INT on both MySQL and PostgreSQL.
// Keep validation aligned with that schema even on 64-bit Go and SQLite tests.
const maxStoredUserQuota = int64(math.MaxInt32)

// MaxStoredUserQuota returns the maximum value the users.quota column can
// hold (SQL INT / MaxInt32). Controller pre-payment checks must use this exact
// ceiling: Nexus accepts a final balance of MaxInt32, one higher than the
// upstream MaxQuota-1 boundary. The atomic settlement guard uses the same
// constant, so pre-checks and credits never disagree on what can be stored.
func MaxStoredUserQuota() int {
	return int(maxStoredUserQuota)
}

// TopUpQuotaFromAmount, TopUpQuotaFromMoney and DirectTopUpQuota expose the
// exact conversions used by the settlement path in model/topup.go. Controllers
// must reuse them instead of re-implementing the math, so quotes, stored order
// amounts and callback credits always resolve to the same quota.
func TopUpQuotaFromAmount(amount int64) (int, error) {
	return topUpQuotaFromAmount(amount)
}

func TopUpQuotaFromMoney(amount float64) (int, error) {
	return topUpQuotaFromMoney(amount)
}

func DirectTopUpQuota(amount int64) (int, error) {
	return directTopUpQuota(amount)
}

// TopUpQuotaPerUnit validates the configuration before any decimal conversion
// or division. Quotes, order normalization and settlement share this guard.
func TopUpQuotaPerUnit() (decimal.Decimal, error) {
	qpu := common.QuotaPerUnit
	if qpu <= 0 || math.IsNaN(qpu) || math.IsInf(qpu, 0) {
		return decimal.Zero, ErrInvalidTopUpQuota
	}
	return decimal.NewFromFloat(qpu), nil
}

func topUpQuotaFromAmount(amount int64) (int, error) {
	qpu, err := TopUpQuotaPerUnit()
	if amount <= 0 || err != nil {
		return 0, ErrInvalidTopUpQuota
	}
	return checkedTopUpQuota(decimal.NewFromInt(amount).Mul(qpu))
}

func topUpQuotaFromMoney(amount float64) (int, error) {
	qpu, err := TopUpQuotaPerUnit()
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || err != nil {
		return 0, ErrInvalidTopUpQuota
	}
	return checkedTopUpQuota(decimal.NewFromFloat(amount).Mul(qpu))
}

func checkedTopUpQuota(quota decimal.Decimal) (int, error) {
	// A positive fraction that truncates to zero cannot be credited. Check
	// before IntPart, together with the inclusive SQL INT upper bound.
	if quota.LessThan(decimal.NewFromInt(1)) || quota.GreaterThan(decimal.NewFromInt(maxStoredUserQuota)) {
		return 0, ErrTopUpQuotaOutOfRange
	}
	return int(quota.IntPart()), nil
}

func directTopUpQuota(amount int64) (int, error) {
	if amount <= 0 || amount > maxStoredUserQuota {
		return 0, ErrInvalidTopUpQuota
	}
	return int(amount), nil
}

// ValidateTopUpQuotaCapacity is the advisory pre-payment check used by the
// controllers. It reads the current wallet balance from the database (never
// from Redis) and rejects orders whose credit could not be stored: the final
// balance must stay within the users.quota column range. The settlement path
// repeats the same invariant atomically, because the balance can change after
// the payment link or local order is created.
func ValidateTopUpQuotaCapacity(userID int, creditedQuota int) error {
	if creditedQuota <= 0 || int64(creditedQuota) > maxStoredUserQuota {
		return ErrInvalidTopUpQuota
	}
	maxExistingQuota := maxStoredUserQuota - int64(creditedQuota)

	var user User
	if err := DB.Select("COALESCE(quota, 0) AS quota").Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTopUpUserNotFound
		}
		return err
	}
	if int64(user.Quota) > maxExistingQuota {
		return ErrTopUpQuotaLimitExceeded
	}
	return nil
}

func addUserQuotaTx(tx *gorm.DB, userID int, quota int) error {
	if tx == nil || userID <= 0 || quota <= 0 {
		return ErrInvalidTopUpQuota
	}
	maxExistingQuota := maxStoredUserQuota - int64(quota)
	result := tx.Model(&User{}).
		Where("id = ? AND COALESCE(quota, 0) <= ?", userID, maxExistingQuota).
		Update("quota", gorm.Expr("COALESCE(quota, 0) + ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		// The conditional update can miss either because the user row does
		// not exist or because adding the quota would overflow the wallet.
		// Count inside the same transaction to keep the two cases distinct.
		var count int64
		if err := tx.Model(&User{}).Where("id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrTopUpUserNotFound
		}
		return ErrTopUpQuotaLimitExceeded
	}
	return nil
}
