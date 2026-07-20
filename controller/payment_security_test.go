package controller

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/waffo-com/waffo-go/core"
)

func TestVerifyCreemSignatureRequiresSecretInTestMode(t *testing.T) {
	originalTestMode := setting.CreemTestMode
	setting.CreemTestMode = true
	t.Cleanup(func() { setting.CreemTestMode = originalTestMode })

	assert.False(t, verifyCreemSignature(`{"eventType":"checkout.completed"}`, "attacker-signature", ""))
}

func TestVerifyCreemSignatureUsesHMAC(t *testing.T) {
	payload := `{"eventType":"checkout.completed"}`
	secret := "webhook-secret"
	signature := generateCreemSignature(payload, secret)

	assert.True(t, verifyCreemSignature(payload, signature, secret))
	assert.False(t, verifyCreemSignature(payload+" ", signature, secret))
}

func TestPaymentPayloadFingerprintIsDeterministicAndRedacted(t *testing.T) {
	payload := []byte(`{"customer":"private@example.com"}`)
	fingerprint := paymentPayloadFingerprint(payload)

	assert.Len(t, fingerprint, 64)
	assert.Equal(t, fingerprint, paymentPayloadFingerprint(payload))
	assert.NotContains(t, fingerprint, "private")
}

func TestWaffoCurrencyDecimalPlaces(t *testing.T) {
	assert.Equal(t, int32(0), waffoCurrencyDecimalPlaces(" jpy "))
	assert.Equal(t, int32(2), waffoCurrencyDecimalPlaces("usd"))
}

func TestWaffoCurrencySnapshotInTradeNo(t *testing.T) {
	currency, ok := waffoCurrencyFromTradeNo("WAFFO-USD-42-1720000000000-abc123")
	assert.True(t, ok)
	assert.Equal(t, "USD", currency)

	_, ok = waffoCurrencyFromTradeNo("WAFFO-42-1720000000000-abc123")
	assert.False(t, ok)
	_, ok = waffoCurrencyFromTradeNo("WAFFO-usd-42-1720000000000-abc123")
	assert.False(t, ok)
}

func TestWaffoOnlyClosesTerminalFailure(t *testing.T) {
	assert.True(t, isWaffoOrderTerminalFailure(core.OrderStatusOrderClose))
	assert.False(t, isWaffoOrderTerminalFailure(core.OrderStatusPayInProgress))
	assert.False(t, isWaffoOrderTerminalFailure(core.OrderStatusAuthorizationRequired))
	assert.False(t, isWaffoOrderTerminalFailure(core.OrderStatusAuthedWaitingCapture))
}

func TestWaffoWebhookBindsConfiguredMerchant(t *testing.T) {
	originalMerchantID := setting.WaffoMerchantId
	setting.WaffoMerchantId = "merchant_nexus"
	t.Cleanup(func() { setting.WaffoMerchantId = originalMerchantID })

	assert.True(t, isConfiguredWaffoMerchant(map[string]interface{}{"merchantId": "merchant_nexus"}))
	assert.False(t, isConfiguredWaffoMerchant(map[string]interface{}{"merchantId": "merchant_attacker"}))
	assert.False(t, isConfiguredWaffoMerchant(nil))

	setting.WaffoMerchantId = ""
	assert.True(t, isConfiguredWaffoMerchant(nil))
}

func TestWaffoPancakeResolutionErrorsKeepDatabaseFailuresRetryable(t *testing.T) {
	assert.True(t, isPermanentWaffoPancakeResolutionError(model.ErrTopUpNotFound))
	assert.True(t, isPermanentWaffoPancakeResolutionError(model.ErrSubscriptionOrderNotFound))
	assert.True(t, isPermanentWaffoPancakeResolutionError(model.ErrPaymentOrderMismatch))
	assert.True(t, isPermanentWaffoPancakeResolutionError(service.ErrWaffoPancakeWebhookOrderInvalid))
	assert.False(t, isPermanentWaffoPancakeResolutionError(errors.New("database unavailable")))
}

func TestWaffoPancakeWebhookRequiresConfiguredStore(t *testing.T) {
	originalStoreID := setting.WaffoPancakeStoreID
	setting.WaffoPancakeStoreID = "store_nexus"
	t.Cleanup(func() { setting.WaffoPancakeStoreID = originalStoreID })

	assert.True(t, isConfiguredWaffoPancakeStore(" store_nexus "))
	assert.False(t, isConfiguredWaffoPancakeStore("store_attacker"))
	assert.False(t, isConfiguredWaffoPancakeStore(""))
}
