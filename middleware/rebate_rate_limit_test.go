package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// rebateRateLimitTestUsers keeps each -count repetition on a fresh identity
// because the process-wide in-memory limiter outlives one test invocation.
var rebateRateLimitTestUsers atomic.Int64

// TestRebateReadRateLimitIsScopedByUser proves the rebate read endpoints are
// rate limited per authenticated user and that one user exhausting the limit
// cannot block or exhaust another user's quota.
func TestRebateReadRateLimitIsScopedByUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = originalRedisEnabled })

	router := gin.New()
	router.Use(func(c *gin.Context) {
		userId, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", userId)
		c.Next()
	})
	router.GET("/rebate", RebateReadRateLimit(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	attacker := int(rebateRateLimitTestUsers.Add(1))
	victim := int(rebateRateLimitTestUsers.Add(1))
	do := func(userID int) int {
		req := httptest.NewRequest(http.MethodGet, "/rebate", nil)
		req.Header.Set("X-Test-User", strconv.Itoa(userID))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	for i := 0; i < rebateReadRateLimitNum; i++ {
		require.Equal(t, http.StatusOK, do(attacker), "request %d should be allowed", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, do(attacker))
	require.Equal(t, http.StatusTooManyRequests, do(attacker))

	// Exhausting one user's quota must not affect another authenticated user.
	for i := 0; i < rebateReadRateLimitNum; i++ {
		require.Equal(t, http.StatusOK, do(victim))
	}
	require.Equal(t, http.StatusTooManyRequests, do(victim))
}

// TestRequestIdIsServerGeneratedAndUnspoofable proves the wallet settlement
// identity cannot be chosen by a client: a supplied X-Oneapi-Request-Id header
// is ignored, and every request receives a distinct server-generated value.
func TestRequestIdIsServerGeneratedAndUnspoofable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestId())
	router.GET("/identity", func(c *gin.Context) {
		c.String(http.StatusOK, c.GetString(common.RequestIdKey))
	})

	call := func(spoofed string) (string, string) {
		req := httptest.NewRequest(http.MethodGet, "/identity", nil)
		if spoofed != "" {
			req.Header.Set(common.RequestIdKey, spoofed)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String(), w.Header().Get(common.RequestIdKey)
	}

	first, firstHeader := call("attacker-chosen-identity")
	second, secondHeader := call("attacker-chosen-identity")
	require.NotEqual(t, "attacker-chosen-identity", first)
	require.NotEqual(t, "attacker-chosen-identity", firstHeader)
	require.NotEqual(t, "attacker-chosen-identity", second)
	require.NotEqual(t, "attacker-chosen-identity", secondHeader)
	require.NotEmpty(t, first)
	require.Equal(t, first, firstHeader)
	require.Equal(t, second, secondHeader)
	require.NotEqual(t, first, second)
}
