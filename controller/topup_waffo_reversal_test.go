package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/waffo-com/waffo-go/core"
	"github.com/waffo-com/waffo-go/utils"
	"gorm.io/gorm"
)

func waffoReversalTestBinding() model.TopUpPaymentBinding {
	return model.TopUpPaymentBinding{
		Provider: model.PaymentProviderWaffo, CheckoutID: "request_original", PaymentID: "order_original",
		Currency: "USD", PaidMinor: 1000, CreditedQuota: 100,
	}
}

func waffoRefundTestResult() core.RefundNotificationResult {
	return core.RefundNotificationResult{
		RefundRequestID: "refund_request", AcquiringRefundOrderID: "refund_original",
		AcquiringOrderID: "order_original", OrigPaymentRequestID: "request_original",
		RefundAmount: "3.00", RefundStatus: core.RefundStatusPartiallyRefunded,
		RefundCompletedAt: "2026-04-01T03:00:00.123Z",
		// These are deliberately different from the original order amount/currency.
		UserCurrency: "JPY", FinalDealAmount: "450", RemainingRefundAmount: "7.00",
	}
}

func waffoChargebackTestResult() waffoChargebackResult {
	return waffoChargebackResult{
		ChargebackID: "cb_first", OriginalOrderID: "order_original", OriginalPaymentRequestID: "request_original",
		MerchantID: "merchant_test", ChargebackStatus: "ACTION_REQUIRED", Amount: "4.00", Currency: "USD",
		ChargebackDateTime: "2026-04-01T03:00:00.123Z", LifeCycle: "FIRST_CYCLE",
	}
}

func TestWaffoRefundReversalUsesOriginalCurrencyAndSingleItem(t *testing.T) {
	binding, result := waffoReversalTestBinding(), waffoRefundTestResult()
	setTopUpTestValue(t, &setting.WaffoCurrency, "JPY")
	got, err := waffoRefundReversal(&result, &binding)
	require.NoError(t, err)
	require.Equal(t, "refund_item", got.Kind)
	require.Equal(t, "refund_original", got.ObjectID)
	require.Equal(t, binding.PaymentID, got.PaymentID)
	require.Equal(t, "USD", got.Currency)
	require.EqualValues(t, 300, got.AmountMinor)
	require.EqualValues(t, 1000, got.OriginalMinor)
	require.True(t, got.Terminal)
	require.False(t, got.Released)
	require.Empty(t, got.GroupID)
	require.Len(t, got.EventID, 64)

	result.UserCurrency, result.RemainingRefundAmount, result.FinalDealAmount = "EUR", "0", "99999"
	replay, err := waffoRefundReversal(&result, &binding)
	require.NoError(t, err)
	require.Equal(t, got, replay)
	result.RefundStatus = core.RefundStatusFullyRefunded
	full, err := waffoRefundReversal(&result, &binding)
	require.NoError(t, err)
	require.EqualValues(t, 300, full.AmountMinor) // FULLY_REFUNDED is not a cumulative amount.
	require.Equal(t, got.ObjectID, full.ObjectID)
	require.NotEqual(t, got.EventID, full.EventID)

	result.RefundStatus, result.RefundAmount = core.RefundStatusPartiallyRefunded, "3.01"
	changed, err := waffoRefundReversal(&result, &binding)
	require.NoError(t, err)
	require.Equal(t, got.EventID, changed.EventID) // Model receipt must reject changed cash on replay.
	binding.PaymentID, result.AcquiringOrderID = "other_payment", "other_payment"
	other, err := waffoRefundReversal(&result, &binding)
	require.NoError(t, err)
	require.NotEqual(t, got.EventID, other.EventID)

	for _, tc := range []struct {
		currency, amount string
		minor            int64
	}{
		{"JPY", "300", 300},
		{"USD", "0.12", 12},
	} {
		t.Run(tc.currency, func(t *testing.T) {
			binding.Currency, result.RefundAmount = tc.currency, tc.amount
			event, err := waffoRefundReversal(&result, &binding)
			require.NoError(t, err)
			require.Equal(t, tc.minor, event.AmountMinor)
		})
	}
}

func TestWaffoRefundReversalRejectsUnverifiedSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*core.RefundNotificationResult, *model.TopUpPaymentBinding)
	}{
		{"pending", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) {
			r.RefundStatus = core.RefundStatusInProgress
		}},
		{"failed", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) {
			r.RefundStatus = core.RefundStatusFailed
		}},
		{"unknown_status", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.RefundStatus = "SUCCESS" }},
		{"payment_identity", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) {
			r.AcquiringOrderID = "other_order"
		}},
		{"checkout_identity", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) {
			r.OrigPaymentRequestID = "other_request"
		}},
		{"missing_checkout", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.OrigPaymentRequestID = "" }},
		{"missing_refund_identity", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.AcquiringRefundOrderID = "" }},
		{"overpayment", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.RefundAmount = "10.01" }},
		{"nonpositive", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.RefundAmount = "0" }},
		{"excess_precision", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.RefundAmount = "3.001" }},
		{"scientific_notation", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.RefundAmount = "3e0" }},
		{"missing_completion_time", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) { r.RefundCompletedAt = "" }},
		{"invalid_completion_time", func(r *core.RefundNotificationResult, _ *model.TopUpPaymentBinding) {
			r.RefundCompletedAt = "yesterday"
		}},
		{"foreign_provider", func(_ *core.RefundNotificationResult, b *model.TopUpPaymentBinding) {
			b.Provider = model.PaymentProviderStripe
		}},
		{"unbound_currency", func(_ *core.RefundNotificationResult, b *model.TopUpPaymentBinding) { b.Currency = "" }},
		{"missing_credit_snapshot", func(_ *core.RefundNotificationResult, b *model.TopUpPaymentBinding) { b.CreditedQuota = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, result := waffoReversalTestBinding(), waffoRefundTestResult()
			tc.mutate(&result, &binding)
			_, err := waffoRefundReversal(&result, &binding)
			require.Error(t, err)
		})
	}
	binding, result := waffoReversalTestBinding(), waffoRefundTestResult()
	_, err := waffoRefundReversal(nil, &binding)
	require.Error(t, err)
	_, err = waffoRefundReversal(&result, nil)
	require.Error(t, err)
}

func TestWaffoChargebackReversalOfficialStatusesAndGroups(t *testing.T) {
	setTopUpTestValue(t, &setting.WaffoMerchantId, "merchant_test")
	binding, result := waffoReversalTestBinding(), waffoChargebackTestResult()
	seen := map[string]bool{}
	for _, tc := range []struct {
		status             string
		terminal, released bool
	}{
		{"ACTION_REQUIRED", false, false},
		{"UNDER_REVIEW", false, false},
		{"EVIDENCE_REQUIRED", false, false},
		{"SECOND_CYCLE_RESPONSE_REQUIRED", false, false},
		{"ESCALATE_TO_2ND_CYCLE", false, false},
		{"CASE_WON", true, true},
		{"CANCELED", true, true},
		{"SETTLED", true, true},
		{"CASE_LOST", true, false},
		{"ACCEPTED", true, false},
		{"EXPIRED", true, false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			result.ChargebackStatus = tc.status
			got, err := waffoChargebackReversal(&result, &binding)
			require.NoError(t, err)
			require.Equal(t, "dispute", got.Kind)
			require.Equal(t, "cb_first", got.ObjectID)
			require.Equal(t, "cb_first", got.GroupID)
			require.EqualValues(t, 400, got.AmountMinor)
			require.EqualValues(t, 1000, got.OriginalMinor)
			require.Equal(t, tc.terminal, got.Terminal)
			require.Equal(t, tc.released, got.Released)
			occurred, err := time.Parse(time.RFC3339Nano, result.ChargebackDateTime)
			require.NoError(t, err)
			require.Equal(t, occurred.Unix(), got.OccurredAt)
			require.False(t, seen[got.EventID])
			seen[got.EventID] = true
		})
	}
	result.ChargebackStatus, result.RelevantChargebackID = "ESCALATE_TO_2ND_CYCLE", "cb_second"
	first, err := waffoChargebackReversal(&result, &binding)
	require.NoError(t, err)
	require.Equal(t, "cb_first", first.GroupID)
	result.ChargebackID, result.LifeCycle, result.RelevantChargebackID = "cb_second", "SECOND_CYCLE", "cb_first"
	result.ChargebackStatus = "SECOND_CYCLE_RESPONSE_REQUIRED"
	second, err := waffoChargebackReversal(&result, &binding)
	require.NoError(t, err)
	require.Equal(t, first.GroupID, second.GroupID)
	require.Equal(t, "cb_second", second.ObjectID)
	require.NotEqual(t, first.EventID, second.EventID)
}

func TestWaffoChargebackReversalRejectsIdentityAndUnknownFields(t *testing.T) {
	setTopUpTestValue(t, &setting.WaffoMerchantId, "merchant_test")
	for _, tc := range []struct {
		name   string
		mutate func(*waffoChargebackResult)
	}{
		{"merchant", func(r *waffoChargebackResult) { r.MerchantID = "other_merchant" }},
		{"order", func(r *waffoChargebackResult) { r.OriginalOrderID = "other_order" }},
		{"request", func(r *waffoChargebackResult) { r.OriginalPaymentRequestID = "other_request" }},
		{"currency", func(r *waffoChargebackResult) { r.Currency = "EUR" }},
		{"amount", func(r *waffoChargebackResult) { r.Amount = "10.01" }},
		{"chargeback_id", func(r *waffoChargebackResult) { r.ChargebackID = "" }},
		{"status", func(r *waffoChargebackResult) { r.ChargebackStatus = "FUTURE_STATUS" }},
		{"lifecycle", func(r *waffoChargebackResult) { r.LifeCycle = "THIRD_CYCLE" }},
		{"missing_root", func(r *waffoChargebackResult) { r.LifeCycle = "SECOND_CYCLE" }},
		{"self_root", func(r *waffoChargebackResult) { r.LifeCycle, r.RelevantChargebackID = "SECOND_CYCLE", r.ChargebackID }},
		{"missing_time", func(r *waffoChargebackResult) { r.ChargebackDateTime = "" }},
		{"invalid_time", func(r *waffoChargebackResult) { r.ChargebackDateTime = "1711940400" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, result := waffoReversalTestBinding(), waffoChargebackTestResult()
			tc.mutate(&result)
			_, err := waffoChargebackReversal(&result, &binding)
			require.Error(t, err)
		})
	}
	binding, result := waffoReversalTestBinding(), waffoChargebackTestResult()
	_, err := waffoChargebackReversal(nil, &binding)
	require.Error(t, err)
	setTopUpTestValue(t, &setting.WaffoMerchantId, "")
	_, err = waffoChargebackReversal(&result, &binding)
	require.Error(t, err)
}

func setupWaffoReversalWebhook(t *testing.T) (platform, merchant *utils.KeyPair) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	platform, err := utils.GenerateKeyPair()
	require.NoError(t, err)
	merchant, err = utils.GenerateKeyPair()
	require.NoError(t, err)
	setTopUpTestValue(t, &setting.WaffoSandbox, true)
	setTopUpTestValue(t, &setting.WaffoSandboxApiKey, "test-api-key")
	setTopUpTestValue(t, &setting.WaffoSandboxPrivateKey, merchant.PrivateKey)
	setTopUpTestValue(t, &setting.WaffoSandboxPublicCert, platform.PublicKey)
	setTopUpTestValue(t, &setting.WaffoMerchantId, "merchant_test")
	return platform, merchant
}

func waffoReversalTestPayload(t *testing.T, eventType string, result interface{}) string {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"eventType": eventType, "result": result})
	require.NoError(t, err)
	return string(body)
}

func callWaffoReversalWebhook(body, signature string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/waffo/webhook", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-SIGNATURE", signature)
	WaffoWebhook(ctx)
	ctx.Writer.WriteHeaderNow()
	return recorder
}

func requireWaffoReversalACK(t *testing.T, recorder *httptest.ResponseRecorder, publicKey string, success bool) {
	t.Helper()
	status, message := http.StatusInternalServerError, "failed"
	if success {
		status, message = http.StatusOK, "success"
	}
	require.Equal(t, status, recorder.Code, recorder.Body.String())
	require.JSONEq(t, `{"message":"`+message+`"}`, recorder.Body.String())
	require.True(t, utils.Verify(recorder.Body.String(), recorder.Header().Get("X-SIGNATURE"), publicKey))
}

func TestWaffoReversalWebhookVerifiesOriginalBodyAndRejectsStorageFailure(t *testing.T) {
	platform, merchant := setupWaffoReversalWebhook(t)
	setTopUpTestValue(t, &model.DB, (*gorm.DB)(nil))
	body := " \n" + waffoReversalTestPayload(t, core.EventRefund, waffoRefundTestResult()) + "\n"
	signature, err := utils.Sign(body, platform.PrivateKey)
	require.NoError(t, err)
	wrongSignature, err := utils.Sign(body, merchant.PrivateKey)
	require.NoError(t, err)
	for _, tc := range []struct{ name, body, signature string }{
		{"missing_signature", body, ""},
		{"wrong_signer", body, wrongSignature},
		{"changed_amount", strings.Replace(body, `"3.00"`, `"3.01"`, 1), signature},
		{"changed_whitespace", strings.TrimSpace(body), signature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := callWaffoReversalWebhook(tc.body, tc.signature)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "success")
		})
	}
	for _, payload := range []string{
		body,
		waffoReversalTestPayload(t, waffoEventChargeback, waffoChargebackTestResult()),
		`{"eventType":"UNKNOWN_NOTIFICATION","result":{}}`,
		`{"eventType":"REFUND_NOTIFICATION","result":null}`,
		`{"eventType":"CHARGEBACK_NOTIFICATION","result":null}`,
		`{"eventType":"REFUND_NOTIFICATION","result":`,
	} {
		sig, err := utils.Sign(payload, platform.PrivateKey)
		require.NoError(t, err)
		requireWaffoReversalACK(t, callWaffoReversalWebhook(payload, sig), merchant.PublicKey, false)
	}
}

func setupWaffoReversalDB(t *testing.T) (*gorm.DB, model.TopUpPaymentBinding) {
	t.Helper()
	db := setupTopUpQuotaTestEnv(t).db
	require.NoError(t, db.AutoMigrate(&model.TopUpPaymentBinding{}, &model.TopUpReversalState{}, &model.TopUpReversalEvent{}, &model.WalletSourceReversal{}, &model.RebateSourceReversal{}))
	require.NoError(t, db.Create(&model.RebateSystemState{Key: "wallet_credit_baseline_v1", Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	return db, addWaffoReversalTestBinding(t, db, "original")
}

func addWaffoReversalTestBinding(t *testing.T, db *gorm.DB, suffix string) model.TopUpPaymentBinding {
	t.Helper()
	user := model.User{Username: "waffo_" + suffix, AffCode: "waffo_" + suffix, Group: "default", Quota: 100}
	require.NoError(t, db.Create(&user).Error)
	trade := "waffo_trade_" + suffix
	key, err := model.NewWalletCreditEventKey(user.Id, model.WalletCreditSourceTopUp, "topup", trade, "paid")
	require.NoError(t, err)
	lot := model.WalletCreditLot{
		CreditEventKey: key, UserID: user.Id, Source: model.WalletCreditSourceTopUp, Component: "paid", Eligible: true,
		ReferenceType: "topup", ReferenceID: trade, QuotaTotal: 100, QuotaRemaining: 100,
		EligibleMoneyMicros: 1_000_000, EligibleMoneyRemaining: 1_000_000, QuotaPerUnitMicros: 100_000_000,
	}
	require.NoError(t, db.Create(&lot).Error)
	order := model.TopUp{UserId: user.Id, TradeNo: trade, PaymentProvider: model.PaymentProviderWaffo, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	identity := model.TopUpPaymentIdentity{Provider: model.PaymentProviderWaffo, CheckoutID: "request_" + suffix, PaymentID: "order_" + suffix, Currency: "USD", PaidMinor: 1000}
	require.NoError(t, model.RegisterTopUpCheckout(trade, identity.Provider, identity.CheckoutID))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := model.BindTopUpPaymentTx(tx, &order, identity, 100); err != nil {
			return err
		}
		return tx.Model(&order).Update("status", common.TopUpStatusSuccess).Error
	}))
	var binding model.TopUpPaymentBinding
	require.NoError(t, db.Where("trade_no = ?", trade).First(&binding).Error)
	return binding
}

func TestWaffoReversalWebhookRefundReplayFailuresAndPending(t *testing.T) {
	platform, merchant := setupWaffoReversalWebhook(t)
	db, binding := setupWaffoReversalDB(t)
	send := func(result core.RefundNotificationResult, success bool) {
		t.Helper()
		body := waffoReversalTestPayload(t, core.EventRefund, result)
		signature, err := utils.Sign(body, platform.PrivateKey)
		require.NoError(t, err)
		requireWaffoReversalACK(t, callWaffoReversalWebhook(body, signature), merchant.PublicKey, success)
	}
	result := waffoRefundTestResult()
	for _, status := range []string{core.RefundStatusInProgress, core.RefundStatusFailed, "UNKNOWN"} {
		result.RefundStatus = status
		send(result, false)
	}
	result = waffoRefundTestResult()
	result.OrigPaymentRequestID = "wrong_request"
	send(result, false)
	result = waffoRefundTestResult()
	result.AcquiringOrderID = "unbound_order"
	send(result, false)
	var receipts int64
	require.NoError(t, db.Model(&model.TopUpReversalEvent{}).Count(&receipts).Error)
	require.Zero(t, receipts)

	result = waffoRefundTestResult()
	holdKey, err := model.NewWalletFinalSettlementKey(binding.UserID, "relay", "waffo-pending")
	require.NoError(t, err)
	require.NoError(t, model.ReserveWalletQuota(db, holdKey, binding.UserID, 10))
	send(result, false)
	send(result, false) // A durable receipt is not success while accounting is held.
	require.NoError(t, db.First(&binding, binding.ID).Error)
	require.True(t, binding.ReversalPending)
	require.NoError(t, model.RefundWalletReservation(db, holdKey, binding.UserID))
	send(result, true)
	send(result, true)
	result.RefundStatus = core.RefundStatusFullyRefunded
	send(result, true)
	var user model.User
	require.NoError(t, db.First(&user, binding.UserID).Error)
	require.Equal(t, 70, user.Quota)
	var states int64
	require.NoError(t, db.Model(&model.TopUpReversalState{}).Count(&states).Error)
	require.EqualValues(t, 1, states)
	result.RefundAmount = "3.01"
	send(result, false)
	require.NoError(t, db.First(&user, binding.UserID).Error)
	require.Equal(t, 70, user.Quota)

	// A real query failure must also be retried, even with a valid signature.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	send(waffoRefundTestResult(), false)
}

func TestWaffoReversalWebhookChargebackGroupsAndCrossPaymentRoot(t *testing.T) {
	platform, merchant := setupWaffoReversalWebhook(t)
	db, binding := setupWaffoReversalDB(t)
	send := func(result waffoChargebackResult, success bool) {
		t.Helper()
		body := waffoReversalTestPayload(t, waffoEventChargeback, result)
		signature, err := utils.Sign(body, platform.PrivateKey)
		require.NoError(t, err)
		requireWaffoReversalACK(t, callWaffoReversalWebhook(body, signature), merchant.PublicKey, success)
	}
	quota := func(want int) {
		t.Helper()
		var user model.User
		require.NoError(t, db.First(&user, binding.UserID).Error)
		require.Equal(t, want, user.Quota)
	}
	first := waffoChargebackTestResult()
	send(first, true)
	quota(60)
	first.ChargebackStatus, first.RelevantChargebackID = "ESCALATE_TO_2ND_CYCLE", "cb_second"
	send(first, true)
	quota(60)
	second := first
	second.ChargebackID, second.LifeCycle, second.RelevantChargebackID = "cb_second", "SECOND_CYCLE", "cb_first"
	second.ChargebackStatus = "SECOND_CYCLE_RESPONSE_REQUIRED"
	send(second, true)
	send(second, true)
	quota(60)

	other := addWaffoReversalTestBinding(t, db, "foreign")
	foreign := second
	foreign.ChargebackID, foreign.OriginalOrderID, foreign.OriginalPaymentRequestID = "cb_foreign", other.PaymentID, other.CheckoutID
	send(foreign, false) // Model rejects using another payment's first-round root.
	foreign = first
	foreign.MerchantID = "other_merchant"
	send(foreign, false)
	foreign = first
	foreign.ChargebackStatus = "UNKNOWN"
	send(foreign, false)
	quota(60)

	second.ChargebackStatus = "CASE_WON"
	send(second, true)
	quota(60) // First round is still effective.
	first.ChargebackStatus = "CASE_WON"
	send(first, true)
	quota(100)
	first.ChargebackStatus = "ACTION_REQUIRED"
	send(first, true) // Reordered open notification cannot override terminal release.
	quota(100)
	first.ChargebackStatus = "CASE_LOST"
	send(first, false) // Creation time cannot order contradictory terminal outcomes.
	quota(100)
}
