package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// SystemTask is the main-DB view of maintenance work. Relational deletion and
// its checkpoint commit together; ClickHouse uses a durable mutation intent.
// ProgressVersion fences stale mirrors. LogBackend prevents unsafe DSN rollback.
type SystemTask struct {
	ID              int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Kind            string `json:"kind" gorm:"type:varchar(48);not null;index"`
	Status          string `json:"status" gorm:"type:varchar(24);not null;index"`
	UserID          int    `json:"user_id" gorm:"index"`
	Target          int64  `json:"target"`
	Processed       int64  `json:"processed"`
	Error           string `json:"error,omitempty" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	ProgressVersion int64  `json:"progress_version" gorm:"not null;default:0"`
	ProgressExact   bool   `json:"progress_exact" gorm:"not null;default:false"`
	LogBackend      string `json:"-" gorm:"type:varchar(24);not null;default:''"`
}

func CreateLogCleanupTask(userID int, target int64) (*SystemTask, error) {
	if userID <= 0 || target <= 0 || target > time.Now().Unix() {
		return nil, errors.New("invalid cleanup target")
	}
	task := &SystemTask{Kind: "log_cleanup", Status: "queued", UserID: userID, Target: target, ProgressExact: !IsClickHouseLogDB()}
	if IsClickHouseLogDB() {
		task.LogBackend = "clickhouse"
	}
	err := DB.Create(task).Error
	return task, err
}

func GetSystemTask(id int64) (*SystemTask, error) {
	var task SystemTask
	err := DB.First(&task, id).Error
	return &task, err
}

// DeleteOldLogBatch selects IDs before DELETE. DELETE LIMIT is not portable to
// PostgreSQL/SQLite. Each statement uses a bounded parameter list on all SQL DBs.
func DeleteOldLogBatch(ctx context.Context, target int64, limit int) (int64, error) {
	if IsClickHouseLogDB() {
		return 0, errors.New("ClickHouse log cleanup requires a durable asynchronous task")
	}
	return deleteOldLogBatchWithLease(ctx, target, limit)
}

func deleteOldLogBatchTx(tx *gorm.DB, target int64, limit int) (int64, error) {
	if target <= 0 {
		return 0, errors.New("invalid cleanup target")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var ids []int
	if err := tx.Model(&Log{}).Where("created_at < ?", target).Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := tx.Where("id IN ? AND created_at < ?", ids, target).Delete(&Log{})
	return result.RowsAffected, result.Error
}
