package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"gorm.io/gorm"
)

const clickHouseCleanupRetrySeconds int64 = 60

type clickHouseMutationState struct {
	Found   bool
	Pending bool
	Failed  bool
}

// Kept small so recovery can be tested with lost acknowledgements and crashes
// without claiming those tests exercise an actual ClickHouse server.
type clickHouseCleanupStore interface {
	MutationState(context.Context, string) (clickHouseMutationState, error)
	HasRemaining(context.Context, int64, int64) (bool, error)
	Submit(context.Context, string, string) error
}

type clickHouseCleanupDB struct {
	db *gorm.DB
}

func (store clickHouseCleanupDB) MutationState(ctx context.Context, marker string) (clickHouseMutationState, error) {
	var rows []struct {
		Done   uint8  `gorm:"column:is_done"`
		Reason string `gorm:"column:latest_fail_reason"`
	}
	err := store.db.WithContext(ctx).Raw("SELECT is_done, latest_fail_reason FROM system.mutations WHERE database = currentDatabase() AND table = 'logs' AND position(command, ?) > 0", marker).Scan(&rows).Error
	state := clickHouseMutationState{Found: len(rows) > 0}
	for _, row := range rows {
		state.Pending = state.Pending || row.Done == 0
		state.Failed = state.Failed || (row.Done == 0 && row.Reason != "")
	}
	return state, err
}

func (store clickHouseCleanupDB) HasRemaining(ctx context.Context, target, maxID int64) (bool, error) {
	var rows []struct {
		ID int64 `gorm:"column:id"`
	}
	err := store.db.WithContext(ctx).Raw("SELECT id FROM logs WHERE created_at < ? AND id > 0 AND id <= ? LIMIT 1", target, maxID).Scan(&rows).Error
	return len(rows) != 0, err
}

func (store clickHouseCleanupDB) Submit(ctx context.Context, queryID, statement string) error {
	ctx = ch.Context(ctx, ch.WithQueryID(queryID), ch.WithSettings(ch.Settings{
		"mutations_sync": 0, "alter_sync": 0, "max_execution_time": 4,
	}))
	return store.db.WithContext(ctx).Exec(statement).Error
}

func clickHouseCleanupSQL(checkpoint *LogCleanupCheckpoint) (statement, marker string, err error) {
	if checkpoint == nil || checkpoint.Backend != "clickhouse" || checkpoint.Target <= 0 || checkpoint.Target > time.Now().Unix() || checkpoint.MutationMaxID <= 0 {
		return "", "", errors.New("invalid ClickHouse cleanup range")
	}
	token, decodeErr := hex.DecodeString(checkpoint.MutationToken)
	if decodeErr != nil || len(token) != 16 || len(checkpoint.MutationToken) != 32 || checkpoint.MutationToken != strings.ToLower(checkpoint.MutationToken) {
		return "", "", errors.New("invalid ClickHouse cleanup mutation token")
	}
	marker = "nexus_log_cleanup_" + checkpoint.MutationToken
	// Fixed identifiers, validated integers and a lowercase hex token are the
	// ONLY interpolated values. A row-dependent tautology preserves the marker
	// in system.mutations.command; query_id is NOT a mutation_id.
	statement = fmt.Sprintf("ALTER TABLE logs DELETE WHERE created_at < %d AND id > 0 AND id <= %d AND if(id > 0, '%s', '') = '%s'", checkpoint.Target, checkpoint.MutationMaxID, marker, marker)
	return statement, marker, nil
}

func prepareClickHouseCleanup(ctx context.Context, meta *gorm.DB, taskID int64, owner string) (*LogCleanupCheckpoint, error) {
	var checkpoint LogCleanupCheckpoint
	err := meta.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := guardLogCleanupLease(tx, owner); err != nil {
			return err
		}
		if err := tx.First(&checkpoint, "task_id = ?", taskID).Error; err != nil {
			return err
		}
		if checkpoint.Backend != "clickhouse" {
			return errors.New("refusing to resume relational cleanup on ClickHouse")
		}
		if checkpoint.Status != "running" {
			return nil
		}
		var sequence LogIDSequence
		if err := tx.First(&sequence, 1).Error; err != nil {
			return err
		}
		if sequence.Binding == "" || sequence.LastID < 0 {
			return errors.New("invalid ClickHouse log ID sequence")
		}
		if checkpoint.MutationState != "" {
			if checkpoint.StoreBinding != sequence.Binding {
				return errors.New("ClickHouse cleanup store identity changed")
			}
			return nil
		}
		if checkpoint.Target <= 0 || checkpoint.Target > time.Now().Unix() {
			return errors.New("invalid ClickHouse cleanup cutoff")
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		checkpoint.StoreBinding = sequence.Binding
		checkpoint.MutationToken = hex.EncodeToString(token[:])
		checkpoint.MutationState = "prepared"
		checkpoint.MutationMaxID = sequence.LastID
		checkpoint.ProgressExact = false
		checkpoint.Revision++
		checkpoint.UpdatedAt = time.Now().Unix()
		if sequence.LastID == 0 {
			checkpoint.Status = "succeeded"
			checkpoint.MutationState = "done"
		}
		return tx.Save(&checkpoint).Error
	})
	return &checkpoint, err
}

// Save only under the current relational lease and expected revision. A late
// response from a previous worker can never overwrite the successor's state.
func saveClickHouseCleanup(ctx context.Context, meta *gorm.DB, owner string, checkpoint *LogCleanupCheckpoint) error {
	return meta.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := guardLogCleanupLease(tx, owner); err != nil {
			return err
		}
		var stored LogCleanupCheckpoint
		if err := tx.First(&stored, "task_id = ?", checkpoint.TaskID).Error; err != nil {
			return err
		}
		if stored.Revision != checkpoint.Revision || stored.MutationToken != checkpoint.MutationToken || stored.StoreBinding != checkpoint.StoreBinding {
			return ErrLogCleanupLeaseLost
		}
		checkpoint.Revision++
		checkpoint.UpdatedAt = time.Now().Unix()
		if err := tx.Save(checkpoint).Error; err != nil {
			return err
		}
		if checkpoint.Status != "running" {
			return releaseLogCleanupLeaseTx(tx, owner)
		}
		return nil
	})
}

func runClickHouseLogCleanupBatch(ctx context.Context, taskID int64, owner string) (*LogCleanupCheckpoint, error) {
	return runClickHouseLogCleanupWithStore(ctx, LogMetaDB, taskID, owner, clickHouseCleanupDB{db: LOG_DB})
}

func runClickHouseLogCleanupWithStore(ctx context.Context, meta *gorm.DB, taskID int64, owner string, store clickHouseCleanupStore) (*LogCleanupCheckpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	checkpoint, err := prepareClickHouseCleanup(ctx, meta, taskID, owner)
	if err != nil || checkpoint.Status != "running" {
		return checkpoint, err
	}
	statement, marker, err := clickHouseCleanupSQL(checkpoint)
	if err != nil {
		return nil, err
	}
	state, err := store.MutationState(ctx, marker)
	if err != nil {
		return nil, err
	}
	if state.Pending {
		checkpoint.MutationState = "submitted"
		checkpoint.Error = ""
		if state.Failed {
			// ClickHouse may still retry this mutation. Do not call it failed or
			// submit more work while a server-side mutation remains active.
			checkpoint.Error = "ClickHouse mutation is pending with a server error; inspect system.mutations"
		}
		return checkpoint, saveClickHouseCleanup(ctx, meta, owner, checkpoint)
	}
	// After an ambiguous submission, allow the bounded server request time to
	// elapse before checking/retrying. Even a replay deletes only this immutable
	// ID/time range, never newly allocated IDs or a moving time window.
	if !state.Found && checkpoint.MutationTryAt > 0 && time.Now().Unix()-checkpoint.MutationTryAt < clickHouseCleanupRetrySeconds {
		return checkpoint, nil
	}
	remaining, err := store.HasRemaining(ctx, checkpoint.Target, checkpoint.MutationMaxID)
	if err != nil {
		return nil, err
	}
	if !remaining {
		if checkpoint.MutationTryAt > 0 && !state.Found {
			// An empty range cannot identify the outcome of a missing mutation:
			// TTL or a different cleanup may have removed the rows. Never turn
			// an unknown remote result into success, even after the retry delay.
			checkpoint.Error = "ClickHouse mutation outcome is unknown; inspect system.mutations and reconcile the persisted intent"
			return checkpoint, saveClickHouseCleanup(ctx, meta, owner, checkpoint)
		}
		checkpoint.Status, checkpoint.MutationState, checkpoint.Error = "succeeded", "done", ""
		return checkpoint, saveClickHouseCleanup(ctx, meta, owner, checkpoint)
	}
	if checkpoint.MutationTryAt > 0 && time.Now().Unix()-checkpoint.MutationTryAt < clickHouseCleanupRetrySeconds {
		return checkpoint, nil
	}
	checkpoint.MutationState = "submitting"
	checkpoint.MutationTryAt = time.Now().Unix()
	checkpoint.Error = "ClickHouse mutation acknowledgement pending; recovery will inspect the persisted range"
	// Commit intent and retry time BEFORE submission. If the acknowledgement
	// or the following save is lost, a successor recovers by the same marker.
	if err := saveClickHouseCleanup(ctx, meta, owner, checkpoint); err != nil {
		return nil, err
	}
	queryID := fmt.Sprintf("%s_%d", marker, checkpoint.Revision)
	if err := store.Submit(ctx, queryID, statement); err != nil {
		return nil, err
	}
	checkpoint.MutationState, checkpoint.Error = "submitted", ""
	return checkpoint, saveClickHouseCleanup(ctx, meta, owner, checkpoint)
}
