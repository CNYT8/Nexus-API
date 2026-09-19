package model

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaymentConfirmationValidate(t *testing.T) {
	testCases := []struct {
		name           string
		confirmation   PaymentConfirmation
		expectedAmount float64
		expectedError  error
	}{
		{
			name: "equivalent decimal formatting",
			confirmation: PaymentConfirmation{
				Amount:        "9.9",
				DecimalPlaces: 2,
			},
			expectedAmount: 9.90,
		},
		{
			name: "currency is case insensitive",
			confirmation: PaymentConfirmation{
				Amount:           "9.9900",
				Currency:         "usd",
				ExpectedCurrency: "USD",
				DecimalPlaces:    2,
			},
			expectedAmount: 9.99,
		},
		{
			name: "legacy go float rounding",
			confirmation: PaymentConfirmation{
				Amount:             "1.00",
				DecimalPlaces:      2,
				UseGoFloatRounding: true,
			},
			expectedAmount: 1.005,
		},
		{
			name: "amount mismatch",
			confirmation: PaymentConfirmation{
				Amount:        "9.98",
				DecimalPlaces: 2,
			},
			expectedAmount: 9.99,
			expectedError:  ErrPaymentAmountMismatch,
		},
		{
			name: "provider tax overpayment",
			confirmation: PaymentConfirmation{
				Amount:           "10.79",
				DecimalPlaces:    2,
				AllowOverpayment: true,
			},
			expectedAmount: 9.99,
		},
		{
			name: "tax mode still rejects underpayment",
			confirmation: PaymentConfirmation{
				Amount:           "9.98",
				DecimalPlaces:    2,
				AllowOverpayment: true,
			},
			expectedAmount: 9.99,
			expectedError:  ErrPaymentAmountMismatch,
		},
		{
			name: "malformed amount",
			confirmation: PaymentConfirmation{
				Amount:        "not-a-number",
				DecimalPlaces: 2,
			},
			expectedAmount: 9.99,
			expectedError:  ErrPaymentConfirmationInvalid,
		},
		{
			name: "negative amount",
			confirmation: PaymentConfirmation{
				Amount:        "-9.99",
				DecimalPlaces: 2,
			},
			expectedAmount: 9.99,
			expectedError:  ErrPaymentConfirmationInvalid,
		},
		{
			name: "zero amount",
			confirmation: PaymentConfirmation{
				Amount:        "0",
				DecimalPlaces: 2,
			},
			expectedAmount: 9.99,
			expectedError:  ErrPaymentConfirmationInvalid,
		},
		{
			name: "currency mismatch",
			confirmation: PaymentConfirmation{
				Amount:           "9.99",
				Currency:         "JPY",
				ExpectedCurrency: "USD",
				DecimalPlaces:    2,
			},
			expectedAmount: 9.99,
			expectedError:  ErrPaymentCurrencyMismatch,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.confirmation.Validate(testCase.expectedAmount)
			if testCase.expectedError == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, testCase.expectedError)
		})
	}
}

func TestRechargeEpayValidatesAmountAndIsIdempotent(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 501, 10)
	insertTopUpForPaymentGuardTest(t, "epay-confirmed", 501, PaymentProviderEpay)

	confirmation := PaymentConfirmation{Amount: "9.990", DecimalPlaces: 2}
	require.NoError(t, RechargeEpay("epay-confirmed", "alipay", "127.0.0.1", confirmation))
	assert.Equal(t, 210, getUserQuotaForPaymentGuardTest(t, 501))

	topUp := GetTopUpByTradeNo("epay-confirmed")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	assert.Equal(t, "alipay", topUp.PaymentMethod)

	require.NoError(t, RechargeEpay("epay-confirmed", "alipay", "127.0.0.1", confirmation))
	assert.Equal(t, 210, getUserQuotaForPaymentGuardTest(t, 501))
}

func TestRechargeEpayRejectsAmountMismatch(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 502, 10)
	insertTopUpForPaymentGuardTest(t, "epay-underpaid", 502, PaymentProviderEpay)

	err := RechargeEpay("epay-underpaid", "alipay", "127.0.0.1", PaymentConfirmation{
		Amount:        "0.01",
		DecimalPlaces: 2,
	})
	require.ErrorIs(t, err, ErrPaymentAmountMismatch)
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "epay-underpaid"))
	assert.Equal(t, 10, getUserQuotaForPaymentGuardTest(t, 502))
}

func TestRechargeEpayRejectsMismatchedPaymentProvider(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 503, 0)
	insertTopUpForPaymentGuardTest(t, "epay-provider-mismatch", 503, PaymentProviderStripe)

	err := RechargeEpay("epay-provider-mismatch", "alipay", "127.0.0.1", PaymentConfirmation{
		Amount:        "9.99",
		DecimalPlaces: 2,
	})
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "epay-provider-mismatch"))
	assert.Equal(t, 0, getUserQuotaForPaymentGuardTest(t, 503))
}

func TestRechargeEpayRejectsQuotaOverflow(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	maxInt := int(^uint(0) >> 1)
	insertUserForPaymentGuardTest(t, 504, maxInt-1)
	insertTopUpForPaymentGuardTest(t, "epay-quota-overflow", 504, PaymentProviderEpay)

	err := RechargeEpay("epay-quota-overflow", "alipay", "127.0.0.1", PaymentConfirmation{
		Amount:        "9.99",
		DecimalPlaces: 2,
	})
	require.Error(t, err)
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "epay-quota-overflow"))
	assert.Equal(t, maxInt-1, getUserQuotaForPaymentGuardTest(t, 504))
}

func TestRechargeStripeIsIdempotent(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 505, 0)
	insertTopUpForPaymentGuardTest(t, "stripe-idempotent", 505, PaymentProviderStripe)

	require.NoError(t, Recharge("stripe-idempotent", "cus_123", "127.0.0.1"))
	assert.Equal(t, 999, getUserQuotaForPaymentGuardTest(t, 505))
	require.NoError(t, Recharge("stripe-idempotent", "cus_123", "127.0.0.1"))
	assert.Equal(t, 999, getUserQuotaForPaymentGuardTest(t, 505))
}

func TestRechargeStripeReturnsNotFoundSentinel(t *testing.T) {
	truncateTables(t)

	err := Recharge("missing-stripe-order", "cus_123", "127.0.0.1")
	require.ErrorIs(t, err, ErrTopUpNotFound)
}

func TestRechargeGatewaysRejectAmountMismatch(t *testing.T) {
	testCases := []struct {
		name     string
		provider string
		recharge func(string, PaymentConfirmation) error
	}{
		{
			name:     "creem",
			provider: PaymentProviderCreem,
			recharge: func(tradeNo string, confirmation PaymentConfirmation) error {
				return RechargeCreem(tradeNo, "127.0.0.1", confirmation)
			},
		},
		{
			name:     "waffo",
			provider: PaymentProviderWaffo,
			recharge: func(tradeNo string, confirmation PaymentConfirmation) error {
				return RechargeWaffo(tradeNo, "127.0.0.1", confirmation)
			},
		},
		{
			name:     "waffo pancake",
			provider: PaymentProviderWaffoPancake,
			recharge: RechargeWaffoPancake,
		},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncateTables(t)
			userID := 600 + index
			tradeNo := "amount-mismatch-" + testCase.provider
			insertUserForPaymentGuardTest(t, userID, 0)
			insertTopUpForPaymentGuardTest(t, tradeNo, userID, testCase.provider)

			err := testCase.recharge(tradeNo, PaymentConfirmation{
				Amount:           "1.00",
				Currency:         "USD",
				ExpectedCurrency: "USD",
				DecimalPlaces:    2,
			})
			require.Error(t, err)
			assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, tradeNo))
			assert.Equal(t, 0, getUserQuotaForPaymentGuardTest(t, userID))
		})
	}
}

func TestRechargeCreemUsesDirectQuota(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 701, 0)
	insertTopUpForPaymentGuardTest(t, "creem-direct-quota", 701, PaymentProviderCreem)

	require.NoError(t, RechargeCreem("creem-direct-quota", "127.0.0.1", PaymentConfirmation{
		Amount:           "9.99",
		Currency:         "USD",
		ExpectedCurrency: "USD",
		DecimalPlaces:    2,
	}))
	assert.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, 701))
}

func TestManualCompleteCreemUsesDirectQuota(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 702, 0)
	insertTopUpForPaymentGuardTest(t, "creem-manual-direct-quota", 702, PaymentProviderCreem)

	require.NoError(t, ManualCompleteTopUp("creem-manual-direct-quota", "127.0.0.1"))
	assert.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, 702))
}

func TestManualCompleteLegacyCreemOrderUsesDirectQuota(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 703, 0)
	insertTopUpForPaymentGuardTest(t, "legacy-creem-manual-direct-quota", 703, PaymentProviderCreem)
	require.NoError(t, DB.Model(&TopUp{}).
		Where("trade_no = ?", "legacy-creem-manual-direct-quota").
		Update("payment_provider", "").Error)

	require.NoError(t, ManualCompleteTopUp("legacy-creem-manual-direct-quota", "127.0.0.1"))
	assert.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, 703))
}

func TestCompleteSubscriptionOrderValidatesConfirmation(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 801, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 802)
	insertSubscriptionOrderForPaymentGuardTest(t, "subscription-confirmation", 801, plan.Id, PaymentProviderCreem)

	err := CompleteSubscriptionOrderWithConfirmation(
		"subscription-confirmation",
		`{"provider":"creem"}`,
		PaymentProviderCreem,
		PaymentMethodCreem,
		PaymentConfirmation{
			Amount:           "1.00",
			Currency:         "USD",
			ExpectedCurrency: "USD",
			DecimalPlaces:    2,
		},
	)
	require.ErrorIs(t, err, ErrPaymentAmountMismatch)
	assert.Equal(t, common.TopUpStatusPending, GetSubscriptionOrderByTradeNo("subscription-confirmation").Status)
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 801))
	assert.Nil(t, GetTopUpByTradeNo("subscription-confirmation"))

	err = CompleteSubscriptionOrderWithConfirmation(
		"subscription-confirmation",
		`{"provider":"creem"}`,
		PaymentProviderCreem,
		PaymentMethodCreem,
		PaymentConfirmation{
			Amount:           "9.990",
			Currency:         "usd",
			ExpectedCurrency: "USD",
			DecimalPlaces:    2,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, common.TopUpStatusSuccess, GetSubscriptionOrderByTradeNo("subscription-confirmation").Status)
	assert.Equal(t, int64(1), countUserSubscriptionsForPaymentGuardTest(t, 801))
	topUp := GetTopUpByTradeNo("subscription-confirmation")
	require.NotNil(t, topUp)
	assert.Equal(t, PaymentProviderCreem, topUp.PaymentProvider)
}

func TestCompleteSubscriptionOrderRejectsCrossUserTopUpCollision(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 901, 0)
	insertUserForPaymentGuardTest(t, 902, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 903)
	insertSubscriptionOrderForPaymentGuardTest(t, "subscription-user-collision", 901, plan.Id, PaymentProviderCreem)
	insertTopUpForPaymentGuardTest(t, "subscription-user-collision", 902, PaymentProviderCreem)

	err := CompleteSubscriptionOrderWithConfirmation(
		"subscription-user-collision",
		`{"provider":"creem"}`,
		PaymentProviderCreem,
		PaymentMethodCreem,
		PaymentConfirmation{
			Amount:           "9.99",
			Currency:         "USD",
			ExpectedCurrency: "USD",
			DecimalPlaces:    2,
		},
	)
	require.ErrorIs(t, err, ErrPaymentOrderMismatch)
	assert.Equal(t, common.TopUpStatusPending, GetSubscriptionOrderByTradeNo("subscription-user-collision").Status)
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 901))
	topUp := GetTopUpByTradeNo("subscription-user-collision")
	require.NotNil(t, topUp)
	assert.Equal(t, 902, topUp.UserId)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}

func TestRechargeEpayConcurrentCallbacksCreditOnce(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertUserForPaymentGuardTest(t, 1001, 10)
	insertTopUpForPaymentGuardTest(t, "epay-concurrent", 1001, PaymentProviderEpay)
	confirmation := PaymentConfirmation{Amount: "9.99", DecimalPlaces: 2}

	const callbackCount = 32
	start := make(chan struct{})
	errorsCh := make(chan error, callbackCount)
	var wg sync.WaitGroup
	for i := 0; i < callbackCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errorsCh <- RechargeEpay("epay-concurrent", "alipay", "127.0.0.1", confirmation)
		}()
	}
	close(start)
	wg.Wait()
	close(errorsCh)

	for err := range errorsCh {
		require.NoError(t, err)
	}
	assert.Equal(t, 210, getUserQuotaForPaymentGuardTest(t, 1001))
	assert.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, "epay-concurrent"))
}

func TestRechargeRollsBackWhenUserCannotBeCredited(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertTopUpForPaymentGuardTest(t, "epay-missing-user", 1101, PaymentProviderEpay)
	err := RechargeEpay("epay-missing-user", "alipay", "127.0.0.1", PaymentConfirmation{
		Amount:        "9.99",
		DecimalPlaces: 2,
	})
	require.ErrorIs(t, err, ErrTopUpUserNotFound)
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "epay-missing-user"))
}

func TestPaymentQuotaUsesDatabaseIntWidth(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	quota, err := topUpQuotaFromAmount(maxStoredUserQuota)
	require.NoError(t, err)
	assert.Equal(t, int(maxStoredUserQuota), quota)

	_, err = topUpQuotaFromAmount(maxStoredUserQuota + 1)
	require.Error(t, err)
	_, err = directTopUpQuota(maxStoredUserQuota + 1)
	require.Error(t, err)
}

func TestTopUpQuotaNumericSafety(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	for _, qpu := range []float64{-1, 0, math.NaN(), math.Inf(1), math.Inf(-1)} {
		common.QuotaPerUnit = qpu
		_, err := TopUpQuotaFromAmount(1)
		require.ErrorIs(t, err, ErrInvalidTopUpQuota)
		_, err = TopUpQuotaFromMoney(1)
		require.ErrorIs(t, err, ErrInvalidTopUpQuota)
		quota, err := DirectTopUpQuota(math.MaxInt32)
		require.NoError(t, err) // Creem never depends on QPU.
		require.Equal(t, math.MaxInt32, quota)
	}
	for _, qpu := range []float64{0.5, 1e-20, math.SmallestNonzeroFloat64} {
		common.QuotaPerUnit = qpu
		quota, err := TopUpQuotaFromAmount(1)
		require.ErrorIs(t, err, ErrTopUpQuotaOutOfRange)
		require.Zero(t, quota)
		quota, err = TopUpQuotaFromMoney(1)
		require.ErrorIs(t, err, ErrTopUpQuotaOutOfRange)
		require.Zero(t, quota)
	}
	common.QuotaPerUnit = 1
	for _, amount := range []float64{-1, 0, math.NaN(), math.Inf(1), math.Inf(-1), math.MaxFloat64, 0x1p63, math.MaxInt32 + 0.5} {
		_, err := TopUpQuotaFromMoney(amount)
		require.Error(t, err)
	}
	for _, amount := range []int64{math.MinInt64, -1, 0, math.MaxInt32 + 1, math.MaxInt64} {
		_, err := TopUpQuotaFromAmount(amount)
		require.Error(t, err)
		_, err = DirectTopUpQuota(amount)
		require.Error(t, err)
	}
	for _, amount := range []float64{1, 1.9, math.MaxInt32} {
		quota, err := TopUpQuotaFromMoney(amount)
		require.NoError(t, err)
		require.Equal(t, int(amount), quota)
	}
	common.QuotaPerUnit = math.MaxFloat64
	_, err := TopUpQuotaFromAmount(math.MaxInt64)
	require.ErrorIs(t, err, ErrTopUpQuotaOutOfRange)
	_, err = TopUpQuotaFromMoney(math.MaxFloat64)
	require.ErrorIs(t, err, ErrTopUpQuotaOutOfRange)
}

func TestPaymentQuotaAllSettlementEntrypoints(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	confirmation := PaymentConfirmation{Amount: "9.99", DecimalPlaces: 2}
	for _, provider := range []string{PaymentProviderEpay, PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake} {
		for _, manual := range []bool{false, true} {
			for _, qpu := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), 0.01, math.SmallestNonzeroFloat64, math.MaxFloat64, 1} {
				t.Run(fmt.Sprintf("%s/manual=%t/qpu=%g", provider, manual, qpu), func(t *testing.T) {
					truncateTables(t)
					common.QuotaPerUnit = qpu
					const userID = 1301
					credit := 2
					if provider == PaymentProviderStripe {
						credit = 9 // Money=9.99, not Amount=2.
					}
					initial := math.MaxInt32 - credit
					insertUserForPaymentGuardTest(t, userID, initial)
					insertTopUpForPaymentGuardTest(t, "numeric-settlement", userID, provider)
					recharge := func() error {
						if manual {
							return ManualCompleteTopUp("numeric-settlement", "127.0.0.1")
						}
						switch provider {
						case PaymentProviderEpay:
							return RechargeEpay("numeric-settlement", "alipay", "127.0.0.1", confirmation)
						case PaymentProviderStripe:
							return Recharge("numeric-settlement", "cus_test", "127.0.0.1")
						case PaymentProviderCreem:
							return RechargeCreem("numeric-settlement", "127.0.0.1", confirmation)
						case PaymentProviderWaffo:
							return RechargeWaffo("numeric-settlement", "127.0.0.1", confirmation)
						default:
							return RechargeWaffoPancake("numeric-settlement", confirmation)
						}
					}
					if qpu == 1 || provider == PaymentProviderCreem {
						require.NoError(t, recharge())
						require.NoError(t, recharge())
						require.Equal(t, math.MaxInt32, getUserQuotaForPaymentGuardTest(t, userID))
						require.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, "numeric-settlement"))
					} else {
						require.Error(t, recharge())
						require.Equal(t, initial, getUserQuotaForPaymentGuardTest(t, userID))
						require.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "numeric-settlement"))
					}
				})
			}
		}
	}
}

func TestTopUpQuotaExportedWrappers(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 2
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	assert.Equal(t, int(maxStoredUserQuota), MaxStoredUserQuota())

	quota, err := TopUpQuotaFromAmount(3)
	require.NoError(t, err)
	assert.Equal(t, 6, quota)

	quota, err = TopUpQuotaFromMoney(3.5)
	require.NoError(t, err)
	assert.Equal(t, 7, quota)

	quota, err = DirectTopUpQuota(9)
	require.NoError(t, err)
	assert.Equal(t, 9, quota)

	_, err = TopUpQuotaFromAmount(0)
	require.ErrorIs(t, err, ErrInvalidTopUpQuota)
	_, err = TopUpQuotaFromMoney(0)
	require.ErrorIs(t, err, ErrInvalidTopUpQuota)
	_, err = DirectTopUpQuota(0)
	require.ErrorIs(t, err, ErrInvalidTopUpQuota)
	_, err = TopUpQuotaFromAmount(int64(maxStoredUserQuota) + 1)
	require.ErrorIs(t, err, ErrTopUpQuotaOutOfRange)
	_, err = DirectTopUpQuota(int64(maxStoredUserQuota) + 1)
	require.ErrorIs(t, err, ErrInvalidTopUpQuota)
}

func TestValidateTopUpQuotaCapacityBoundaries(t *testing.T) {
	truncateTables(t)

	const creditedQuota = 1_000_000
	userID := 1201
	insertUserForPaymentGuardTest(t, userID, int(maxStoredUserQuota)-creditedQuota)

	// The final balance may reach exactly MaxInt32, matching addUserQuotaTx.
	require.NoError(t, ValidateTopUpQuotaCapacity(userID, creditedQuota))

	require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).
		Update("quota", int(maxStoredUserQuota)-creditedQuota+1).Error)
	require.ErrorIs(t, ValidateTopUpQuotaCapacity(userID, creditedQuota), ErrTopUpQuotaLimitExceeded)
}

func TestValidateTopUpQuotaCapacityRejectsInvalidQuota(t *testing.T) {
	truncateTables(t)

	userID := 1202
	insertUserForPaymentGuardTest(t, userID, 0)

	for _, creditedQuota := range []int{-1, 0, int(maxStoredUserQuota) + 1} {
		require.ErrorIs(t, ValidateTopUpQuotaCapacity(userID, creditedQuota), ErrInvalidTopUpQuota)
	}
}

func TestValidateTopUpQuotaCapacityUserNotFound(t *testing.T) {
	truncateTables(t)

	require.ErrorIs(t, ValidateTopUpQuotaCapacity(1203, 100), ErrTopUpUserNotFound)
}

func TestRechargeEpayEnforcesFinalWalletQuotaLimit(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	// The helper stores Amount=2, so every callback credits exactly 2 quota.
	confirmation := PaymentConfirmation{Amount: "9.99", DecimalPlaces: 2}

	t.Run("allows exact highest wallet balance", func(t *testing.T) {
		truncateTables(t)
		insertUserForPaymentGuardTest(t, 1204, int(maxStoredUserQuota)-2)
		insertTopUpForPaymentGuardTest(t, "epay-wallet-exact", 1204, PaymentProviderEpay)

		require.NoError(t, RechargeEpay("epay-wallet-exact", "alipay", "127.0.0.1", confirmation))
		assert.Equal(t, int(maxStoredUserQuota), getUserQuotaForPaymentGuardTest(t, 1204))
		assert.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, "epay-wallet-exact"))
	})

	t.Run("rejects wallet overflow and keeps order pending", func(t *testing.T) {
		truncateTables(t)
		insertUserForPaymentGuardTest(t, 1205, int(maxStoredUserQuota)-1)
		insertTopUpForPaymentGuardTest(t, "epay-wallet-over-limit", 1205, PaymentProviderEpay)

		err := RechargeEpay("epay-wallet-over-limit", "alipay", "127.0.0.1", confirmation)
		require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)
		assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "epay-wallet-over-limit"))
		assert.Equal(t, int(maxStoredUserQuota)-1, getUserQuotaForPaymentGuardTest(t, 1205))
	})
}
