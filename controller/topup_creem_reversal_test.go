package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const creemRefundTestPayload = `{"id":"evt_refund","eventType":"refund.created","created_at":1728734351631,"object":{"id":"ref_verified","object":"refund","status":"succeeded","refund_amount":300,"refund_currency":"USD","metadata":{"trade_no":"untrusted_order"},"transaction":{"id":"tran_verified","order":"ord_verified","amount_paid":1000,"currency":"USD","status":"partially_refunded","refunded_amount":400},"order":{"id":"ord_verified","transaction":"tran_verified","amount_paid":1000,"currency":"USD"}}}`

func creemRefundTestEvent(t *testing.T) CreemWebhookEvent {
	t.Helper()
	var event CreemWebhookEvent
	require.NoError(t, json.Unmarshal([]byte(creemRefundTestPayload), &event))
	return event
}

func TestCreemReversalUsesCashIdentityAndSeparateRefundRepresentations(t *testing.T) {
	event := creemRefundTestEvent(t)
	reversals, err := creemReversalsFromEvent(&event)
	require.NoError(t, err)
	require.Len(t, reversals, 2)
	total, item := reversals[0], reversals[1]
	for _, reversal := range reversals {
		require.Equal(t, model.PaymentProviderCreem, reversal.Provider)
		require.Equal(t, "ord_verified", reversal.PaymentID)
		require.Equal(t, "USD", reversal.Currency)
		require.EqualValues(t, 1000, reversal.OriginalMinor)
		require.EqualValues(t, 1728734351, reversal.OccurredAt)
		require.True(t, reversal.Terminal)
		require.False(t, reversal.Released)
	}
	require.Equal(t, "refund_total", total.Kind)
	require.Equal(t, "tran_verified", total.ObjectID)
	require.EqualValues(t, 400, total.AmountMinor)
	require.Equal(t, "refund_item", item.Kind)
	require.Equal(t, "ref_verified", item.ObjectID)
	require.EqualValues(t, 300, item.AmountMinor)
	require.NotEqual(t, total.EventID, item.EventID)
	replayed, err := creemReversalsFromEvent(&event)
	require.NoError(t, err)
	require.Equal(t, reversals, replayed)
	// 渠道未提供累计数时，仍只用稳定退款对象记录这一笔。
	event.Object.Transaction.RefundedAmount = nil
	reversals, err = creemReversalsFromEvent(&event)
	require.NoError(t, err)
	require.Equal(t, []model.VerifiedTopUpReversal{item}, reversals)
}

func TestCreemReversalRejectsUnprovenIdentityAndAmounts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CreemWebhookEvent)
	}{
		{"missing_payment_identity", func(e *CreemWebhookEvent) { e.Object.Transaction.Order = "" }},
		{"mismatched_order", func(e *CreemWebhookEvent) { e.Object.Order.Id = "ord_other" }},
		{"mismatched_transaction", func(e *CreemWebhookEvent) { e.Object.Order.Transaction = "tran_other" }},
		{"mismatched_original_amount", func(e *CreemWebhookEvent) { e.Object.Order.AmountPaid++ }},
		{"mismatched_currency", func(e *CreemWebhookEvent) { e.Object.RefundCurrency = "EUR" }},
		{"not_succeeded", func(e *CreemWebhookEvent) { e.Object.Status = "pending" }},
		{"zero_refund", func(e *CreemWebhookEvent) { e.Object.RefundAmount = 0 }},
		{"excess_refund", func(e *CreemWebhookEvent) { e.Object.RefundAmount = 1001 }},
		{"total_below_item", func(e *CreemWebhookEvent) { *e.Object.Transaction.RefundedAmount = 299 }},
		{"total_above_paid", func(e *CreemWebhookEvent) { *e.Object.Transaction.RefundedAmount = 1001 }},
		{"missing_event_identity", func(e *CreemWebhookEvent) { e.Id = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := creemRefundTestEvent(t)
			tc.mutate(&event)
			reversals, err := creemReversalsFromEvent(&event)
			require.ErrorIs(t, err, model.ErrTopUpReversalConflict)
			require.Empty(t, reversals)
		})
	}
}

func TestCreemReversalDisputeDoesNotBecomeARefund(t *testing.T) {
	event := creemRefundTestEvent(t)
	event.EventType = "dispute.created"
	event.Object.Id, event.Object.Object = "disp_verified", "dispute"
	event.Object.Amount, event.Object.Currency = 600, "USD"
	event.Object.Transaction.Status = "chargeback"
	// 拒付事件的 refunded_amount 不证明发生了另一次退款。
	*event.Object.Transaction.RefundedAmount = 1000
	reversals, err := creemReversalsFromEvent(&event)
	require.NoError(t, err)
	require.Len(t, reversals, 1)
	require.Equal(t, "dispute", reversals[0].Kind)
	require.Equal(t, "disp_verified", reversals[0].ObjectID)
	require.EqualValues(t, 600, reversals[0].AmountMinor)
	require.False(t, reversals[0].Terminal)
	require.False(t, reversals[0].Released)
	event.Object.Transaction.Status = "paid"
	_, err = creemReversalsFromEvent(&event)
	require.ErrorIs(t, err, model.ErrTopUpReversalConflict)
}

func TestCreemReversalWebhookRequiresSignatureAndRetriesFailures(t *testing.T) {
	oldSecret, oldDB := setting.CreemWebhookSecret, model.DB
	setting.CreemWebhookSecret, model.DB = "creem_reversal_test", nil
	t.Cleanup(func() { setting.CreemWebhookSecret, model.DB = oldSecret, oldDB })
	signature := generateCreemSignature(creemRefundTestPayload, setting.CreemWebhookSecret)
	for _, tc := range []struct {
		name, body, signature string
		status                int
	}{
		{"missing_signature", creemRefundTestPayload, "", http.StatusUnauthorized},
		{"tampered_body", strings.Replace(creemRefundTestPayload, "300", "301", 1), signature, http.StatusUnauthorized},
		{"verified_storage_failure", creemRefundTestPayload, signature, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/creem/webhook", strings.NewReader(tc.body))
			ctx.Request.Header.Set(CreemSignatureHeader, tc.signature)
			CreemWebhook(ctx)
			ctx.Writer.WriteHeaderNow()
			require.Equal(t, tc.status, recorder.Code)
		})
	}
}
