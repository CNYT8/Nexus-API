package model

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUserLogWindowBounds(t *testing.T) {
	start, end, err := NormalizeUserLogWindow(0, 0)
	require.NoError(t, err)
	require.Equal(t, UserLogMaxSpanSeconds, end-start)
	_, _, err = NormalizeUserLogWindow(end-UserLogMaxSpanSeconds-1, end)
	require.NoError(t, err)
	_, _, err = NormalizeUserLogWindow(end-365*86400, end)
	require.NoError(t, err)
	_, _, err = NormalizeUserLogWindow(end-365*86400-1, end)
	require.Error(t, err)
	_, _, err = NormalizeUserLogWindow(end+1, end)
	require.Error(t, err)
	_, _, err = NormalizeUserLogWindow(-1, end)
	require.Error(t, err)
}

func TestLogCleanupDurableProgressAndExportOwnerCursor(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&SystemTask{}))
	require.NoError(t, LOG_DB.AutoMigrate(&LogCleanupLease{}, &LogCleanupCheckpoint{}))
	require.NoError(t, LOG_DB.Exec("DELETE FROM log_cleanup_checkpoints").Error)
	require.NoError(t, LOG_DB.Exec("DELETE FROM log_cleanup_leases").Error)
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)
	now := time.Now().Unix()
	rows := []Log{
		{UserId: 801, Type: LogTypeConsume, CreatedAt: now - 200, Content: "old"},
		{UserId: 801, Type: LogTypeConsume, CreatedAt: now, Content: "mine", ChannelId: 9},
		{UserId: 802, Type: LogTypeConsume, CreatedAt: now, Content: "other-owner"},
	}
	require.NoError(t, LOG_DB.Create(&rows).Error)
	logs, err := GetLogExportBatch(context.Background(), LogExportFilter{UserID: 801}, 0, 1)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	cursor := logs[0].Id
	FormatUserLogExport(logs, 0)
	require.Zero(t, logs[0].ChannelId)
	logs, err = GetLogExportBatch(context.Background(), LogExportFilter{UserID: 801}, cursor, 1)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, "old", logs[0].Content)
	task, err := CreateLogCleanupTask(801, now-100)
	require.NoError(t, err)
	ctx := context.Background()
	_, claimed, err := ClaimLogCleanupLease(ctx, task, "worker-one")
	require.NoError(t, err)
	require.True(t, claimed)
	_, claimed, err = ClaimLogCleanupLease(ctx, task, "worker-two")
	require.NoError(t, err)
	require.False(t, claimed)
	checkpoint, err := RunLogCleanupBatch(ctx, task.ID, "worker-one", 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, checkpoint.Processed)
	checkpoint, err = RunLogCleanupBatch(ctx, task.ID, "worker-one", 100)
	require.NoError(t, err)
	require.Equal(t, "succeeded", checkpoint.Status)
	require.NoError(t, SyncLogCleanupProgress(ctx, checkpoint))
	stored, err := GetSystemTask(task.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", stored.Status)
	require.EqualValues(t, 1, stored.Processed)
}
