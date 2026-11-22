package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGracefulShutdownPropagatesFlushError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	server := &http.Server{Handler: http.NotFoundHandler()}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	injected := errors.New("quota database unavailable")
	calls := 0
	err = serveHTTPUntilCanceledWithFlush(ctx, server, listener, time.Second, func(flushCtx context.Context) error {
		calls++
		require.NoError(t, flushCtx.Err(), "the signal context must not cancel the flush context")
		deadline, ok := flushCtx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now(), deadline, time.Second)
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.Equal(t, 1, calls)
}

func TestGracefulShutdownBoundsFlushAfterFastDrain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	server := &http.Server{Handler: http.NotFoundHandler()}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveHTTPUntilCanceledWithFlush(ctx, server, listener, 30*time.Millisecond, func(flushCtx context.Context) error {
			<-flushCtx.Done()
			return flushCtx.Err()
		})
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("quota flush outlived the shutdown deadline")
	}
}

func TestGracefulShutdownDoesNotRestartBudgetAfterDrainTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	entered, handlerCanceled := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(handlerCanceled)
	})}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flushContexts := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- serveHTTPUntilCanceledWithFlush(ctx, server, listener, 30*time.Millisecond, func(flushCtx context.Context) error {
			flushContexts <- flushCtx.Err()
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
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after the shared deadline")
	}
	require.ErrorIs(t, <-flushContexts, context.DeadlineExceeded)
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed-out HTTP connection was not closed")
	}
}
