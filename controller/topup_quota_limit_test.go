package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"gorm.io/gorm"
)

type topUpQuotaTestEnv struct {
	db *gorm.DB
}

// setupTopUpQuotaTestEnv builds an isolated in-memory database and restores the
// global model.DB/model.LOG_DB and common flags afterwards so other controller
// tests keep seeing their own setup.
func setupTopUpQuotaTestEnv(t *testing.T) *topUpQuotaTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	oldUsingPostgreSQL := common.UsingPostgreSQL
	oldRedisEnabled := common.RedisEnabled
	oldQuotaPerUnit := common.QuotaPerUnit
	oldIsMasterNode := common.IsMasterNode
	oldSQLitePath := common.SQLitePath
	oldSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")

	// GetUserGroup depends on the shared quoted group column initialized by
	// InitDB/initCol. Initialize it with a throwaway local database once, then
	// close that connection; the test uses its own isolated in-memory DB.
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s_cols?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	require.NoError(t, model.InitDB())
	if initDB := model.DB; initDB != nil {
		if sqlDB, dbErr := initDB.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	}

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.QuotaPerUnit = 500000

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		common.UsingPostgreSQL = oldUsingPostgreSQL
		common.RedisEnabled = oldRedisEnabled
		common.QuotaPerUnit = oldQuotaPerUnit
		common.IsMasterNode = oldIsMasterNode
		common.SQLitePath = oldSQLitePath
		if hadSQLDSN {
			_ = os.Setenv("SQL_DSN", oldSQLDSN)
		} else {
			_ = os.Unsetenv("SQL_DSN")
		}
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	return &topUpQuotaTestEnv{db: db}
}

func (env *topUpQuotaTestEnv) insertUser(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, env.db.Create(&model.User{
		Id:       id,
		Username: fmt.Sprintf("topup_quota_%d", id),
		Status:   common.UserStatusEnabled,
		Quota:    quota,
		AffCode:  fmt.Sprintf("topup_quota_aff_%d", id),
		Group:    "default",
	}).Error)
}

func (env *topUpQuotaTestEnv) countTopUps(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, env.db.Model(&model.TopUp{}).Count(&count).Error)
	return count
}

func buildTopUpTestContext(t *testing.T, path string, body string, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		ctx.Set("id", userID)
	}
	return ctx, recorder
}

func setTopUpTestValue[T any](t *testing.T, target *T, value T) {
	t.Helper()
	old := *target
	*target = value
	t.Cleanup(func() { *target = old })
}

func setupTopUpGatewaySettings(t *testing.T) {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	setTopUpTestValue(t, &operation_setting.GetGeneralSetting().QuotaDisplayType, operation_setting.QuotaDisplayTypeUSD)
	setTopUpTestValue(t, &operation_setting.MinTopUp, 0)
	setTopUpTestValue(t, &setting.StripeMinTopUp, 0)
	setTopUpTestValue(t, &setting.WaffoMinTopUp, 0)
	setTopUpTestValue(t, &setting.WaffoPancakeMinTopUp, 0)
	setTopUpTestValue(t, &operation_setting.Price, 1.0)
	setTopUpTestValue(t, &setting.StripeUnitPrice, 1.0)
	setTopUpTestValue(t, &setting.WaffoUnitPrice, 1.0)
	setTopUpTestValue(t, &setting.WaffoPancakeUnitPrice, 1.0)
	setTopUpTestValue(t, &operation_setting.GetPaymentSetting().AmountDiscount, map[int]float64{})
	setTopUpTestValue(t, &setting.WaffoEnabled, true)
	setTopUpTestValue(t, &setting.WaffoPancakeMerchantID, "test-merchant")
	setTopUpTestValue(t, &setting.WaffoPancakePrivateKey, "test-private")
	setTopUpTestValue(t, &setting.WaffoPancakeProductID, "test-product")
	setTopUpTestValue(t, &setting.StripeApiSecret, "sk_test_local")
	setTopUpTestValue(t, &setting.CreemApiKey, "test-key")
	setTopUpTestValue(t, &operation_setting.PayAddress, "https://pay.example.com")
	setTopUpTestValue(t, &operation_setting.EpayId, "test-id")
	setTopUpTestValue(t, &operation_setting.EpayKey, "test-key")
	setTopUpTestValue(t, &operation_setting.PayMethods, []map[string]string{{"type": "alipay"}})
	oldRatios := common.TopupGroupRatio2JSONString()
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() { require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldRatios)) })
}

type topUpTestEndpoint struct {
	name    string
	handler gin.HandlerFunc
}

func topUpAmountEndpoints() []topUpTestEndpoint {
	return []topUpTestEndpoint{
		{"epay quote", RequestAmount}, {"epay pay", RequestEpay},
		{"stripe quote", RequestStripeAmount}, {"stripe pay", RequestStripePay},
		{"waffo quote", RequestWaffoAmount}, {"waffo pay", RequestWaffoPay},
		{"pancake quote", RequestWaffoPancakeAmount}, {"pancake pay", RequestWaffoPancakePay},
	}
}

func topUpEndpointBody(endpoint topUpTestEndpoint, amount string) string {
	method := "alipay"
	if strings.HasPrefix(endpoint.name, "stripe") {
		method = "stripe"
	}
	return fmt.Sprintf(`{"amount":%s,"payment_method":%q}`, amount, method)
}

func TestTopUpEntrypointsRejectUnsafeNumbers(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9100, 0)
	for _, display := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeCNY, operation_setting.QuotaDisplayTypeTokens} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		for _, tc := range []struct {
			name, amount string
			qpu          float64
		}{
			{"zero QPU", "1", 0}, {"negative QPU", "1", -1},
			{"NaN QPU", "1", math.NaN()}, {"positive Inf QPU", "1", math.Inf(1)}, {"negative Inf QPU", "1", math.Inf(-1)},
			{"zero input", "0", 1}, {"negative input", "-1", 1}, {"min int", "-9223372036854775808", 1},
			{"max int", "9223372036854775807", 1}, {"int overflow", "9223372036854775808", 1},
			{"quota overflow", "2147483648", 1}, {"NaN input", "NaN", 1}, {"Inf input", "1e999", 1},
			{"fraction input", "0.5", 1}, {"tiny QPU", "1", 1e-20}, {"subnormal QPU", "1", math.SmallestNonzeroFloat64},
			{"huge QPU", "1", math.MaxFloat64},
		} {
			common.QuotaPerUnit = tc.qpu
			for _, endpoint := range topUpAmountEndpoints() {
				t.Run(display+"/"+tc.name+"/"+endpoint.name, func(t *testing.T) {
					ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, tc.amount), 9100)
					require.NotPanics(t, func() { endpoint.handler(ctx) })
					require.Equal(t, http.StatusOK, recorder.Code)
					var response struct {
						Message string `json:"message"`
					}
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					require.NotEmpty(t, response.Message)
					require.NotEqual(t, "success", response.Message)
					require.Zero(t, env.countTopUps(t))
				})
			}
		}
	}
}

func TestTopUpEntrypointsRejectFractionalZeroCredit(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9101, 0)
	common.QuotaPerUnit = 0.5
	for _, endpoint := range topUpAmountEndpoints() {
		t.Run(endpoint.name, func(t *testing.T) {
			ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, "1"), 9101)
			endpoint.handler(ctx)
			require.JSONEq(t, `{"message":"error","data":"充值额度超出允许范围"}`, recorder.Body.String())
		})
	}
	require.Zero(t, env.countTopUps(t))
}

func TestTopUpNormalizationAndMinimumBoundaries(t *testing.T) {
	setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	for name, normalize := range map[string]func(int64) (int64, error){"epay": normalizeEpayTopUpAmount, "waffo": normalizeWaffoTopUpAmount, "pancake": normalizeWaffoPancakeTopUpAmount} {
		t.Run(name, func(t *testing.T) {
			for _, qpu := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), 1e-20, math.SmallestNonzeroFloat64} {
				common.QuotaPerUnit = qpu
				_, err := normalize(1)
				require.Error(t, err)
			}
			common.QuotaPerUnit = 2.5
			value, err := normalize(12)
			require.NoError(t, err)
			require.EqualValues(t, 4, value)
			value, err = normalize(1)
			require.NoError(t, err)
			if name == "epay" {
				require.Zero(t, value)
			} else {
				require.EqualValues(t, 1, value)
			}
			for _, amount := range []int64{0, -1, math.MinInt64} {
				_, err = normalize(amount)
				require.Error(t, err)
			}
		})
	}
	common.QuotaPerUnit = 2.5
	operation_setting.MinTopUp, setting.StripeMinTopUp = 3, 3
	minimum, err := getMinTopup()
	require.NoError(t, err)
	require.EqualValues(t, 7, minimum)
	minimum, err = getStripeMinTopup()
	require.NoError(t, err)
	require.EqualValues(t, 6, minimum) // Stripe truncates QPU first.
	for _, qpu := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), math.MaxFloat64} {
		common.QuotaPerUnit = qpu
		_, err := getMinTopup()
		require.Error(t, err)
		_, err = getStripeMinTopup()
		require.Error(t, err)
	}
	for _, qpu := range []float64{1e-20, math.SmallestNonzeroFloat64} {
		common.QuotaPerUnit = qpu
		require.EqualValues(t, math.MaxInt64, maxTopUpAmount())
	}
	common.QuotaPerUnit = 1
	require.EqualValues(t, math.MaxInt32, maxTopUpAmount())
	// Decimal IntPart may return MaxInt64, but the float gateway must reject
	// the rounded 2^63 rather than wrapping and clamping it to 1.
	value, err := normalizeEpayTopUpAmount(math.MaxInt64)
	require.NoError(t, err)
	require.EqualValues(t, math.MaxInt64, value)
	_, err = normalizeWaffoTopUpAmount(math.MaxInt64)
	require.Error(t, err)
}

func TestTopUpEntrypointsMinimumConfiguration(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9102, 0)
	for _, minimum := range []int{-1, int(^uint(0) >> 1)} {
		operation_setting.MinTopUp, setting.StripeMinTopUp = minimum, minimum
		setting.WaffoMinTopUp, setting.WaffoPancakeMinTopUp = minimum, minimum
		operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
		common.QuotaPerUnit = 500000
		for _, endpoint := range topUpAmountEndpoints() {
			ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, "100"), 9102)
			require.NotPanics(t, func() { endpoint.handler(ctx) })
			require.NotContains(t, recorder.Body.String(), `"success"`)
			require.Zero(t, env.countTopUps(t))
		}
	}
}

func TestTopUpPricingRejectsAbnormalConfiguration(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9103, 0)
	common.QuotaPerUnit = 1
	for _, invalid := range []float64{-1, 0, math.NaN(), math.Inf(1), math.Inf(-1), math.MaxFloat64} {
		operation_setting.Price, setting.StripeUnitPrice = invalid, invalid
		setting.WaffoUnitPrice, setting.WaffoPancakeUnitPrice = invalid, invalid
		for _, endpoint := range topUpAmountEndpoints() {
			ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, "10"), 9103)
			require.NotPanics(t, func() { endpoint.handler(ctx) })
			require.JSONEq(t, `{"message":"error","data":"充值校验失败，请稍后重试"}`, recorder.Body.String())
			require.Zero(t, env.countTopUps(t))
		}
	}
	operation_setting.Price, setting.StripeUnitPrice = 1, 1
	setting.WaffoUnitPrice, setting.WaffoPancakeUnitPrice = 1, 1
	for _, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.MaxFloat64} {
		operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{10: invalid}
		for _, endpoint := range topUpAmountEndpoints() {
			ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, "10"), 9103)
			require.NotPanics(t, func() { endpoint.handler(ctx) })
			require.Contains(t, recorder.Body.String(), `"message":"error"`)
			require.Zero(t, env.countTopUps(t))
		}
	}
}

func TestTopUpErrorsArePublicAndLogged(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9104, 0)
	var logs bytes.Buffer
	setTopUpTestValue[io.Writer](t, &gin.DefaultErrorWriter, &logs)
	const secret = "private-dsn-password SELECT quota FROM users"
	for _, err := range []error{errors.New(secret), fmt.Errorf("%s: %w", secret, model.ErrTopUpQuotaLimitExceeded)} {
		ctx, recorder := buildTopUpTestContext(t, "/topup-test", `{}`, 9104)
		require.True(t, rejectTopUpQuota(ctx, err))
		require.NotContains(t, recorder.Body.String(), secret)
		require.Contains(t, logs.String(), secret)
		logs.Reset()
	}
	// Inject a real query failure at the capacity query, not the earlier user
	// or group lookups, so every actual payment endpoint reaches this guard.
	require.NoError(t, env.db.Callback().Query().Before("gorm:query").Register("topup_test_failure", func(tx *gorm.DB) {
		if len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "COALESCE(quota, 0) AS quota" {
			tx.AddError(errors.New(secret))
		}
	}))
	t.Cleanup(func() { _ = env.db.Callback().Query().Remove("topup_test_failure") })
	setTopUpTestValue(t, &setting.CreemProducts, `[{"productId":"test","price":10,"quota":10}]`)
	endpoints := append(topUpAmountEndpoints(), topUpTestEndpoint{"creem pay", RequestCreemPay})
	for _, endpoint := range endpoints {
		body := topUpEndpointBody(endpoint, "10")
		if endpoint.name == "creem pay" {
			body = `{"product_id":"test","payment_method":"creem"}`
		}
		ctx, recorder := buildTopUpTestContext(t, "/topup-test", body, 9104)
		endpoint.handler(ctx)
		require.JSONEq(t, `{"message":"error","data":"充值校验失败，请稍后重试"}`, recorder.Body.String(), endpoint.name)
		require.Contains(t, logs.String(), secret)
		logs.Reset()
		require.Zero(t, env.countTopUps(t))
	}
}

type topUpRoundTripFunc func(*http.Request) (*http.Response, error)

func (f topUpRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTopUpQuoteOrderSettlementUnits(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	require.NoError(t, env.db.AutoMigrate(&model.Log{}))
	// Only the payment providers are stubbed. Real controller binding, quote,
	// stored order and model settlement/capacity code run against SQLite.
	transport := topUpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"id":"cs_test","url":"https://checkout.example.com"}`
		if strings.Contains(req.URL.Host, "creem") {
			body = `{"id":"creem_test","checkout_url":"https://checkout.example.com"}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	oldBackend := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{HTTPClient: &http.Client{Transport: transport}}))
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, oldBackend) })
	setTopUpTestValue(t, &stripe.Key, stripe.Key)
	setTopUpTestValue[http.RoundTripper](t, &http.DefaultTransport, transport)
	// Waffo SDK configuration failures happen after local insert. This lets
	// us inspect the real normalized Amount without any remote order request.
	setTopUpTestValue(t, &setting.WaffoSandbox, false)
	setTopUpTestValue(t, &setting.WaffoApiKey, "")
	setTopUpTestValue(t, &setting.WaffoPrivateKey, "")
	setTopUpTestValue(t, &setting.WaffoPublicCert, "")
	common.QuotaPerUnit = 2.5
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1.2}`))
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{12: 0.5}
	operation_setting.Price, setting.StripeUnitPrice = 2, 2
	setting.WaffoUnitPrice, setting.WaffoPancakeUnitPrice = 2, 2
	providers := []struct {
		name, provider string
		quote, pay     gin.HandlerFunc
	}{
		{"epay", model.PaymentProviderEpay, RequestAmount, RequestEpay},
		{"stripe", model.PaymentProviderStripe, RequestStripeAmount, RequestStripePay},
		{"waffo", model.PaymentProviderWaffo, RequestWaffoAmount, RequestWaffoPay},
		{"pancake", model.PaymentProviderWaffoPancake, RequestWaffoPancakeAmount, RequestWaffoPancakePay},
	}
	userID := 9200
	for _, display := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeCNY, operation_setting.QuotaDisplayTypeTokens} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		for _, provider := range providers {
			t.Run(display+"/"+provider.name, func(t *testing.T) {
				userID++
				storedAmount, credit := int64(12), 30
				payMoney := 14.4
				if display == operation_setting.QuotaDisplayTypeTokens {
					storedAmount, credit, payMoney = 4, 10, 5.76
				}
				if provider.name == "stripe" {
					storedAmount, credit = 12, 35 // Preserve float 12*1.2=14.399999999999999 then decimal truncation.
				}
				env.insertUser(t, userID, math.MaxInt32-credit)
				body := topUpEndpointBody(topUpTestEndpoint{name: provider.name}, "12")
				ctx, recorder := buildTopUpTestContext(t, "/topup-test", body, userID)
				provider.quote(ctx)
				require.JSONEq(t, fmt.Sprintf(`{"message":"success","data":"%.2f"}`, payMoney), recorder.Body.String())
				ctx, recorder = buildTopUpTestContext(t, "/topup-test", body, userID)
				provider.pay(ctx)
				var order model.TopUp
				require.NoError(t, env.db.Where("user_id = ?", userID).First(&order).Error, recorder.Body.String())
				require.Equal(t, storedAmount, order.Amount)
				if provider.name == "stripe" {
					require.InDelta(t, 14.4, order.Money, 1e-10)
				} else {
					require.InDelta(t, payMoney, order.Money, 1e-10)
				}
				// Gateway init failures above are not paid orders; reset only in
				// the fixture to exercise the same callback amount conversion.
				require.NoError(t, env.db.Model(&order).Update("status", common.TopUpStatusPending).Error)
				confirmation := model.PaymentConfirmation{Amount: fmt.Sprintf("%.2f", order.Money), DecimalPlaces: 2}
				recharge := func() error {
					switch provider.name {
					case "epay":
						return model.RechargeEpay(order.TradeNo, "alipay", "127.0.0.1", confirmation)
					case "stripe":
						return model.Recharge(order.TradeNo, "cus_test", "127.0.0.1")
					case "waffo":
						return model.RechargeWaffo(order.TradeNo, "127.0.0.1", confirmation)
					default:
						return model.RechargeWaffoPancake(order.TradeNo, confirmation)
					}
				}
				require.NoError(t, recharge())
				require.NoError(t, recharge())
				var user model.User
				require.NoError(t, env.db.First(&user, userID).Error)
				require.Equal(t, math.MaxInt32, user.Quota)
				// Same request now fails capacity in both actual entrypoints.
				before := env.countTopUps(t)
				for _, handler := range []gin.HandlerFunc{provider.quote, provider.pay} {
					ctx, recorder := buildTopUpTestContext(t, "/topup-test", body, userID)
					handler(ctx)
					require.JSONEq(t, `{"message":"error","data":"本次充值后额度将超出允许范围"}`, recorder.Body.String())
				}
				require.Equal(t, before, env.countTopUps(t))
			})
		}
	}
	// Creem's product quota is direct, even when QPU is unusable.
	common.QuotaPerUnit = math.NaN()
	setTopUpTestValue(t, &setting.CreemProducts, `[{"productId":"direct","price":9.99,"currency":"USD","quota":2147483647}]`)
	userID++
	env.insertUser(t, userID, 0)
	ctx, recorder := buildTopUpTestContext(t, "/topup-test", `{"product_id":"direct","payment_method":"creem"}`, userID)
	RequestCreemPay(ctx)
	require.Contains(t, recorder.Body.String(), `"message":"success"`)
	var order model.TopUp
	require.NoError(t, env.db.Where("user_id = ?", userID).First(&order).Error)
	require.EqualValues(t, math.MaxInt32, order.Amount)
	require.Equal(t, 9.99, order.Money)
	confirmation := model.PaymentConfirmation{Amount: "9.99", Currency: "USD", ExpectedCurrency: "USD", DecimalPlaces: 2}
	require.NoError(t, model.RechargeCreem(order.TradeNo, "127.0.0.1", confirmation))
	require.NoError(t, model.RechargeCreem(order.TradeNo, "127.0.0.1", confirmation))
	var user model.User
	require.NoError(t, env.db.First(&user, userID).Error)
	require.Equal(t, math.MaxInt32, user.Quota)
}

func TestTopUpTokensLimitDoesNotReportNormalizedCurrencyAmount(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9300, 0)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	common.QuotaPerUnit = 500000
	for _, endpoint := range topUpAmountEndpoints() {
		if strings.HasPrefix(endpoint.name, "stripe") {
			continue // Stripe keeps its independent 10000 quantity ceiling.
		}
		ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, "2147500000"), 9300)
		endpoint.handler(ctx)
		require.JSONEq(t, `{"message":"error","data":"充值额度超出允许范围"}`, recorder.Body.String())
	}
	require.Zero(t, env.countTopUps(t))
}

func TestTopUpTinyQPUKeepsValidConversions(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9400, 0)
	common.QuotaPerUnit = 1e-10
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	// MaxInt32/QPU exceeds int64, but a small valid request must still work.
	require.EqualValues(t, math.MaxInt64, maxTopUpAmount())
	for _, normalize := range []func(int64) (int64, error){normalizeEpayTopUpAmount, normalizeWaffoTopUpAmount, normalizeWaffoPancakeTopUpAmount} {
		amount, err := normalize(1)
		require.NoError(t, err)
		require.EqualValues(t, 10000000000, amount)
		quota, err := model.TopUpQuotaFromAmount(amount)
		require.NoError(t, err)
		require.Equal(t, 1, quota)
	}
	for _, endpoint := range []topUpTestEndpoint{{"epay", RequestAmount}, {"waffo", RequestWaffoAmount}, {"pancake", RequestWaffoPancakeAmount}} {
		ctx, recorder := buildTopUpTestContext(t, "/topup-test", `{"amount":1}`, 9400)
		endpoint.handler(ctx)
		require.JSONEq(t, `{"message":"success","data":"10000000000.00"}`, recorder.Body.String())
	}
	common.QuotaPerUnit = 0.5
	quota, err := model.TopUpQuotaFromAmount(2)
	require.NoError(t, err)
	require.Equal(t, 1, quota)
}

func TestTopUpGroupRatioAndDiscountFallbacks(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9401, 0)
	common.QuotaPerUnit = 1
	for _, ratio := range []string{"-1", "1e308"} {
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":`+ratio+`}`))
		for _, endpoint := range topUpAmountEndpoints() {
			ctx, recorder := buildTopUpTestContext(t, "/topup-test", topUpEndpointBody(endpoint, "10"), 9401)
			require.NotPanics(t, func() { endpoint.handler(ctx) })
			require.Contains(t, recorder.Body.String(), `"message":"error"`)
			require.Zero(t, env.countTopUps(t))
		}
	}
	// Invalid negative input must not become positive via a negative ratio.
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":-1}`))
	_, err := getStripeCreditedQuota(-1, "default")
	require.ErrorIs(t, err, model.ErrInvalidTopUpQuota)
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":0}`))
	for _, discount := range []float64{-1, 0} {
		operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{10: discount}
		for _, handler := range []gin.HandlerFunc{RequestAmount, RequestStripeAmount, RequestWaffoAmount, RequestWaffoPancakeAmount} {
			ctx, recorder := buildTopUpTestContext(t, "/topup-test", `{"amount":10}`, 9401)
			handler(ctx)
			require.JSONEq(t, `{"message":"success","data":"10.00"}`, recorder.Body.String())
		}
	}
}

func TestCreemProductNumericConfiguration(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	setupTopUpGatewaySettings(t)
	env.insertUser(t, 9402, 0)
	setTopUpTestValue(t, &setting.CreemProducts, "")
	for _, quota := range []string{"-1", "0", "2147483648", "9223372036854775807", "9223372036854775808", "NaN", "1e999"} {
		setting.CreemProducts = fmt.Sprintf(`[{"productId":"test","price":10,"quota":%s}]`, quota)
		ctx, recorder := buildTopUpTestContext(t, "/topup-test", `{"product_id":"test","payment_method":"creem"}`, 9402)
		require.NotPanics(t, func() { RequestCreemPay(ctx) })
		require.Contains(t, recorder.Body.String(), `"message":"error"`)
		require.Zero(t, env.countTopUps(t))
	}
	for _, price := range []string{"-1", "0", "NaN", "1e999"} {
		setting.CreemProducts = fmt.Sprintf(`[{"productId":"test","price":%s,"quota":1}]`, price)
		ctx, recorder := buildTopUpTestContext(t, "/topup-test", `{"product_id":"test","payment_method":"creem"}`, 9402)
		require.NotPanics(t, func() { RequestCreemPay(ctx) })
		require.Contains(t, recorder.Body.String(), `"message":"error"`)
		require.Zero(t, env.countTopUps(t))
	}
}

func TestValidateTopUpQuotaConversionBoundary(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	env.insertUser(t, 9001, 0)

	// QPU = 500000: 4294 * 500000 = 2,147,000,000 fits the Nexus MaxInt32
	// wallet ceiling, 4295 * 500000 does not.
	require.NoError(t, validateTopUpQuota(9001, 4294))
	require.EqualError(t, validateTopUpQuota(9001, 4295), "单笔充值数量不能大于 4294")
}

func TestRequestAmountRejectsTopUpThatCannotBeSettled(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	env.insertUser(t, 9002, 0)

	ctx, recorder := buildTopUpTestContext(t, "/api/user/amount", `{"amount":4295}`, 9002)
	RequestAmount(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"单笔充值数量不能大于 4294"}`, recorder.Body.String())

	ctx, recorder = buildTopUpTestContext(t, "/api/user/amount", `{"amount":4294}`, 9002)
	RequestAmount(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.Equal(t, "success", payload.Message)
}

func TestRequestAmountRejectsCapacityExceeded(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	creditedQuota := 100 * int(common.QuotaPerUnit)
	env.insertUser(t, 9003, model.MaxStoredUserQuota()-creditedQuota+1)

	ctx, recorder := buildTopUpTestContext(t, "/api/user/amount", `{"amount":100}`, 9003)
	RequestAmount(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"本次充值后额度将超出允许范围"}`, recorder.Body.String())
}

func TestRequestAmountRejectsMissingUser(t *testing.T) {
	setupTopUpQuotaTestEnv(t)

	ctx, recorder := buildTopUpTestContext(t, "/api/user/amount", `{"amount":10}`, 987654)
	RequestAmount(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"充值用户不存在"}`, recorder.Body.String())
}

func TestStripeCreditedQuotaIncludesGroupRatioAndZeroFallback(t *testing.T) {
	setupTopUpQuotaTestEnv(t)
	oldTopupGroupRatio := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldTopupGroupRatio))
	})

	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"vip":2}`))
	quota, err := getStripeCreditedQuota(2147, "vip")
	require.NoError(t, err)
	assert.Equal(t, 2147*2*500000, quota)
	assert.InDelta(t, 2.0, GetChargedAmount(1, model.User{Group: "vip"}), 0.000001)

	_, err = getStripeCreditedQuota(2148, "vip")
	require.ErrorIs(t, err, model.ErrTopUpQuotaOutOfRange)

	// A configured zero ratio falls back to 1 for both pricing and crediting.
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"free":0}`))
	quota, err = getStripeCreditedQuota(1, "free")
	require.NoError(t, err)
	assert.Equal(t, 500000, quota)
	assert.InDelta(t, 1.0, GetChargedAmount(1, model.User{Group: "free"}), 0.000001)
}

func TestStripeRequestAmountRejectsCreditedQuotaOverflow(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	oldTopupGroupRatio := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldTopupGroupRatio))
	})
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":100000}`))
	env.insertUser(t, 9010, 0)

	ctx, recorder := buildTopUpTestContext(t, "/api/user/stripe/amount", `{"amount":1}`, 9010)
	RequestStripeAmount(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"充值额度超出允许范围"}`, recorder.Body.String())
}

func TestStripeRequestAmountRejectsCapacityExceeded(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	env.insertUser(t, 9011, model.MaxStoredUserQuota())

	ctx, recorder := buildTopUpTestContext(t, "/api/user/stripe/amount", `{"amount":1}`, 9011)
	RequestStripeAmount(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"本次充值后额度将超出允许范围"}`, recorder.Body.String())
}

func TestStripeRequestPayRejectsMissingUserBeforeCreatingOrder(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)

	ctx, recorder := buildTopUpTestContext(t, "/api/user/stripe/pay", `{"amount":1,"payment_method":"stripe"}`, 987655)
	RequestStripePay(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"用户不存在"}`, recorder.Body.String())
	assert.Zero(t, env.countTopUps(t))
}

func TestCreemRequestPayRejectsMissingUserBeforeCreatingOrder(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	oldProducts := setting.CreemProducts
	t.Cleanup(func() { setting.CreemProducts = oldProducts })
	setting.CreemProducts = `[{"productId":"prod_valid","name":"Valid","price":9.99,"currency":"USD","quota":1000}]`

	ctx, recorder := buildTopUpTestContext(t, "/api/user/creem/pay", `{"product_id":"prod_valid","payment_method":"creem"}`, 987656)
	RequestCreemPay(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"用户不存在"}`, recorder.Body.String())
	assert.Zero(t, env.countTopUps(t))
}

func TestCreemRequestPayRejectsInvalidProductQuota(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	env.insertUser(t, 9020, 0)
	oldProducts := setting.CreemProducts
	t.Cleanup(func() { setting.CreemProducts = oldProducts })
	setting.CreemProducts = `[` +
		`{"productId":"prod_zero","name":"Zero","price":9.99,"currency":"USD","quota":0},` +
		`{"productId":"prod_huge","name":"Huge","price":9.99,"currency":"USD","quota":2147483648}` +
		`]`

	for _, productID := range []string{"prod_zero", "prod_huge"} {
		ctx, recorder := buildTopUpTestContext(t, "/api/user/creem/pay", `{"product_id":"`+productID+`","payment_method":"creem"}`, 9020)
		RequestCreemPay(ctx)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.JSONEq(t, `{"message":"error","data":"无效的充值额度"}`, recorder.Body.String())
	}
	assert.Zero(t, env.countTopUps(t))
}

func TestCreemRequestPayRejectsCapacityExceeded(t *testing.T) {
	env := setupTopUpQuotaTestEnv(t)
	env.insertUser(t, 9021, model.MaxStoredUserQuota())
	oldProducts := setting.CreemProducts
	t.Cleanup(func() { setting.CreemProducts = oldProducts })
	setting.CreemProducts = `[{"productId":"prod_valid","name":"Valid","price":9.99,"currency":"USD","quota":1000}]`

	ctx, recorder := buildTopUpTestContext(t, "/api/user/creem/pay", `{"product_id":"prod_valid","payment_method":"creem"}`, 9021)
	RequestCreemPay(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"message":"error","data":"本次充值后额度将超出允许范围"}`, recorder.Body.String())
	assert.Zero(t, env.countTopUps(t))
}
