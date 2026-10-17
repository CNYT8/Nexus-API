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
	rebatePayoutOnce    sync.Once
	rebatePayoutRunning atomic.Bool
)

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
	if !rebatePayoutRunning.CompareAndSwap(false, true) {
		return
	}
	defer rebatePayoutRunning.Store(false)

	for {
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
