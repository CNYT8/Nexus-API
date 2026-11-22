package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// runServerWithGracefulShutdown replaces gin.Engine.Run in main. Return from
// main after logging errors so its deferred model.CloseDB still runs.
func runServerWithGracefulShutdown(server *http.Server) error {
	timeout := 120 * time.Second
	if raw := os.Getenv("SHUTDOWN_TIMEOUT_SECONDS"); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds <= 0 || seconds > int64((1<<63-1)/time.Second) {
			return fmt.Errorf("SHUTDOWN_TIMEOUT_SECONDS must be a positive duration in seconds")
		}
		timeout = time.Duration(seconds) * time.Second
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if os.Getenv("NEXUS_SHUTDOWN_STDIN") == "1" {
		// Windows cannot deliver POSIX SIGTERM via Node's child.kill. Electron
		// opts into this private parent-child pipe; it exposes no network route.
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				if scanner.Text() == "shutdown" {
					stop()
					return
				}
			}
			// Losing the parent pipe also stops an orphaned desktop backend.
			stop()
		}()
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	return serveHTTPUntilCanceledWithFlush(ctx, server, listener, timeout, func(flushCtx context.Context) error {
		if common.DataExportEnabled {
			return model.FlushQuotaDataCache(flushCtx)
		}
		return nil
	})
}

// Keep the existing internal callback contract for callers that need no DB work.
func serveHTTPUntilCanceled(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration, afterShutdown func()) error {
	return serveHTTPUntilCanceledWithFlush(ctx, server, listener, timeout, func(context.Context) error {
		if afterShutdown != nil {
			afterShutdown()
		}
		return nil
	})
}

func serveHTTPUntilCanceledWithFlush(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration, flush func(context.Context) error) error {
	return serveHTTPUntilCanceledWithDrain(ctx, server, listener, timeout, common.ShutdownRequests, flush)
}

func serveHTTPUntilCanceledWithDrain(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration, requests *common.RequestDrain, flush func(context.Context) error) error {
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	var serveErr error
	select {
	case serveErr = <-serveErrors:
	case <-ctx.Done():
		common.SysLog(fmt.Sprintf("HTTP shutdown requested; waiting up to %s for active requests", timeout))
	}

	// HTTP, registered sessions and quota flushing share one deadline. Do not
	// derive it from the canceled signal context or restart it between phases.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Stop WS admission immediately, before HTTP drain. Shutdown does not wait
	// for hijacked sockets, so their owners cancel/close them and acknowledge
	// completion only after their request workers have finished settlement.
	requests.BeginDrain()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		common.SysError("HTTP graceful shutdown: " + shutdownErr.Error())
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	var drainErr error
	if err := requests.Wait(shutdownCtx); err != nil {
		drainErr = fmt.Errorf("drain registered requests during shutdown: %w", err)
		common.SysError(drainErr.Error())
	}
	var flushErr error
	if flush != nil {
		if err := flush(shutdownCtx); err != nil {
			flushErr = fmt.Errorf("flush quota data during shutdown: %w", err)
			common.SysError(flushErr.Error())
		}
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr, drainErr, flushErr)
}
