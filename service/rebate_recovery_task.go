package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	rebateRecoveryTickInterval = 1 * time.Minute
	rebateRecoveryBatchSize    = 100

	// A reservation debits the wallet immediately. Reservations left behind by a
	// crashed process must be refunded, but only after a TTL that no legitimate
	// request (including streaming) can exceed.
	rebateStaleReservationTTL       = 72 * time.Hour
	rebateReservationExpiryBatch    = 200
	rebateReservationExpiryInterval = 1 * time.Hour
)

var (
	rebateRecoveryOnce    sync.Once
	rebateRecoveryRunning atomic.Bool
)

// StartRebateRecoveryTask replays wallet settlements that committed their
// non-refundable finalizing barrier but crashed before the final accounting
// transaction committed. Replaying is safe because finalization is keyed by the
// durable reservation identity and any completed settlement is returned as-is.
//
// Token quota adjustments are request-local and cannot be reconstructed here;
// this task only guarantees the wallet debit, source-lot consumption, and
// invitation rebate are eventually consistent.
func StartRebateRecoveryTask() {
	rebateRecoveryOnce.Do(func() {
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("rebate settlement recovery task started: tick=%s", rebateRecoveryTickInterval))
			ticker := time.NewTicker(rebateRecoveryTickInterval)
			defer ticker.Stop()
			expiryTicker := time.NewTicker(rebateReservationExpiryInterval)
			defer expiryTicker.Stop()

			runRebateRecoveryOnce()
			runStaleReservationExpiryOnce()
			for {
				select {
				case <-ticker.C:
					runRebateRecoveryOnce()
				case <-expiryTicker.C:
					runStaleReservationExpiryOnce()
				}
			}
		})
	})
}

func runStaleReservationExpiryOnce() {
	refunded, err := model.ExpireStaleWalletReservations(model.DB, time.Now().UTC().Add(-rebateStaleReservationTTL), rebateReservationExpiryBatch)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("rebate stale reservation expiry failed: %v", err))
		return
	}
	if refunded > 0 {
		logger.LogInfo(context.Background(), fmt.Sprintf("rebate stale reservations refunded: %d", refunded))
	}
}

func runRebateRecoveryOnce() {
	if !rebateRecoveryRunning.CompareAndSwap(false, true) {
		return
	}
	defer rebateRecoveryRunning.Store(false)

	for {
		processed, err := model.RecoverFinalizingWalletSettlements(model.DB, rebateRecoveryBatchSize)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("rebate settlement recovery task failed: %v", err))
			return
		}
		if processed < rebateRecoveryBatchSize {
			return
		}
	}
}
