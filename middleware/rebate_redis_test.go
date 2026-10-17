package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

// rebateRedisRateLimitTestUsers keeps repeated -count runs on fresh identities
// even though Redis keys can outlive one test invocation.
var rebateRedisRateLimitTestUsers atomic.Int64

// TestRebateReadRateLimitOnRealRedis proves the Redis-backed implementation of
// RebateReadRateLimit enforces the per-user threshold atomically on a real
// server: user A is allowed exactly rebateReadRateLimitNum times and then gets
// 429, while user B is unaffected. The function selects its implementation at
// construction time, so common.RedisEnabled must be set before it is built.
func TestRebateReadRateLimitOnRealRedis(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not set; skipping real Redis rebate rate limit test")
	}
	gin.SetMode(gin.TestMode)

	oldRDB, oldRedisEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: addr})
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, client.Ping(pingCtx).Err())
	pingCancel()

	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = oldRDB
		common.RedisEnabled = oldRedisEnabled
		require.NoError(t, client.Close())
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		userId, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", userId)
		c.Next()
	})
	router.GET("/rebate", RebateReadRateLimit(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	unique := time.Now().UnixNano()%1_000_000_000 + rebateRedisRateLimitTestUsers.Add(1)
	userA := int(unique)*2 + 1
	userB := userA + 1
	keys := []string{
		fmt.Sprintf("rateLimit:RB:user:%d", userA),
		fmt.Sprintf("rateLimit:RB:user:%d", userB),
	}
	cleanupCtx := context.Background()
	require.NoError(t, client.Del(cleanupCtx, keys...).Err())
	t.Cleanup(func() { _ = client.Del(cleanupCtx, keys...).Err() })

	do := func(userID int) int {
		req := httptest.NewRequest(http.MethodGet, "/rebate", nil)
		req.Header.Set("X-Test-User", strconv.Itoa(userID))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	for i := 0; i < rebateReadRateLimitNum; i++ {
		require.Equal(t, http.StatusOK, do(userA), "request %d should be allowed", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, do(userA))
	require.Equal(t, http.StatusTooManyRequests, do(userA))

	// Exhausting user A's Redis key must not consume user B's budget.
	for i := 0; i < rebateReadRateLimitNum; i++ {
		require.Equal(t, http.StatusOK, do(userB), "victim request %d should be allowed", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, do(userB))

	// The threshold is durable in Redis (a fresh limiter does not reset it),
	// which is what makes the limit survive multiple application instances.
	require.Equal(t, http.StatusTooManyRequests, do(userA))
	require.EqualValues(t, rebateReadRateLimitNum, client.LLen(cleanupCtx, keys[0]).Val())
}
