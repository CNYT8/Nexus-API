package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func stripeReversalTestEvent(t *testing.T, kind, object string) stripe.Event {
	t.Helper()
	var event stripe.Event
	payload := fmt.Sprintf(`{"id":"evt_verified","object":"event","created":100,"type":%q,"data":{"object":%s}}`, kind, object)
	require.NoError(t, json.Unmarshal([]byte(payload), &event))
	return event
}

func TestStripeReversalUsesVerifiedCashIdentity(t *testing.T) {
	event := stripeReversalTestEvent(t, "charge.refunded", `{"id":"ch_real","object":"charge","paid":true,"payment_intent":"pi_real","currency":"usd","amount":1000,"amount_refunded":300,"metadata":{"trade_no":"attacker_order"}}`)
	got, apply, err := stripeReversalFromEvent(event)
	require.NoError(t, err)
	require.True(t, apply)
	require.Equal(t, "pi_real", got.PaymentID)
	require.Equal(t, "ch_real", got.ObjectID)
	require.Equal(t, "refund_total", got.Kind)
	require.EqualValues(t, 300, got.AmountMinor)
	require.EqualValues(t, 1000, got.OriginalMinor)
	for _, object := range []string{
		`{"id":"ch_real","paid":true,"currency":"usd","amount":1000,"amount_refunded":300,"metadata":{"payment_intent":"pi_metadata"}}`,
		`{"id":"ch_real","paid":false,"payment_intent":"pi_real","currency":"usd","amount":1000,"amount_refunded":300}`,
		`{"id":"ch_real","paid":true,"payment_intent":"pi_real","currency":"usd","amount":1000,"amount_refunded":1001}`,
	} {
		_, apply, err = stripeReversalFromEvent(stripeReversalTestEvent(t, "charge.refunded", object))
		require.Error(t, err)
		require.False(t, apply)
	}
}

func TestStripeReversalDisputeAndWarningSemantics(t *testing.T) {
	for _, tc := range []struct {
		kind, status                       string
		apply, terminal, released, invalid bool
	}{
		{"charge.dispute.created", "needs_response", true, false, false, false},
		{"charge.dispute.updated", "under_review", true, false, false, false},
		{"charge.dispute.closed", "lost", true, true, false, false},
		{"charge.dispute.closed", "won", true, true, true, false},
		{"charge.dispute.updated", "warning_under_review", false, false, false, false},
		{"charge.dispute.closed", "needs_response", false, false, false, true},
		{"charge.dispute.updated", "unknown", false, false, false, true},
	} {
		object := fmt.Sprintf(`{"id":"dp_real","object":"dispute","payment_intent":"pi_real","currency":"usd","amount":400,"status":%q}`, tc.status)
		got, apply, err := stripeReversalFromEvent(stripeReversalTestEvent(t, tc.kind, object))
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.apply, apply)
		require.Equal(t, tc.terminal, got.Terminal)
		require.Equal(t, tc.released, got.Released)
	}
}

func TestStripeReversalWebhookRequiresSignatureAndRetriesFailures(t *testing.T) {
	oldSecret, oldDB := setting.StripeWebhookSecret, model.DB
	setting.StripeWebhookSecret, model.DB = "whsec_reversal_test", nil
	t.Cleanup(func() { setting.StripeWebhookSecret, model.DB = oldSecret, oldDB })
	payload := `{"id":"evt_signed","object":"event","created":100,"type":"charge.refunded","data":{"object":{"id":"ch_real","object":"charge","paid":true,"payment_intent":"pi_real","currency":"usd","amount":1000,"amount_refunded":300}}}`
	sign := func(body string, timestamp int64) string {
		mac := hmac.New(sha256.New, []byte(setting.StripeWebhookSecret))
		_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
		return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
	}
	now := time.Now().Unix()
	for _, tc := range []struct {
		name, body, signature string
		status                int
	}{
		{"missing_signature", payload, "", http.StatusBadRequest},
		{"tampered_body", strings.Replace(payload, "300", "301", 1), sign(payload, now), http.StatusBadRequest},
		{"stale_signature", payload, sign(payload, now-3600), http.StatusBadRequest},
		// Verified cash event reaches the model; unavailable storage is not ACKed.
		{"verified_storage_failure", payload, sign(payload, now), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", strings.NewReader(tc.body))
			ctx.Request.Header.Set("Stripe-Signature", tc.signature)
			StripeWebhook(ctx)
			ctx.Writer.WriteHeaderNow()
			require.Equal(t, tc.status, recorder.Code)
		})
	}
}
