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

// The process-wide limiter survives -count repetitions; give each invocation
// separate user identities instead of inheriting the preceding run's quota.
var ticketRateLimitTestUsers atomic.Int64

func TestTicketWriteRateLimitIsScopedByUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalEnabled := common.CriticalRateLimitEnable
	originalNum := common.CriticalRateLimitNum
	originalDuration := common.CriticalRateLimitDuration
	originalRedisEnabled := common.RedisEnabled
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 1
	common.CriticalRateLimitDuration = 60
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.CriticalRateLimitEnable = originalEnabled
		common.CriticalRateLimitNum = originalNum
		common.CriticalRateLimitDuration = originalDuration
		common.RedisEnabled = originalRedisEnabled
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		userId, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", userId)
		c.Next()
	})
	router.POST("/tickets", TicketWriteRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := func(userId string) int {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tickets", nil)
		req.Header.Set("X-Test-User", userId)
		req.RemoteAddr = "192.0.2.1:1234"
		router.ServeHTTP(recorder, req)
		return recorder.Code
	}

	base := int64(880000) + ticketRateLimitTestUsers.Add(2)
	first, second := strconv.FormatInt(base, 10), strconv.FormatInt(base+1, 10)
	require.Equal(t, http.StatusNoContent, request(first))
	require.Equal(t, http.StatusTooManyRequests, request(first))
	require.Equal(t, http.StatusNoContent, request(second))
	require.Equal(t, http.StatusUnauthorized, request(""))
}
