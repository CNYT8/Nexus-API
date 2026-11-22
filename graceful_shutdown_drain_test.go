package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGracefulShutdownWaitsForHijackedSettlementBeforeFlush(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	drain := common.NewRequestDrain()
	entered, socketClosed, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseSettlement := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSettlement) }) }
	defer release()
	handlerErrors := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopping, done, ok := drain.Register()
		if !ok {
			http.Error(w, "server shutting down", http.StatusServiceUnavailable)
			return
		}
		defer done()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			handlerErrors <- err
			return
		}
		defer connection.Close()
		close(entered)
		<-stopping
		_ = connection.Close()
		close(socketClosed)
		// Model the deferred worker/finalizer work after socket cancellation.
		<-releaseSettlement
		close(settled)
	})}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var flushed atomic.Int32
	var flushedBeforeSettlement atomic.Bool
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveHTTPUntilCanceledWithDrain(ctx, server, listener, 2*time.Second, drain, func(context.Context) error {
			select {
			case <-settled:
			default:
				flushedBeforeSettlement.Store(true)
			}
			flushed.Add(1)
			return nil
		})
	}()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetWriteDeadline(time.Now().Add(time.Second)))
	_, err = io.WriteString(client, "GET /ws HTTP/1.1\r\nHost: localhost\r\n\r\n")
	require.NoError(t, err)
	select {
	case <-entered:
	case err := <-handlerErrors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not hijack the HTTP connection")
	}
	cancel()
	select {
	case <-socketClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not notify the hijacked connection")
	}
	select {
	case err := <-stopped:
		t.Fatalf("shutdown returned before hijacked settlement finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	require.Zero(t, flushed.Load())
	release()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish after hijacked settlement")
	}
	require.Equal(t, int32(1), flushed.Load())
	require.False(t, flushedBeforeSettlement.Load())
}

func TestGracefulShutdownNotifiesRegisteredWorkBeforeHTTPWait(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	drain := common.NewRequestDrain()
	stopping, pendingDone, ok := drain.Register()
	require.True(t, ok)
	defer pendingDone()
	entered := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-stopping:
			pendingDone()
		case <-r.Context().Done():
		}
	})}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveHTTPUntilCanceledWithDrain(ctx, server, listener, time.Second, drain, nil)
	}()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter handler")
	}
	cancel()
	select {
	case err := <-stopped:
		require.NoError(t, err, "notifying after HTTP drain would consume its deadline")
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP handler could not observe the drain notification")
	}
}

func TestGracefulShutdownRegisteredDrainUsesRemainingBudget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	drain := common.NewRequestDrain()
	stopping, pendingDone, ok := drain.Register()
	require.True(t, ok)
	defer pendingDone()
	entered, requestCanceled := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(requestCanceled)
	})}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flushContextErrors := make(chan error, 1)
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveHTTPUntilCanceledWithDrain(ctx, server, listener, 30*time.Millisecond, drain, func(flushCtx context.Context) error {
			flushContextErrors <- flushCtx.Err()
			return flushCtx.Err()
		})
	}()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter handler")
	}
	cancel()
	select {
	case <-stopping:
		// Notification must not be deferred until after HTTP consumes the budget.
	case <-time.After(time.Second):
		t.Fatal("registered work did not receive shutdown notification")
	}
	select {
	case err := <-stopped:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.ErrorContains(t, err, "drain registered requests during shutdown")
	case <-time.After(time.Second):
		t.Fatal("registered drain exceeded the shared shutdown deadline")
	}
	require.ErrorIs(t, <-flushContextErrors, context.DeadlineExceeded)
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP timeout did not close the active connection")
	}
	// Timing out must not pretend the unresponsive registered worker settled.
	canceled, stopWait := context.WithCancel(context.Background())
	stopWait()
	require.ErrorIs(t, drain.Wait(canceled), context.Canceled)
	pendingDone()
	require.NoError(t, drain.Wait(context.Background()))
}

func TestGracefulShutdownRegisteredDrainDoesNotGetExtraBudget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	drain := common.NewRequestDrain()
	_, pendingDone, ok := drain.Register()
	require.True(t, ok)
	defer pendingDone()
	server := &http.Server{Handler: http.NotFoundHandler()}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	flushContextErrors := make(chan error, 1)
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveHTTPUntilCanceledWithDrain(ctx, server, listener, 20*time.Millisecond, drain, func(flushCtx context.Context) error {
			flushContextErrors <- flushCtx.Err()
			return flushCtx.Err()
		})
	}()
	select {
	case err := <-stopped:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.ErrorContains(t, err, "drain registered requests during shutdown")
	case <-time.After(time.Second):
		t.Fatal("registered drain did not honor the deadline after fast HTTP shutdown")
	}
	require.ErrorIs(t, <-flushContextErrors, context.DeadlineExceeded)
}
