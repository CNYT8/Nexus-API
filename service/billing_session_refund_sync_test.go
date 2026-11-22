package service

import (
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type blockingRefundFunding struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	err     error
}

func (f *blockingRefundFunding) Source() string       { return BillingSourceWallet }
func (f *blockingRefundFunding) PreConsume(int) error { return nil }
func (f *blockingRefundFunding) Settle(int) error     { return nil }
func (f *blockingRefundFunding) Refund() error {
	f.calls.Add(1)
	close(f.entered)
	<-f.release
	return f.err
}

func TestBillingSessionRefundSyncWaitsForAsynchronousRefund(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	refundErr := errors.New("refund failed")
	funding := &blockingRefundFunding{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		err:     refundErr,
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(funding.release) }) }
	t.Cleanup(release)
	session := &BillingSession{
		relayInfo:     &relaycommon.RelayInfo{IsPlayground: true},
		funding:       funding,
		tokenConsumed: 100,
	}
	session.Refund(ctx)
	select {
	case <-funding.entered:
	case <-time.After(time.Second):
		t.Fatal("异步退款没有开始")
	}
	result := make(chan error, 1)
	go func() { result <- session.RefundSync(ctx) }()
	select {
	case err := <-result:
		t.Fatalf("资金退款尚未结束就返回：%v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case err := <-result:
		require.ErrorIs(t, err, refundErr)
	case <-time.After(time.Second):
		t.Fatal("资金退款结束后仍未返回")
	}
	require.ErrorIs(t, session.RefundSync(ctx), refundErr)
	session.Refund(ctx)
	require.Equal(t, int32(1), funding.calls.Load())
	require.False(t, session.NeedsRefund())
}

func TestBillingSessionRefundSyncStartsRefundOnce(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	funding := &blockingRefundFunding{entered: make(chan struct{}), release: make(chan struct{})}
	close(funding.release)
	session := &BillingSession{
		relayInfo:     &relaycommon.RelayInfo{IsPlayground: true},
		funding:       funding,
		tokenConsumed: 100,
	}
	require.NoError(t, session.RefundSync(ctx))
	require.NoError(t, session.RefundSync(ctx))
	session.Refund(ctx)
	require.Equal(t, int32(1), funding.calls.Load())
}

func TestBillingSessionRefundSyncPreservesSettlementBarrier(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	funding := &failingSettlementFunding{settleErr: errors.New("settlement failed")}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}
	require.Error(t, session.Settle(200))
	require.NoError(t, session.RefundSync(ctx))
	require.Zero(t, funding.refundCalls)
	require.False(t, session.NeedsRefund())
}
