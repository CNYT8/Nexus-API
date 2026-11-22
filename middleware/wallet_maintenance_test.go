package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWalletMaintenanceGateTimeoutAndDrain(t *testing.T) {
	EndWalletMaintenanceGate()
	t.Cleanup(EndWalletMaintenanceGate)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r := gin.New()
	r.Use(WalletMaintenanceGate())
	r.GET("/business", func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusOK)
	})
	r.GET("/api/status", func(c *gin.Context) { c.Status(http.StatusOK) })
	go func() {
		defer close(done)
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/business", nil))
	}()
	<-entered
	// Keep cleanup safe even if an assertion fails before the request drains.
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
			<-done
		}
	})
	require.False(t, BeginWalletMaintenanceGate(0))
	require.True(t, IsWalletMaintenanceActive())
	blocked := httptest.NewRecorder()
	r.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/business", nil))
	require.Equal(t, http.StatusServiceUnavailable, blocked.Code)
	status := httptest.NewRecorder()
	r.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	require.Equal(t, http.StatusOK, status.Code)
	close(release)
	<-done
	released = true
	require.True(t, BeginWalletMaintenanceGate(0))
	EndWalletMaintenanceGate()
	require.False(t, IsWalletMaintenanceActive())
	require.Zero(t, inFlightBusinessRequests.Load())
}
