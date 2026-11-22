package relay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func responsesWSWaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("Responses WebSocket worker did not reach the expected barrier")
	}
}

func responsesWSCanceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestResponsesWSDrainRejectsCreateAdmission(t *testing.T) {
	drain := common.NewRequestDrain()
	s := &responsesWSSession{ctx: context.Background(), drain: drain}
	first := &responsesWSCallState{}
	require.True(t, s.tryReserveCurrent(first))
	require.NotNil(t, first.drainDone)
	require.False(t, s.tryReserveCurrent(&responsesWSCallState{}))
	drain.BeginDrain()
	require.ErrorIs(t, drain.Wait(responsesWSCanceledContext()), context.Canceled)
	s.stateMu.Lock()
	s.current = nil
	s.stateMu.Unlock()
	require.False(t, s.tryReserveCurrent(&responsesWSCallState{}))
	first.drainDone()
	first.drainDone()
	require.NoError(t, drain.Wait(responsesWSCanceledContext()))
}

func TestResponsesWSDrainWaitsForRunnerCompletion(t *testing.T) {
	peer, _ := responsesWSTestSocketPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drain := common.NewRequestDrain()
	s := &responsesWSSession{ctx: ctx, cancel: cancel, client: peer, drain: drain, request: httptest.NewRequest(http.MethodGet, "/v1/responses", nil)}
	state := &responsesWSCallState{done: make(chan struct{})}
	require.True(t, s.tryReserveCurrent(state))
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseRunner := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRunner)
	s.runner = func(*http.Request, string, func(*gin.Context) *types.NewAPIError) *types.NewAPIError {
		close(entered)
		<-release // includes finalization and middleware work after the response
		return nil
	}
	data := []byte(`{"type":"response.create","model":"test"}`)
	event, streamID, err := parseResponsesWSEnvelope(data)
	require.NoError(t, err)
	go func() {
		s.runRequest(state, data, event, streamID, "drain-request")
		close(returned)
	}()
	responsesWSWaitSignal(t, entered)
	drain.BeginDrain()
	require.ErrorIs(t, drain.Wait(responsesWSCanceledContext()), context.Canceled)
	select {
	case <-state.done:
		t.Fatal("request registration ended before the runner completed")
	default:
	}
	releaseRunner()
	responsesWSWaitSignal(t, returned)
	require.NoError(t, drain.Wait(responsesWSCanceledContext()))
	require.False(t, s.tryReserveCurrent(&responsesWSCallState{}))
}

type responsesWSTestSynchronousRefund struct {
	relaycommon.BillingSettler
	entered    chan struct{}
	release    <-chan struct{}
	err        error
	syncCalls  int
	asyncCalls int
}

func (b *responsesWSTestSynchronousRefund) Refund(*gin.Context) { b.asyncCalls++ }

func (b *responsesWSTestSynchronousRefund) RefundSync(*gin.Context) error {
	b.syncCalls++
	close(b.entered)
	<-b.release
	return b.err
}

func TestResponsesWSBillingRequiresSynchronousRefund(t *testing.T) {
	require.True(t, responsesWSBillingReady(), "BillingSession must implement RefundSync(*gin.Context) error before Responses WebSocket is enabled")
}

func TestResponsesWSRefundRejectsAsynchronousBilling(t *testing.T) {
	c, _ := responsesWSTestContext()
	require.NoError(t, responsesWSRefundBilling(c, nil))
	billing := &responsesWSTestSynchronousRefund{}
	// Hide RefundSync behind the old interface to model an asynchronous-only
	// implementation. Calling its Refund would incorrectly release admission.
	legacy := &struct{ relaycommon.BillingSettler }{BillingSettler: billing}
	require.ErrorContains(t, responsesWSRefundBilling(c, legacy), "synchronous billing refunds")
	require.Zero(t, billing.asyncCalls)
}

func TestResponsesWSDrainWaitsForSynchronousRefund(t *testing.T) {
	for _, refundErr := range []error{nil, errors.New("refund persistence failed")} {
		name := "success"
		if refundErr != nil {
			name = "persistence failure"
		}
		t.Run(name, func(t *testing.T) {
			peer, _ := responsesWSTestSocketPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			drain := common.NewRequestDrain()
			s := &responsesWSSession{ctx: ctx, cancel: cancel, client: peer, drain: drain, request: httptest.NewRequest(http.MethodGet, "/v1/responses", nil)}
			state := &responsesWSCallState{done: make(chan struct{})}
			require.True(t, s.tryReserveCurrent(state))
			release, returned := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseRefund := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseRefund)
			billing := &responsesWSTestSynchronousRefund{entered: make(chan struct{}), release: release, err: refundErr}
			var result error
			s.runner = func(*http.Request, string, func(*gin.Context) *types.NewAPIError) *types.NewAPIError {
				c, _ := responsesWSTestContext()
				finalizer := &responsesWSFinalizer{dispatched: true, rejected: true, refund: func() {
					result = responsesWSRefundBilling(c, billing)
				}}
				finalizer.finish()
				finalizer.finish()
				return nil
			}
			data := []byte(`{"type":"response.create","model":"test"}`)
			event, streamID, err := parseResponsesWSEnvelope(data)
			require.NoError(t, err)
			go func() {
				s.runRequest(state, data, event, streamID, "refund-drain-request")
				close(returned)
			}()
			responsesWSWaitSignal(t, billing.entered)
			drain.BeginDrain()
			cancel()
			require.ErrorIs(t, drain.Wait(responsesWSCanceledContext()), context.Canceled)
			select {
			case <-state.done:
				t.Fatal("request completed while its refund was still running")
			default:
			}
			releaseRefund()
			responsesWSWaitSignal(t, returned)
			require.ErrorIs(t, result, refundErr)
			require.Equal(t, 1, billing.syncCalls)
			require.Zero(t, billing.asyncCalls)
			require.NoError(t, drain.Wait(responsesWSCanceledContext()))
		})
	}
}

func TestResponsesWSDrainBetweenRegisterAndUpgrade(t *testing.T) {
	drain := common.NewRequestDrain()
	stopping, done, ok := drain.Register()
	require.True(t, ok)
	defer done()
	drain.BeginDrain()
	peer, client := responsesWSTestSocketPair(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("id", 1)
	c.Set("token_id", 1)
	called := false
	runner := func(*http.Request, string, func(*gin.Context) *types.NewAPIError) *types.NewAPIError {
		called = true
		return nil
	}
	require.Nil(t, ResponsesWebSocketHelper(c, peer, runner, drain, stopping))
	require.False(t, called)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := client.ReadMessage()
	require.Error(t, err)
	done()
	require.NoError(t, drain.Wait(responsesWSCanceledContext()))
}

func TestResponsesWSDrainClosesSocketAndWaitsForActiveWorker(t *testing.T) {
	peer, client := responsesWSTestSocketPair(t)
	drain := common.NewRequestDrain()
	stopping, done, ok := drain.Register()
	require.True(t, ok)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("id", 1)
	c.Set("token_id", 1)
	entered, canceled, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseRunner := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() { releaseRunner(); drain.BeginDrain() })
	runner := func(request *http.Request, _ string, _ func(*gin.Context) *types.NewAPIError) *types.NewAPIError {
		close(entered)
		<-request.Context().Done()
		close(canceled)
		<-release // a cancellation is not completion of settlement/refund
		return nil
	}
	var helperError *types.NewAPIError
	go func() {
		helperError = ResponsesWebSocketHelper(c, peer, runner, drain, stopping)
		done() // mirrors the controller's defer, after helper workers.Wait
		close(returned)
	}()
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"test"}`)))
	responsesWSWaitSignal(t, entered)
	drain.BeginDrain()
	responsesWSWaitSignal(t, canceled)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := client.ReadMessage()
	require.Error(t, err)
	require.ErrorIs(t, drain.Wait(responsesWSCanceledContext()), context.Canceled)
	select {
	case <-returned:
		t.Fatal("helper returned while the request worker was still finalizing")
	default:
	}
	releaseRunner()
	responsesWSWaitSignal(t, returned)
	require.Nil(t, helperError)
	require.NoError(t, drain.Wait(responsesWSCanceledContext()))
}
