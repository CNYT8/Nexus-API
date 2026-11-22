package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClickHouseLogActivationRequiresExplicitConfiguration(t *testing.T) {
	t.Setenv("LOG_SQL_CLICKHOUSE_ENABLED", "")
	t.Setenv("LOG_SQL_CLICKHOUSE_ID_FLOOR", "0")
	t.Setenv("LOG_SQL_CLICKHOUSE_TTL_DAYS", "")
	_, _, err := clickHouseLogConfig()
	require.ErrorContains(t, err, "LOG_SQL_CLICKHOUSE_ENABLED")
	t.Setenv("LOG_SQL_CLICKHOUSE_ENABLED", "true")
	ttl, floor, err := clickHouseLogConfig()
	require.NoError(t, err)
	require.Zero(t, ttl)
	require.Zero(t, floor)
	for _, invalid := range []string{"-1", "1", "30", "31; DROP TABLE logs", "36501"} {
		t.Setenv("LOG_SQL_CLICKHOUSE_TTL_DAYS", invalid)
		_, _, err := clickHouseLogConfig()
		require.Error(t, err, invalid)
	}
	t.Setenv("LOG_SQL_CLICKHOUSE_TTL_DAYS", "31")
	t.Setenv("LOG_SQL_CLICKHOUSE_ID_FLOOR", "9007199254740992")
	ttl, floor, err = clickHouseLogConfig()
	require.NoError(t, err)
	require.Equal(t, 31, ttl)
	require.EqualValues(t, 9007199254740992, floor)
	t.Setenv("LOG_SQL_CLICKHOUSE_ID_FLOOR", "")
	_, _, err = clickHouseLogConfig()
	require.ErrorContains(t, err, "LOG_SQL_CLICKHOUSE_ID_FLOOR")
}

func TestClickHouseLogTTLZeroNeverEnablesRetention(t *testing.T) {
	statement, err := clickHouseLogTTLStatement(0, clickHouseLogsDDL)
	require.NoError(t, err)
	require.Empty(t, statement)
	for _, existing := range []string{
		clickHouseLogsDDL + " TTL toDateTime(created_at) + INTERVAL 31 DAY",
		clickHouseLogsDDL + "\nTTL\ttoDateTime(created_at) + INTERVAL 31 DAY",
	} {
		statement, err := clickHouseLogTTLStatement(0, existing)
		require.NoError(t, err)
		require.Equal(t, "ALTER TABLE logs REMOVE TTL", statement)
	}
	for _, invalid := range []int{-1, 1, 30, 36501} {
		_, err := clickHouseLogTTLStatement(invalid, clickHouseLogsDDL)
		require.Error(t, err)
	}
	statement, err = clickHouseLogTTLStatement(31, clickHouseLogsDDL)
	require.NoError(t, err)
	require.Equal(t, "ALTER TABLE logs MODIFY TTL toDateTime(created_at) + INTERVAL 31 DAY DELETE", statement)
}

func TestClickHouseCleanupSQLRejectsInjectionAndMovingCutoff(t *testing.T) {
	checkpoint := &LogCleanupCheckpoint{
		Backend: "clickhouse", Target: time.Now().Unix() - 100,
		MutationMaxID: 9007199254740993, MutationToken: strings.Repeat("ab", 16),
	}
	statement, marker, err := clickHouseCleanupSQL(checkpoint)
	require.NoError(t, err)
	require.Contains(t, statement, "id <= 9007199254740993")
	require.Contains(t, statement, fmt.Sprintf("created_at < %d", checkpoint.Target))
	require.Equal(t, "nexus_log_cleanup_"+checkpoint.MutationToken, marker)
	require.NotContains(t, statement, "now()")
	for _, token := range []string{"", strings.Repeat("a", 31), strings.Repeat("A", 32), "'; DROP TABLE logs; --"} {
		checkpoint.MutationToken = token
		_, _, err := clickHouseCleanupSQL(checkpoint)
		require.Error(t, err)
	}
	checkpoint.MutationToken = strings.Repeat("ab", 16)
	checkpoint.Target = time.Now().Unix() + 3600
	_, _, err = clickHouseCleanupSQL(checkpoint)
	require.Error(t, err)
	checkpoint.Target = 1
	checkpoint.MutationMaxID = 0
	_, _, err = clickHouseCleanupSQL(checkpoint)
	require.Error(t, err)
}

func openClickHouseMetadataTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.AutoMigrate(&LogIDSequence{}, &LogCleanupLease{}, &LogCleanupCheckpoint{}))
	return db
}

func TestClickHouseLogIDSequencePreservesIntegerCursorAndGaps(t *testing.T) {
	db := openClickHouseMetadataTestDB(t)
	ctx := context.Background()
	_, err := allocateLogID(ctx, db)
	require.Error(t, err)
	require.NoError(t, db.Create(&LogIDSequence{ID: 1, LastID: 9007199254740992, Binding: "logs/uuid"}).Error)
	first, err := allocateLogID(ctx, db)
	require.NoError(t, err)
	// A failed remote insert does not roll back or reuse an allocated ID.
	second, err := allocateLogID(ctx, db)
	require.NoError(t, err)
	require.EqualValues(t, 9007199254740993, first)
	require.Equal(t, first+1, second)
	require.NoError(t, db.Model(&LogIDSequence{}).Where("id = 1").Update("last_id", maxLogID).Error)
	_, err = allocateLogID(ctx, db)
	require.ErrorContains(t, err, "exhausted")
}

type fakeClickHouseCleanupStore struct {
	state       clickHouseMutationState
	remaining   bool
	submitErr   error
	submissions []string
	beforeSend  func()
	lastTarget  int64
	lastMaxID   int64
}

func (store *fakeClickHouseCleanupStore) MutationState(context.Context, string) (clickHouseMutationState, error) {
	return store.state, nil
}

func (store *fakeClickHouseCleanupStore) HasRemaining(_ context.Context, target, maxID int64) (bool, error) {
	store.lastTarget, store.lastMaxID = target, maxID
	return store.remaining, nil
}

func (store *fakeClickHouseCleanupStore) Submit(_ context.Context, _ string, statement string) error {
	if store.beforeSend != nil {
		store.beforeSend()
	}
	store.submissions = append(store.submissions, statement)
	return store.submitErr
}

func seedClickHouseCleanupTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&LogIDSequence{ID: 1, LastID: 42, Binding: "logs/uuid"}).Error)
	require.NoError(t, db.Create(&LogCleanupLease{ID: 1, Owner: "worker", LeaseUntil: time.Now().Unix() + 60}).Error)
	require.NoError(t, db.Create(&LogCleanupCheckpoint{
		TaskID: 1, UserID: 7, Target: time.Now().Unix() - 100,
		Backend: "clickhouse", Status: "running", Revision: 1,
	}).Error)
}

func TestClickHouseCleanupRecoversLostAcknowledgementWithoutMovingRange(t *testing.T) {
	db := openClickHouseMetadataTestDB(t)
	seedClickHouseCleanupTest(t, db)
	ctx := context.Background()
	store := &fakeClickHouseCleanupStore{remaining: true, submitErr: context.DeadlineExceeded}
	var committed LogCleanupCheckpoint
	store.beforeSend = func() {
		require.NoError(t, db.First(&committed, "task_id = 1").Error)
		require.Equal(t, "submitting", committed.MutationState)
		require.EqualValues(t, 42, committed.MutationMaxID)
		require.NotEmpty(t, committed.MutationToken)
		require.Positive(t, committed.MutationTryAt)
	}
	_, err := runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, store.submissions, 1)
	// A restart immediately sees the committed attempt and does not resubmit.
	checkpoint, err := runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.NoError(t, err)
	require.Equal(t, "running", checkpoint.Status)
	require.False(t, checkpoint.ProgressExact)
	require.Len(t, store.submissions, 1)
	// New allocations cannot expand this cleanup's range on recovery.
	require.NoError(t, db.Model(&LogIDSequence{}).Where("id = 1").Update("last_id", 99).Error)
	require.NoError(t, db.Model(&LogCleanupCheckpoint{}).Where("task_id = 1").Update("mutation_try_at", time.Now().Unix()-61).Error)
	store.submitErr, store.beforeSend = nil, nil
	checkpoint, err = runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.NoError(t, err)
	require.Len(t, store.submissions, 2)
	require.Equal(t, store.submissions[0], store.submissions[1])
	require.EqualValues(t, 42, checkpoint.MutationMaxID)
	// A server-side error on an active mutation remains visible and running.
	store.state = clickHouseMutationState{Found: true, Pending: true, Failed: true}
	checkpoint, err = runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.NoError(t, err)
	require.Equal(t, "running", checkpoint.Status)
	require.Contains(t, checkpoint.Error, "server error")
	require.Len(t, store.submissions, 2)
	// Completion requires an empty fixed range, not a driver RowsAffected count.
	store.state, store.remaining = clickHouseMutationState{Found: true}, false
	checkpoint, err = runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.NoError(t, err)
	require.Equal(t, "succeeded", checkpoint.Status)
	require.Zero(t, checkpoint.Processed)
	require.False(t, checkpoint.ProgressExact)
	require.Equal(t, committed.Target, store.lastTarget)
	require.EqualValues(t, 42, store.lastMaxID)
}

func TestClickHouseCleanupMissingMutationIsNeverReportedSuccessful(t *testing.T) {
	db := openClickHouseMetadataTestDB(t)
	seedClickHouseCleanupTest(t, db)
	ctx := context.Background()
	store := &fakeClickHouseCleanupStore{remaining: true, submitErr: context.DeadlineExceeded}
	_, err := runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, db.Model(&LogCleanupCheckpoint{}).Where("task_id = 1").Update("mutation_try_at", time.Now().Unix()-61).Error)
	// TTL or another administrator may have emptied the range. This does not
	// tell us whether the lost remote request is complete or still pending.
	store.remaining = false
	checkpoint, err := runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.NoError(t, err)
	require.Equal(t, "running", checkpoint.Status)
	require.False(t, checkpoint.ProgressExact)
	require.Contains(t, checkpoint.Error, "outcome is unknown")
	require.Len(t, store.submissions, 1)
	// Positive server evidence plus the empty range is sufficient.
	store.state = clickHouseMutationState{Found: true}
	checkpoint, err = runClickHouseLogCleanupWithStore(ctx, db, 1, "worker", store)
	require.NoError(t, err)
	require.Equal(t, "succeeded", checkpoint.Status)
}

func TestClickHouseCleanupFencesStaleWorkersAndStoreChanges(t *testing.T) {
	db := openClickHouseMetadataTestDB(t)
	seedClickHouseCleanupTest(t, db)
	ctx := context.Background()
	checkpoint, err := prepareClickHouseCleanup(ctx, db, 1, "worker")
	require.NoError(t, err)
	require.NoError(t, db.Model(&LogCleanupLease{}).Where("id = 1").Update("owner", "successor").Error)
	require.ErrorIs(t, saveClickHouseCleanup(ctx, db, "worker", checkpoint), ErrLogCleanupLeaseLost)
	require.NoError(t, db.Model(&LogIDSequence{}).Where("id = 1").Update("binding", "replacement/uuid").Error)
	_, err = prepareClickHouseCleanup(ctx, db, 1, "successor")
	require.ErrorContains(t, err, "identity changed")
}

func TestClickHouseCleanupCrashBeforeSubmissionIsRecoverable(t *testing.T) {
	db := openClickHouseMetadataTestDB(t)
	seedClickHouseCleanupTest(t, db)
	ctx := context.Background()
	prepared, err := prepareClickHouseCleanup(ctx, db, 1, "worker")
	require.NoError(t, err)
	require.Equal(t, "prepared", prepared.MutationState)
	// Simulate process death after preparation, before the remote call.
	require.NoError(t, db.Model(&LogCleanupLease{}).Where("id = 1").Update("owner", "successor").Error)
	store := &fakeClickHouseCleanupStore{remaining: true}
	checkpoint, err := runClickHouseLogCleanupWithStore(ctx, db, 1, "successor", store)
	require.NoError(t, err)
	require.Equal(t, prepared.MutationToken, checkpoint.MutationToken)
	require.Len(t, store.submissions, 1)
	// A failed checkpoint commit must prevent submission altogether.
	require.NoError(t, db.Model(&LogCleanupCheckpoint{}).Where("task_id = 1").Updates(map[string]any{"mutation_try_at": 0, "mutation_state": "prepared"}).Error)
	const callback = "test:clickhouse_checkpoint_failure"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "log_cleanup_checkpoints" {
			tx.AddError(errors.New("checkpoint unavailable"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	_, err = runClickHouseLogCleanupWithStore(ctx, db, 1, "successor", store)
	require.ErrorContains(t, err, "checkpoint unavailable")
	require.Len(t, store.submissions, 1)
}
