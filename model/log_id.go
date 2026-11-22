package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxLogID int64 = 1<<63 - 1

// LogIDSequence is relational. A committed allocation may leave a gap after a
// failed insert, but it must never be reused. Never reset this row on cutover.
type LogIDSequence struct {
	ID       int    `gorm:"primaryKey;autoIncrement:false"`
	LastID   int64  `gorm:"not null"`
	Revision int64  `gorm:"not null"`
	Binding  string `gorm:"type:varchar(256);not null"`
}

func initializeLogIDSequence(ctx context.Context, binding string, floor int64, master bool) error {
	var stats struct {
		MaxID   int64  `gorm:"column:max_id"`
		Invalid uint64 `gorm:"column:invalid"`
		Total   uint64 `gorm:"column:total"`
		Unique  uint64 `gorm:"column:unique_ids"`
	}
	if err := LOG_DB.WithContext(ctx).Raw("SELECT max(id) AS max_id, countIf(id <= 0) AS invalid, count() AS total, uniqExact(id) AS unique_ids FROM logs").Scan(&stats).Error; err != nil {
		return err
	}
	if stats.Invalid != 0 || stats.Total != stats.Unique {
		return errors.New("ClickHouse logs contain zero, negative or duplicate IDs; repair historical data before activation")
	}
	if stats.MaxID > floor {
		floor = stats.MaxID
	}
	if floor >= maxLogID {
		return errors.New("log ID sequence exhausted")
	}
	if !master {
		var sequence LogIDSequence
		if err := LogMetaDB.WithContext(ctx).First(&sequence, 1).Error; err != nil {
			return err
		}
		if sequence.Binding != binding || sequence.LastID < floor {
			return errors.New("ClickHouse log ID sequence must first be initialized by the master for this table and historical ID floor")
		}
		return nil
	}
	return LogMetaDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate := LogIDSequence{ID: 1, LastID: floor, Binding: binding}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		// Acquire a write lock before reading on every relational dialect.
		if err := tx.Model(&LogIDSequence{}).Where("id = 1").UpdateColumn("revision", gorm.Expr("revision + 1")).Error; err != nil {
			return err
		}
		var sequence LogIDSequence
		if err := tx.First(&sequence, 1).Error; err != nil {
			return err
		}
		if sequence.Binding != binding {
			return errors.New("ClickHouse table identity changed; explicitly migrate log metadata and reconcile IDs before restarting")
		}
		if sequence.LastID < floor {
			return tx.Model(&LogIDSequence{}).Where("id = 1").UpdateColumn("last_id", floor).Error
		}
		return nil
	})
}

func allocateLogID(ctx context.Context, db *gorm.DB) (int, error) {
	if db == nil || db.Dialector.Name() == "clickhouse" {
		return 0, errors.New("log ID allocation requires a relational metadata database")
	}
	var sequence LogIDSequence
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&LogIDSequence{}).Where("id = 1 AND last_id >= 0 AND last_id < ? AND binding <> ''", maxLogID).
			Updates(map[string]any{"last_id": gorm.Expr("last_id + 1"), "revision": gorm.Expr("revision + 1")})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("log ID sequence missing or exhausted")
		}
		return tx.First(&sequence, 1).Error
	})
	if err != nil {
		return 0, err
	}
	if sequence.LastID <= 0 || int64(int(sequence.LastID)) != sequence.LastID {
		return 0, errors.New("log ID exceeds integer cursor range")
	}
	return int(sequence.LastID), nil
}

func createLog(log *Log) error {
	if !IsClickHouseLogDB() {
		return LOG_DB.Create(log).Error
	}
	if log == nil || log.Id != 0 {
		// A previous ambiguous insertion must be reconciled, never automatically
		// retried with the same ID: MergeTree has no unique-key constraint.
		return errors.New("ClickHouse log creation requires a fresh unassigned record")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := allocateLogID(ctx, LogMetaDB)
	if err != nil {
		return err
	}
	log.Id = id
	return LOG_DB.WithContext(ctx).Create(log).Error
}
