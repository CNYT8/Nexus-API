package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const LogCleanupLeaseSeconds int64 = 60

var ErrLogCleanupLeaseLost = errors.New("log cleanup lease lost")
var ErrLogCleanupBusy = errors.New("another log cleanup is running")

// These tables live in LogMetaDB (LOG_DB for relational deployments). Each
// relational deletion locks the lease until deletion and checkpoint commit.
// ClickHouse instead commits an immutable deletion intent before remote I/O.
type LogCleanupLease struct {
	ID         int    `gorm:"primaryKey;autoIncrement:false"`
	Owner      string `gorm:"type:varchar(64);not null;default:''"`
	LeaseUntil int64  `gorm:"not null;default:0"`
	Sequence   int64  `gorm:"not null;default:0"`
}

type LogCleanupCheckpoint struct {
	TaskID        int64 `gorm:"primaryKey;autoIncrement:false"`
	UserID        int
	Target        int64
	Status        string `gorm:"type:varchar(24);not null"`
	Processed     int64
	Revision      int64
	ProgressExact bool
	Error         string `gorm:"type:text"`
	UpdatedAt     int64
	Backend       string `gorm:"type:varchar(24);not null;default:''"`
	StoreBinding  string `gorm:"type:varchar(256);not null;default:''"`
	MutationToken string `gorm:"type:varchar(32);not null;default:''"`
	MutationState string `gorm:"type:varchar(24);not null;default:''"`
	MutationMaxID int64
	MutationTryAt int64
}

func takeLogCleanupLease(tx *gorm.DB, owner string) (bool, error) {
	if owner == "" || len(owner) > 64 {
		return false, errors.New("invalid log cleanup owner")
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&LogCleanupLease{ID: 1}).Error; err != nil {
		return false, err
	}
	now := time.Now().Unix()
	result := tx.Model(&LogCleanupLease{}).Where("id = ? AND (owner = ? OR lease_until <= ?)", 1, "", now).Updates(map[string]any{
		"owner": owner, "lease_until": now + LogCleanupLeaseSeconds, "sequence": gorm.Expr("sequence + 1"),
	})
	return result.RowsAffected == 1, result.Error
}

func guardLogCleanupLease(tx *gorm.DB, owner string) error {
	if owner == "" {
		return ErrLogCleanupLeaseLost
	}
	now := time.Now().Unix()
	result := tx.Model(&LogCleanupLease{}).Where("id = ? AND owner = ? AND lease_until > ?", 1, owner, now).Updates(map[string]any{
		"lease_until": now + LogCleanupLeaseSeconds, "sequence": gorm.Expr("sequence + 1"),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLogCleanupLeaseLost
	}
	return nil
}

func releaseLogCleanupLeaseTx(tx *gorm.DB, owner string) error {
	return tx.Model(&LogCleanupLease{}).Where("id = ? AND owner = ?", 1, owner).Updates(map[string]any{
		"owner": "", "lease_until": 0, "sequence": gorm.Expr("sequence + 1"),
	}).Error
}

func ReleaseLogCleanupLease(ctx context.Context, owner string) error {
	return releaseLogCleanupLeaseTx(logMetaDB().WithContext(ctx), owner)
}

// ClaimLogCleanupLease also recovers pre-lease running tasks. Their historical
// processed count is only a lower bound, marked by ProgressExact=false.
func ClaimLogCleanupLease(ctx context.Context, task *SystemTask, owner string) (*LogCleanupCheckpoint, bool, error) {
	if task == nil || task.ID <= 0 || task.Kind != "log_cleanup" || task.Target <= 0 || task.Processed < 0 || (task.Status != "queued" && task.Status != "running") {
		return nil, false, errors.New("invalid recoverable log cleanup task")
	}
	backend := ""
	if IsClickHouseLogDB() {
		backend = "clickhouse"
	}
	if task.LogBackend != backend {
		return nil, false, errors.New("log cleanup backend changed; reconcile pending tasks before database cutover or rollback")
	}
	var checkpoint *LogCleanupCheckpoint
	claimed := false
	err := logMetaDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		acquired, err := takeLogCleanupLease(tx, owner)
		if err != nil || !acquired {
			return err
		}
		candidate := LogCleanupCheckpoint{
			TaskID: task.ID, UserID: task.UserID, Target: task.Target, Status: "running",
			Processed: task.Processed, Revision: task.ProgressVersion, ProgressExact: task.ProgressExact && backend == "",
			Backend: backend,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		var stored LogCleanupCheckpoint
		if err := tx.First(&stored, "task_id = ?", task.ID).Error; err != nil {
			return err
		}
		if stored.Target != task.Target || stored.UserID != task.UserID || stored.Backend != backend {
			return errors.New("log cleanup checkpoint identity mismatch")
		}
		if stored.Status == "succeeded" || stored.Status == "failed" {
			checkpoint = &stored
			return releaseLogCleanupLeaseTx(tx, owner)
		}
		if stored.Status != "running" {
			return errors.New("invalid log cleanup checkpoint status")
		}
		stored.Revision++
		stored.UpdatedAt = time.Now().Unix()
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		checkpoint, claimed = &stored, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return checkpoint, claimed, nil
}

// RunLogCleanupBatch commits actual RowsAffected with its checkpoint. A rollback
// rolls back both, so retry or a crash before main-DB mirroring cannot overcount.
func RunLogCleanupBatch(ctx context.Context, taskID int64, owner string, limit int) (*LogCleanupCheckpoint, error) {
	if IsClickHouseLogDB() {
		return runClickHouseLogCleanupBatch(ctx, taskID, owner)
	}
	var checkpoint LogCleanupCheckpoint
	err := LOG_DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := guardLogCleanupLease(tx, owner); err != nil {
			return err
		}
		if err := tx.First(&checkpoint, "task_id = ?", taskID).Error; err != nil {
			return err
		}
		if checkpoint.Backend != "" {
			return errors.New("refusing to resume ClickHouse cleanup on a relational log database")
		}
		if checkpoint.Status != "running" {
			return releaseLogCleanupLeaseTx(tx, owner)
		}
		count, err := deleteOldLogBatchTx(tx, checkpoint.Target, limit)
		if err != nil {
			return err
		}
		checkpoint.Processed += count
		checkpoint.Revision++
		checkpoint.UpdatedAt = time.Now().Unix()
		if count == 0 {
			checkpoint.Status = "succeeded"
		}
		if err := tx.Save(&checkpoint).Error; err != nil {
			return err
		}
		if checkpoint.Status != "running" {
			return releaseLogCleanupLeaseTx(tx, owner)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

// The main database is a versioned projection only. Absolute counts and a
// monotonic revision reject delayed mirrors from a former lease owner.
func SyncLogCleanupProgress(ctx context.Context, checkpoint *LogCleanupCheckpoint) error {
	if checkpoint == nil || checkpoint.Revision <= 0 {
		return errors.New("invalid log cleanup checkpoint")
	}
	return DB.WithContext(ctx).Model(&SystemTask{}).
		Where("id = ? AND kind = ? AND user_id = ? AND target = ? AND progress_version < ?", checkpoint.TaskID, "log_cleanup", checkpoint.UserID, checkpoint.Target, checkpoint.Revision).
		Updates(map[string]any{
			"status": checkpoint.Status, "processed": checkpoint.Processed, "error": checkpoint.Error,
			"progress_version": checkpoint.Revision, "progress_exact": checkpoint.ProgressExact, "updated_at": checkpoint.UpdatedAt,
		}).Error
}

func ListRecoverableLogCleanupTasks(ctx context.Context, limit int) ([]SystemTask, error) {
	if limit <= 0 || limit > 16 {
		limit = 8
	}
	var tasks []SystemTask
	err := DB.WithContext(ctx).Where("kind = ? AND status IN ?", "log_cleanup", []string{"queued", "running"}).
		Order("updated_at ASC, id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}

// The legacy synchronous endpoint uses the same LOG_DB lock as background jobs.
// Each batch releases it in its deleting transaction, without a task checkpoint.
func deleteOldLogBatchWithLease(ctx context.Context, target int64, limit int) (int64, error) {
	owner := common.GetUUID()
	var count int64
	err := LOG_DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		acquired, err := takeLogCleanupLease(tx, owner)
		if err != nil {
			return err
		}
		if !acquired {
			return ErrLogCleanupBusy
		}
		count, err = deleteOldLogBatchTx(tx, target, limit)
		if err != nil {
			return err
		}
		return releaseLogCleanupLeaseTx(tx, owner)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
