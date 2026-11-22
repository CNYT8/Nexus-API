package controller

import (
	"context"
	"errors"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// watchRealtimeShutdown owns only the client socket. Once the upstream is
// connected, OpenaiRealtimeHandler closes both sockets and joins its readers.
// The returned cleanup joins this watcher before Gin can reuse the context.
func watchRealtimeShutdown(c *gin.Context, ws *websocket.Conn, stopping <-chan struct{}) func() {
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	// Shutdown can begin between Register and Upgrade.
	select {
	case <-stopping:
		cancel()
	default:
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-stopping:
		case <-ctx.Done():
		}
		cancel()
		_ = ws.Close()
	}()
	return func() {
		cancel()
		<-stopped
	}
}

// Use the existing idempotent refund, including its wait for a refund already
// in flight. An asynchronous fallback cannot satisfy ShutdownRequests.done.
func refundRealtimeBilling(c *gin.Context, billing relaycommon.BillingSettler) error {
	refunder, ok := billing.(interface {
		RefundSync(*gin.Context) error
	})
	if !ok {
		return errors.New("Realtime WebSocket requires synchronous billing refunds")
	}
	return refunder.RefundSync(c)
}
