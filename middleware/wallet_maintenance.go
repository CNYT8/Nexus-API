package middleware

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	walletMaintenanceActive  atomic.Bool
	inFlightBusinessRequests atomic.Int64
)

func isWalletMaintenanceExemptPath(path string) bool {
	return strings.HasPrefix(path, "/api/option/wallet_ledger") ||
		strings.HasPrefix(path, "/api/option/db_integrity") ||
		path == "/api/status"
}

// BeginWalletMaintenanceGate activates the maintenance gate and waits up to maxWait
// for in-flight non-exempt HTTP requests on this instance to drain.
func BeginWalletMaintenanceGate(maxWait time.Duration) {
	walletMaintenanceActive.Store(true)
	deadline := time.Now().Add(maxWait)
	for inFlightBusinessRequests.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
}

// EndWalletMaintenanceGate reopens normal HTTP request processing.
func EndWalletMaintenanceGate() {
	walletMaintenanceActive.Store(false)
}

// IsWalletMaintenanceActive reports whether the instance is currently in a
// wallet ledger maintenance pause window.
func IsWalletMaintenanceActive() bool {
	return walletMaintenanceActive.Load()
}

// WalletMaintenanceGate rejects new business requests with 503 while the wallet
// credit baseline is being initialized, and tracks in-flight request count so
// the initializer can wait for active requests to drain.
func WalletMaintenanceGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isWalletMaintenanceExemptPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if walletMaintenanceActive.Load() {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "系统正在初始化钱包账本，服务短暂暂停中，请稍后重试",
				"error": gin.H{
					"message": "系统正在初始化钱包账本，服务短暂暂停中，请稍后重试",
					"type":    "service_unavailable",
					"code":    "wallet_maintenance_in_progress",
				},
			})
			return
		}
		inFlightBusinessRequests.Add(1)
		defer inFlightBusinessRequests.Add(-1)
		c.Next()
	}
}
