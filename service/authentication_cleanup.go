package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var authenticationCleanupOnce sync.Once

// StartAuthenticationCleanupTask runs after schema migration. Expired session
// tombstones retain the model's full legacy-cookie replay protection window.
func StartAuthenticationCleanupTask() {
	authenticationCleanupOnce.Do(func() {
		stopping, done, ok := common.ShutdownRequests.Register()
		if !ok {
			return
		}
		go func() {
			defer done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				select {
				case <-stopping:
					cancel()
				case <-ctx.Done():
				}
			}()
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				passCtx, passCancel := context.WithTimeout(ctx, 5*time.Second)
				err := model.DeleteExpiredAuthenticationStateWithContext(passCtx, time.Now())
				passCancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					common.SysError("authentication cleanup: " + err.Error())
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}
