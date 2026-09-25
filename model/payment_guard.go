package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

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

// maxStoredUserQuota is the safe fallback for old schemas and SQLite tests.
// Existing MySQL/PostgreSQL installations may have a BIGINT quota column even
// though the current struct tag says INT, so capacity checks below inspect the
// actual database column instead of rejecting already-valid historical values.
const maxStoredUserQuota = int64(math.MaxInt32)

const maxSignedStoredUserQuota = int64(math.MaxInt64)

var quotaLimitCache sync.Map

func quotaLimitForColumnType(dialect, databaseTypeName, columnType string) (int64, error) {
	if dialect == "sqlite" {
		// Keep SQLite's established Nexus quota boundary. SQLite's INTEGER is
		// wider, but quota calculations and test fixtures intentionally use the
		// same signed 32-bit safety boundary as the legacy schema.
		return maxStoredUserQuota, nil
	}

	typeName := strings.ToLower(strings.TrimSpace(databaseTypeName + " " + columnType))
	unsigned := strings.Contains(typeName, "unsigned")
	switch {
	case strings.Contains(typeName, "bigint"), strings.Contains(typeName, "int8"), strings.Contains(typeName, "serial8"):
		// Go's int is signed; an UNSIGNED BIGINT value above MaxInt64 cannot
		// be represented by User.Quota and must therefore remain rejected.
		return maxSignedStoredUserQuota, nil
	case strings.Contains(typeName, "mediumint"):
		if unsigned {
			return 16777215, nil
		}
		return 8388607, nil
	case strings.Contains(typeName, "smallint"):
		if unsigned {
			return 65535, nil
		}
		return 32767, nil
	case strings.Contains(typeName, "tinyint"):
		if unsigned {
			return 255, nil
		}
		return 127, nil
	case strings.Contains(typeName, "integer"), strings.Contains(typeName, "int4"), strings.Contains(typeName, "serial4"):
		return maxStoredUserQuota, nil
	case strings.Contains(typeName, "int2"), strings.Contains(typeName, "serial2"):
		return 32767, nil
	case strings.Contains(typeName, "int"):
		if unsigned {
			return 4294967295, nil
		}
		return maxStoredUserQuota, nil
	default:
		return 0, fmt.Errorf("unsupported users.quota database type %q", strings.TrimSpace(databaseTypeName+" "+columnType))
	}
}

func storedUserQuotaLimit(db *gorm.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("database is not initialized")
	}
	key := fmt.Sprintf("%p", db.ConnPool)
	if cached, ok := quotaLimitCache.Load(key); ok {
		return cached.(int64), nil
	}
	if db.Dialector.Name() == "sqlite" {
		quotaLimitCache.Store(key, maxStoredUserQuota)
		return maxStoredUserQuota, nil
	}
	columnTypes, err := db.Migrator().ColumnTypes(&User{})
	if err != nil {
		return 0, err
	}
	for _, column := range columnTypes {
		if !strings.EqualFold(column.Name(), "quota") {
			continue
		}
		columnType, _ := column.ColumnType()
		limit, err := quotaLimitForColumnType(db.Dialector.Name(), column.DatabaseTypeName(), columnType)
		if err != nil {
			return 0, err
		}
		quotaLimitCache.Store(key, limit)
		return limit, nil
	}
	return 0, errors.New("users.quota column was not found")
}

// InitializeStoredUserQuotaLimit reads the actual main-database quota column
// after migrations and before request transactions begin. This is important
// for MySQL BIGINT installations: introspecting the schema from inside an
// open transaction can wait on the same pool connection.
func InitializeStoredUserQuotaLimit() error {
	_, err := storedUserQuotaLimit(DB)
	return err
}

// MaxStoredUserQuota remains the legacy per-credit safety bound used by
// quotes and conversion helpers. Final wallet capacity checks use the actual
// database column through storedUserQuotaLimit instead.
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
	// A positive fraction that truncates to zero cannot be credited. Keep the
	// per-credit conversion bound stable; the final wallet capacity is checked
	// separately against the actual database column.
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
	if creditedQuota <= 0 {
		return ErrInvalidTopUpQuota
	}
	storedLimit, err := storedUserQuotaLimit(DB)
	if err != nil {
		return err
	}
	if int64(creditedQuota) > storedLimit {
		return ErrInvalidTopUpQuota
	}
	maxExistingQuota := storedLimit - int64(creditedQuota)

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
	storedLimit, err := storedUserQuotaLimit(DB)
	if err != nil {
		return err
	}
	if int64(quota) > storedLimit {
		return ErrTopUpQuotaOutOfRange
	}
	maxExistingQuota := storedLimit - int64(quota)
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
