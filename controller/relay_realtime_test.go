package controller

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRealtimeControllerUpgradeDrainAdmission(t *testing.T) {
	for _, draining := range []bool{false, true} {
		name := "failed upgrade releases registration"
		if draining {
			name = "drain rejects before upgrade"
		}
		t.Run(name, func(t *testing.T) {
			original := common.ShutdownRequests
			drain := common.NewRequestDrain()
			common.ShutdownRequests = drain
			t.Cleanup(func() { common.ShutdownRequests = original })
			if draining {
				drain.BeginDrain()
			}
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
			// No Upgrade headers: an admitted request fails the handshake.
			Relay(c, types.RelayFormatOpenAIRealtime)
			if draining {
				require.Equal(t, http.StatusServiceUnavailable, writer.Code)
				require.True(t, c.IsAborted())
				require.Contains(t, writer.Body.String(), "server is shutting down")
			} else {
				require.Equal(t, http.StatusBadRequest, writer.Code)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			// A completed registration needs no remaining shutdown budget.
			require.NoError(t, drain.Wait(ctx))
		})
	}
}

func realtimeControllerSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		if conn, err := upgrader.Upgrade(w, r, nil); err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })
	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
		return conn, peer
	case <-time.After(3 * time.Second):
		t.Fatal("websocket upgrade did not complete")
		return nil, nil
	}
}

func TestRealtimeShutdownWatcherCancelsAndCloses(t *testing.T) {
	for _, trigger := range []string{"drain", "already draining", "request cancellation", "cleanup"} {
		t.Run(trigger, func(t *testing.T) {
			conn, peer := realtimeControllerSocketPair(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
			stopping := make(chan struct{})
			if trigger == "already draining" {
				close(stopping)
			}
			cleanup := watchRealtimeShutdown(c, conn, stopping)
			defer cleanup()
			switch trigger {
			case "drain":
				close(stopping)
			case "request cancellation":
				cancel()
			case "cleanup":
				cleanup()
			}
			select {
			case <-c.Request.Context().Done():
			case <-time.After(3 * time.Second):
				t.Fatal("watcher did not cancel the request")
			}
			require.NoError(t, peer.SetReadDeadline(time.Now().Add(3*time.Second)))
			_, _, err := peer.ReadMessage()
			require.Error(t, err)
			var netErr net.Error
			if errors.As(err, &netErr) {
				require.False(t, netErr.Timeout(), "socket remained open until its deadline")
			}
		})
	}
}

type realtimeRefundProbe struct {
	relaycommon.BillingSettler
	started, release chan struct{}
	asyncCalled      bool
}

func (p *realtimeRefundProbe) NeedsRefund() bool   { return false }
func (p *realtimeRefundProbe) Refund(*gin.Context) { p.asyncCalled = true }
func (p *realtimeRefundProbe) RefundSync(*gin.Context) error {
	close(p.started)
	<-p.release
	return nil
}

func TestRealtimeRefundRemainsRegisteredUntilSynchronousCompletion(t *testing.T) {
	drain := common.NewRequestDrain()
	_, done, ok := drain.Register()
	require.True(t, ok)
	probe := &realtimeRefundProbe{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(probe.release) }) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	returned := make(chan error, 1)
	go func() {
		err := func() error {
			defer done()
			return refundRealtimeBilling(c, probe)
		}()
		returned <- err
	}()
	select {
	case <-probe.started:
	case <-time.After(3 * time.Second):
		t.Fatal("RefundSync was skipped when NeedsRefund returned false")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, drain.Wait(ctx), context.Canceled)
	release.Do(func() { close(probe.release) })
	select {
	case err := <-returned:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("refund did not finish")
	}
	require.False(t, probe.asyncCalled)
	require.NoError(t, drain.Wait(ctx))
}
