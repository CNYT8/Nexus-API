package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var logCleanupRunnerOnce sync.Once
var logCleanupWorkerMu sync.Mutex

// StartLogCleanupTaskRunner must be called after both databases and all cleanup
// tables are ready. Every instance may start it; the relational metadata lease
// serializes workers. Each pass is bounded and resumes durable checkpoints.
func StartLogCleanupTaskRunner() {
	logCleanupRunnerOnce.Do(func() {
		go func() {
			runLogCleanupRecoveryPass()
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				runLogCleanupRecoveryPass()
			}
		}()
	})
}

func runLogCleanupRecoveryPass() {
	if !logCleanupWorkerMu.TryLock() {
		return
	}
	defer logCleanupWorkerMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	tasks, err := model.ListRecoverableLogCleanupTasks(ctx, 8)
	cancel()
	if err != nil {
		common.SysError("list log cleanup tasks: " + err.Error())
		return
	}
	// One task per pass prevents a large historical queue from monopolizing DB.
	if len(tasks) > 0 {
		runLogCleanupTask(&tasks[0])
	}
}

// RunLogCleanupTask starts promptly from the HTTP endpoint. Failure to acquire
// the local or database lease leaves durable queued work for the next scan.
func RunLogCleanupTask(id int64) {
	if !logCleanupWorkerMu.TryLock() {
		return
	}
	defer logCleanupWorkerMu.Unlock()
	task, err := model.GetSystemTask(id)
	if err != nil {
		common.SysError("read log cleanup task: " + err.Error())
		return
	}
	if task.Kind == "log_cleanup" && (task.Status == "queued" || task.Status == "running") {
		runLogCleanupTask(task)
	}
}

func runLogCleanupTask(task *model.SystemTask) {
	owner := common.GetUUID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	checkpoint, claimed, err := model.ClaimLogCleanupLease(ctx, task, owner)
	cancel()
	if err != nil {
		common.SysError("claim log cleanup lease: " + err.Error())
		return
	}
	if claimed {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := model.ReleaseLogCleanupLease(ctx, owner); err != nil {
				common.SysError("release log cleanup lease: " + err.Error())
			}
		}()
	}
	if checkpoint != nil && !syncLogCleanupProgress(checkpoint) {
		return
	}
	if !claimed {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for batch := 0; batch < 20 && time.Now().Before(deadline); batch++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		checkpoint, err = model.RunLogCleanupBatch(ctx, task.ID, owner, 100)
		cancel()
		if err != nil {
			if !errors.Is(err, model.ErrLogCleanupLeaseLost) {
				common.SysError("log cleanup batch, retry on next scan: " + err.Error())
			}
			return
		}
		if !syncLogCleanupProgress(checkpoint) || checkpoint.Status != "running" {
			return
		}
		if model.IsClickHouseLogDB() {
			// Mutations progress on the server. Poll once per recovery pass;
			// never spend the relational batch loop repeatedly submitting ALTER.
			return
		}
	}
}

func syncLogCleanupProgress(checkpoint *model.LogCleanupCheckpoint) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := model.SyncLogCleanupProgress(ctx, checkpoint); err != nil {
		common.SysError("persist log cleanup progress: " + err.Error())
		return false
	}
	return true
}
