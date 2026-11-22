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

	"github.com/stretchr/testify/require"
)

func TestGracefulShutdownDrainsBeforeSaving(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
		close(finished)
	})}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var saved atomic.Int32
	var savedBeforeDrain atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- serveHTTPUntilCanceled(ctx, server, listener, 2*time.Second, func() {
			select {
			case <-finished:
			default:
				savedBeforeDrain.Store(true)
			}
			saved.Add(1)
		})
	}()
	responseDone := make(chan error, 1)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		responseDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not enter handler")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before active handler completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	require.Zero(t, saved.Load())
	unblock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	require.Equal(t, int32(1), saved.Load())
	require.False(t, savedBeforeDrain.Load())
	select {
	case err := <-responseDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("request did not complete")
	}
}

func TestGracefulShutdownTimeoutClosesConnectionsAndSaves(t *testing.T) {
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
	var saved atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- serveHTTPUntilCanceled(ctx, server, listener, 30*time.Millisecond, func() { saved.Add(1) })
	}()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not enter handler")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timeout was not enforced")
	}
	require.Equal(t, int32(1), saved.Load())
	select {
	case <-handlerCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("active connection was not closed")
	}
}

func TestGracefulShutdownRejectsInvalidTimeoutBeforeListening(t *testing.T) {
	for _, value := range []string{"0", "-1", "invalid", "9223372036854775807"} {
		t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", value)
		err := runServerWithGracefulShutdown(&http.Server{Addr: "127.0.0.1:0"})
		require.ErrorContains(t, err, "SHUTDOWN_TIMEOUT_SECONDS")
	}
}
