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
)

// SetRebateMaintenancePaused pauses or resumes background rebate payout and
// recovery tasks during wallet ledger baseline initialization.
func SetRebateMaintenancePaused(paused bool) {
	rebateMaintenancePaused.Store(paused)
}

// WaitForRebateTasksIdle waits up to maxWait for any in-flight payout or
// recovery iteration to finish once maintenance pause is enabled.
func WaitForRebateTasksIdle(maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for (rebatePayoutRunning.Load() || rebateRecoveryRunning.Load()) && time.Now().Before(deadline) {
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
	if rebateMaintenancePaused.Load() {
		return
	}
	if !rebatePayoutRunning.CompareAndSwap(false, true) {
		return
	}
	defer rebatePayoutRunning.Store(false)

	for {
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
