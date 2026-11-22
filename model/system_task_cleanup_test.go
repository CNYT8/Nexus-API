package model

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogCleanupLeaseRecoveryUsesAtomicCheckpoint(t *testing.T) {
	oldDB, oldLogDB := DB, LOG_DB
	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%s?mode=memory&cache=shared", t.Name(), name)), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		return db
	}
	DB, LOG_DB = open("main"), open("logs")
	t.Cleanup(func() { DB, LOG_DB = oldDB, oldLogDB })
	require.NoError(t, DB.AutoMigrate(&SystemTask{}))
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}, &LogCleanupLease{}, &LogCleanupCheckpoint{}))
	now := time.Now().Unix()
	logs := []Log{{CreatedAt: now - 100}, {CreatedAt: now - 100}, {CreatedAt: now - 100}, {CreatedAt: now}}
	require.NoError(t, LOG_DB.Create(&logs).Error)
	task, err := CreateLogCleanupTask(42, now-50)
	require.NoError(t, err)
	ctx := context.Background()
	first, claimed, err := ClaimLogCleanupLease(ctx, task, "old-worker")
	require.NoError(t, err)
	require.True(t, claimed)
	require.True(t, first.ProgressExact)
	_, claimed, err = ClaimLogCleanupLease(ctx, task, "blocked-worker")
	require.NoError(t, err)
	require.False(t, claimed)
	_, err = DeleteOldLogBatch(ctx, task.Target, 100)
	require.ErrorIs(t, err, ErrLogCleanupBusy)
	// A failure after DELETE but before checkpoint persistence rolls both back.
	const callback = "test:cleanup_checkpoint_failure"
	require.NoError(t, LOG_DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "log_cleanup_checkpoints" {
			tx.AddError(errors.New("injected checkpoint failure"))
		}
	}))
	_, err = RunLogCleanupBatch(ctx, task.ID, "old-worker", 1)
	require.ErrorContains(t, err, "injected checkpoint failure")
	require.NoError(t, LOG_DB.Callback().Update().Remove(callback))
	var beforeRetry int64
	require.NoError(t, LOG_DB.Model(&Log{}).Count(&beforeRetry).Error)
	require.EqualValues(t, 4, beforeRetry)
	one, err := RunLogCleanupBatch(ctx, task.ID, "old-worker", 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, one.Processed)
	// Simulate a crash after LOG_DB commit but before the main-DB mirror.
	stored, err := GetSystemTask(task.ID)
	require.NoError(t, err)
	require.Zero(t, stored.Processed)
	require.NoError(t, LOG_DB.Model(&LogCleanupLease{}).Where("id = 1").Update("lease_until", 0).Error)
	two, claimed, err := ClaimLogCleanupLease(ctx, stored, "new-worker")
	require.NoError(t, err)
	require.True(t, claimed)
	require.EqualValues(t, 1, two.Processed)
	_, err = RunLogCleanupBatch(ctx, task.ID, "old-worker", 100)
	require.ErrorIs(t, err, ErrLogCleanupLeaseLost)
	require.NoError(t, ReleaseLogCleanupLease(ctx, "old-worker"))
	three, err := RunLogCleanupBatch(ctx, task.ID, "new-worker", 100)
	require.NoError(t, err)
	require.EqualValues(t, 3, three.Processed)
	done, err := RunLogCleanupBatch(ctx, task.ID, "new-worker", 100)
	require.NoError(t, err)
	require.Equal(t, "succeeded", done.Status)
	require.NoError(t, SyncLogCleanupProgress(ctx, done))
	require.NoError(t, SyncLogCleanupProgress(ctx, one))
	stored, err = GetSystemTask(task.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", stored.Status)
	require.EqualValues(t, 3, stored.Processed)
	require.Equal(t, done.Revision, stored.ProgressVersion)
	var remaining int64
	require.NoError(t, LOG_DB.Model(&Log{}).Count(&remaining).Error)
	require.EqualValues(t, 1, remaining)

	// A terminal LOG_DB checkpoint is replayed without another deletion.
	completed, claimed, err := ClaimLogCleanupLease(ctx, task, "replay-worker")
	require.NoError(t, err)
	require.False(t, claimed)
	require.Equal(t, "succeeded", completed.Status)
	require.EqualValues(t, 3, completed.Processed)
	legacy := &SystemTask{Kind: "log_cleanup", Status: "running", UserID: 42, Target: now - 50, Processed: 7}
	require.NoError(t, DB.Create(legacy).Error)
	checkpoint, claimed, err := ClaimLogCleanupLease(ctx, legacy, "legacy-worker")
	require.NoError(t, err)
	require.True(t, claimed)
	require.False(t, checkpoint.ProgressExact)
	checkpoint, err = RunLogCleanupBatch(ctx, legacy.ID, "legacy-worker", 100)
	require.NoError(t, err)
	require.EqualValues(t, 7, checkpoint.Processed)
	require.Equal(t, "succeeded", checkpoint.Status)
}
