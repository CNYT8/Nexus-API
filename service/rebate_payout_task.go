package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	rebatePayoutTickInterval = 1 * time.Minute
	rebatePayoutBatchSize    = 100
)

var (
	rebatePayoutOnce        sync.Once
	rebatePayoutRunning     atomic.Bool
	rebateMaintenancePaused atomic.Bool
	taskBillingWorkMu       sync.Mutex
	taskBillingWorkCount    int
)

// SetRebateMaintenancePaused pauses or resumes background rebate payout and
// recovery tasks during wallet ledger baseline initialization.
func SetRebateMaintenancePaused(paused bool) {
	taskBillingWorkMu.Lock()
	rebateMaintenancePaused.Store(paused)
	taskBillingWorkMu.Unlock()
}

// BeginTaskBillingWork atomically admits a background accounting operation.
// Admission and pause share one mutex, so a worker cannot enter after a drain
// has observed zero active operations.
func BeginTaskBillingWork() bool {
	taskBillingWorkMu.Lock()
	defer taskBillingWorkMu.Unlock()
	if rebateMaintenancePaused.Load() {
		return false
	}
	taskBillingWorkCount++
	return true
}

func EndTaskBillingWork() {
	taskBillingWorkMu.Lock()
	taskBillingWorkCount--
	taskBillingWorkMu.Unlock()
}

// WaitForRebateTasksIdle waits up to maxWait for any in-flight payout or
// recovery iteration to finish once maintenance pause is enabled.
func WaitForRebateTasksIdle(maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for {
		taskBillingWorkMu.Lock()
		idle := taskBillingWorkCount == 0
		taskBillingWorkMu.Unlock()
		if idle {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// StartRebatePayoutTask processes due scheduled invitation rebates. Immediate
// payouts are committed with their settlement and never reach this task.
func StartRebatePayoutTask() {
	rebatePayoutOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("rebate payout task started: tick=%s", rebatePayoutTickInterval))
			ticker := time.NewTicker(rebatePayoutTickInterval)
			defer ticker.Stop()

			runRebatePayoutOnce()
			for range ticker.C {
				runRebatePayoutOnce()
			}
		})
	})
}

func runRebatePayoutOnce() {
	if !BeginTaskBillingWork() {
		return
	}
	defer EndTaskBillingWork()
	if !rebatePayoutRunning.CompareAndSwap(false, true) {
		return
	}
	defer rebatePayoutRunning.Store(false)

	// 每次调度有固定预算，避免待发记录持续增长时长期占用数据库。
	deadline := time.Now().Add(20 * time.Second)
	for batch := 0; batch < 10 && time.Now().Before(deadline); batch++ {
		if rebateMaintenancePaused.Load() {
			return
		}
		paid, err := model.PayDueWalletRebates(model.DB, time.Now().UTC(), rebatePayoutBatchSize)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("rebate payout task failed: %v", err))
			return
		}
		if paid < rebatePayoutBatchSize {
			return
		}
	}
}
