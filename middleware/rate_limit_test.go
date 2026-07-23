package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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

	require.Equal(t, http.StatusNoContent, request("880001"))
	require.Equal(t, http.StatusTooManyRequests, request("880001"))
	require.Equal(t, http.StatusNoContent, request("880002"))
	require.Equal(t, http.StatusUnauthorized, request(""))
}
