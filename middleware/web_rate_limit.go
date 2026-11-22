package middleware

import (
	"net/http"
	"path"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// FrontendRateLimit keeps static requests out of the page navigation bucket.
// Existing GLOBAL_WEB_RATE_LIMIT* settings still apply to pages and missing files.
// GLOBAL_STATIC_RATE_LIMIT_ENABLE defaults to false, as in upstream; when enabled
// GLOBAL_STATIC_RATE_LIMIT defaults to 1000 per GLOBAL_STATIC_RATE_LIMIT_DURATION
// seconds (180 by default). Redis uses a separate atomic list, not the legacy GW key.
func FrontendRateLimit(frontendFS static.ServeFileSystem) gin.HandlerFunc {
	return frontendRateLimitFor(func(*gin.Context) static.ServeFileSystem { return frontendFS })
}

func frontendRateLimitFor(selectFS func(*gin.Context) static.ServeFileSystem) gin.HandlerFunc {
	web := newFrontendRateBucket(common.GlobalWebRateLimitEnable,
		common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "web")
	assets := newFrontendRateBucket(common.GetEnvOrDefaultBool("GLOBAL_STATIC_RATE_LIMIT_ENABLE", false),
		common.GetEnvOrDefault("GLOBAL_STATIC_RATE_LIMIT", 1000),
		int64(common.GetEnvOrDefault("GLOBAL_STATIC_RATE_LIMIT_DURATION", 180)), "static")
	return func(c *gin.Context) {
		limiter := web
		if frontendFileExists(selectFS(c), c.Request) {
			limiter = assets
		}
		limiter(c)
	}
}

func newFrontendRateBucket(enabled bool, limit int, duration int64, bucket string) gin.HandlerFunc {
	if !enabled {
		return func(c *gin.Context) {}
	}
	// A bad deployment value must not produce a zero-length queue panic.
	if limit <= 0 || duration <= 0 || duration > int64((1<<63-1)/time.Second) {
		common.SysError("Invalid frontend rate limit configuration for " + bucket)
		return func(c *gin.Context) { c.AbortWithStatus(http.StatusServiceUnavailable) }
	}
	expiration := max(common.RateLimitKeyExpirationDuration, time.Duration(duration)*time.Second)
	var memory common.InMemoryRateLimiter
	if !common.RedisEnabled {
		memory.Init(expiration)
		return func(c *gin.Context) {
			if !memory.Request(c.ClientIP(), limit, duration) {
				c.AbortWithStatus(http.StatusTooManyRequests)
			}
		}
	}
	return func(c *gin.Context) {
		allowed, err := common.RDB.Eval(c.Request.Context(), frontendRateLimitScript,
			[]string{"rateLimit:frontend:" + bucket + ":" + c.ClientIP()},
			limit, time.Now().UnixMilli(), duration*1000, expiration.Milliseconds()).Int()
		if err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		if allowed != 1 {
			c.AbortWithStatus(http.StatusTooManyRequests)
		}
	}
}

const frontendRateLimitScript = `
local count = redis.call('LLEN', KEYS[1])
if count >= tonumber(ARGV[1]) then
  local oldest = tonumber(redis.call('LINDEX', KEYS[1], -1))
  if oldest and tonumber(ARGV[2]) - oldest < tonumber(ARGV[3]) then
    redis.call('PEXPIRE', KEYS[1], ARGV[4])
    return 0
  end
end
redis.call('LPUSH', KEYS[1], ARGV[2])
redis.call('LTRIM', KEYS[1], 0, tonumber(ARGV[1]) - 1)
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1
`

func frontendFileExists(frontendFS static.ServeFileSystem, request *http.Request) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	// Match ServeFrontendFiles normalization so one asset cannot switch buckets.
	name := path.Clean("/" + request.URL.Path)
	if name == "/" {
		return false
	}
	file, err := frontendFS.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	return err == nil && !info.IsDir()
}
