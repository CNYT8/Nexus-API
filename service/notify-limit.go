package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/bytedance/gopkg/util/gopool"
)

// notifyLimitStore is used for in-memory rate limiting when Redis is disabled
var (
	notifyLimitStore sync.Map
	notifyLimitMutex sync.Mutex
	cleanupOnce      sync.Once
)

type limitCount struct {
	Count     int
	Timestamp time.Time
}

func getDuration() time.Duration {
	minute := constant.NotificationLimitDurationMinute
	return time.Duration(minute) * time.Minute
}

// startCleanupTask starts a background task to clean up expired entries
func startCleanupTask() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Hour)
			now := time.Now()
			notifyLimitMutex.Lock()
			notifyLimitStore.Range(func(key, value interface{}) bool {
				if limit, ok := value.(limitCount); ok {
					if now.Sub(limit.Timestamp) >= getDuration() {
						notifyLimitStore.Delete(key)
					}
				}
				return true
			})
			notifyLimitMutex.Unlock()
		}
	})
}

// CheckNotificationLimit checks if the user has exceeded their notification limit
// Returns true if the user can send notification, false if limit exceeded
func CheckNotificationLimit(userId int, notifyType string) (bool, error) {
	if common.RedisEnabled {
		return checkRedisLimit(userId, notifyType)
	}
	return checkMemoryLimit(userId, notifyType)
}

func checkRedisLimit(userId int, notifyType string) (bool, error) {
	limit := constant.NotifyLimitCount
	duration := getDuration()
	if limit <= 0 || duration <= 0 {
		return false, fmt.Errorf("invalid notification limit configuration")
	}

	key := fmt.Sprintf("notify_limit:%d:%s:%s", userId, notifyType, time.Now().Format("2006010215"))
	const script = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local limit = tonumber(ARGV[1])
if current >= limit then
  return 0
end
current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
if current > limit then
  return 0
end
return 1
`
	allowed, err := common.RDB.Eval(
		context.Background(),
		script,
		[]string{key},
		limit,
		duration.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("failed to update notification limit: %w", err)
	}
	return allowed == 1, nil
}

func checkMemoryLimit(userId int, notifyType string) (bool, error) {
	// Ensure cleanup task is started
	cleanupOnce.Do(startCleanupTask)

	if constant.NotifyLimitCount <= 0 || getDuration() <= 0 {
		return false, fmt.Errorf("invalid notification limit configuration")
	}

	notifyLimitMutex.Lock()
	defer notifyLimitMutex.Unlock()

	key := fmt.Sprintf("%d:%s:%s", userId, notifyType, time.Now().Format("2006010215"))
	now := time.Now()

	// Get current limit count or initialize new one
	var currentLimit limitCount
	if value, ok := notifyLimitStore.Load(key); ok {
		currentLimit = value.(limitCount)
		// Check if the entry has expired
		if now.Sub(currentLimit.Timestamp) >= getDuration() {
			currentLimit = limitCount{Count: 0, Timestamp: now}
		}
	} else {
		currentLimit = limitCount{Count: 0, Timestamp: now}
	}

	// Increment count
	currentLimit.Count++

	// Check against limits
	limit := constant.NotifyLimitCount

	// Store updated count
	notifyLimitStore.Store(key, currentLimit)

	return currentLimit.Count <= limit, nil
}
