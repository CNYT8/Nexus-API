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

func topUpQuotaFromAmount(amount int64) (int, error) {
	if amount <= 0 || common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return 0, errors.New("无效的充值额度")
	}

	quota := decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	if quota.LessThanOrEqual(decimal.Zero) || quota.GreaterThan(decimal.NewFromInt(maxStoredUserQuota)) {
		return 0, errors.New("充值额度超出允许范围")
	}
	return int(quota.IntPart()), nil
}

func topUpQuotaFromMoney(amount float64) (int, error) {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) ||
		common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return 0, errors.New("无效的充值额度")
	}

	quota := decimal.NewFromFloat(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	if quota.LessThanOrEqual(decimal.Zero) || quota.GreaterThan(decimal.NewFromInt(maxStoredUserQuota)) {
		return 0, errors.New("充值额度超出允许范围")
	}
	return int(quota.IntPart()), nil
}

func directTopUpQuota(amount int64) (int, error) {
	if amount <= 0 || amount > maxStoredUserQuota {
		return 0, errors.New("无效的充值额度")
	}
	return int(amount), nil
}

func addUserQuotaTx(tx *gorm.DB, userID int, quota int) error {
	if tx == nil || userID <= 0 || quota <= 0 {
		return errors.New("无效的充值参数")
	}
	maxExistingQuota := maxStoredUserQuota - int64(quota)
	result := tx.Model(&User{}).
		Where("id = ? AND quota <= ?", userID, maxExistingQuota).
		Update("quota", gorm.Expr("quota + ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("充值用户不存在或额度超出允许范围")
	}
	return nil
}
