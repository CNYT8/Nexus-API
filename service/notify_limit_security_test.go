package service

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func resetMemoryNotificationLimitStore() {
	notifyLimitMutex.Lock()
	defer notifyLimitMutex.Unlock()
	notifyLimitStore.Range(func(key, _ interface{}) bool {
		notifyLimitStore.Delete(key)
		return true
	})
}

func TestMemoryNotificationLimitIsAtomic(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	oldLimit := constant.NotifyLimitCount
	oldDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 3
	constant.NotificationLimitDurationMinute = 10
	resetMemoryNotificationLimitStore()
	t.Cleanup(func() {
		resetMemoryNotificationLimitStore()
		common.RedisEnabled = oldRedisEnabled
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	})

	const attempts = 64
	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			ok, err := CheckNotificationLimit(42, "quota")
			if err != nil {
				t.Errorf("CheckNotificationLimit() error = %v", err)
				return
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != int64(constant.NotifyLimitCount) {
		t.Fatalf("allowed notifications = %d, want %d", got, constant.NotifyLimitCount)
	}
}

func TestMemoryNotificationLimitRejectsInvalidConfiguration(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	oldLimit := constant.NotifyLimitCount
	oldDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 0
	constant.NotificationLimitDurationMinute = 10
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	})

	if ok, err := CheckNotificationLimit(7, "quota"); err == nil || ok {
		t.Fatalf("CheckNotificationLimit() = (%v, %v), want rejection", ok, err)
	}
}
