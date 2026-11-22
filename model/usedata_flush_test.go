package model

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newQuotaDataFlushDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&QuotaData{}))

	CacheQuotaDataLock.Lock()
	previousDB, previousCache := DB, CacheQuotaData
	DB, CacheQuotaData = db, make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		DB, CacheQuotaData = previousDB, previousCache
		CacheQuotaDataLock.Unlock()
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func pendingQuotaData(modelName string) *QuotaData {
	return &QuotaData{UserID: 1, Username: "flush-user", ModelName: modelName, CreatedAt: 3600, Count: 1, Quota: 10, TokenUsed: 20}
}

func TestFlushQuotaDataCachePartialFailureDoesNotReplaySuccess(t *testing.T) {
	db := newQuotaDataFlushDB(t)
	existing := pendingQuotaData("success")
	require.NoError(t, db.Create(existing).Error)
	CacheQuotaData["success"] = pendingQuotaData("success")
	failed := pendingQuotaData("failure")
	CacheQuotaData["failure"] = failed
	injected := errors.New("injected quota insert failure")
	const callback = "test:quota_partial_failure"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if data, ok := tx.Statement.Dest.(*QuotaData); ok && data.ModelName == "failure" {
			tx.AddError(injected)
		}
	}))

	require.ErrorIs(t, FlushQuotaDataCache(context.Background()), injected)
	require.Len(t, CacheQuotaData, 1)
	require.Same(t, failed, CacheQuotaData["failure"])
	var saved QuotaData
	require.NoError(t, db.Where("model_name = ?", "success").First(&saved).Error)
	require.Equal(t, 2, saved.Count)
	require.Equal(t, 20, saved.Quota)
	require.Equal(t, 40, saved.TokenUsed)

	require.NoError(t, db.Callback().Create().Remove(callback))
	require.NoError(t, FlushQuotaDataCache(context.Background()))
	require.Empty(t, CacheQuotaData)
	// The compatibility entry point is also safe after a successful flush.
	SaveQuotaDataCache()
	saved = QuotaData{}
	require.NoError(t, db.Where("model_name = ?", "success").First(&saved).Error)
	require.Equal(t, 2, saved.Count)
	require.Equal(t, 20, saved.Quota)
	require.Equal(t, 40, saved.TokenUsed)
	saved = QuotaData{}
	require.NoError(t, db.Where("model_name = ?", "failure").First(&saved).Error)
	require.Equal(t, 1, saved.Count)
	require.Equal(t, 10, saved.Quota)
}

func TestFlushQuotaDataCacheQueryFailureDoesNotInsert(t *testing.T) {
	db := newQuotaDataFlushDB(t)
	pending := pendingQuotaData("query-error")
	CacheQuotaData["pending"] = pending
	injected := errors.New("injected quota query failure")
	const callback = "test:quota_query_failure"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		tx.AddError(injected)
	}))
	require.ErrorIs(t, FlushQuotaDataCache(context.Background()), injected)
	require.Same(t, pending, CacheQuotaData["pending"])
	require.Zero(t, pending.Id)
	require.NoError(t, db.Callback().Query().Remove(callback))
	var count int64
	require.NoError(t, db.Model(&QuotaData{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, FlushQuotaDataCache(context.Background()))
	require.Empty(t, CacheQuotaData)
}

func TestFlushQuotaDataCacheWriteErrorsRollBackAndRetry(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			db := newQuotaDataFlushDB(t)
			if operation == "update" {
				require.NoError(t, db.Create(pendingQuotaData(operation)).Error)
			}
			pending := pendingQuotaData(operation)
			CacheQuotaData["pending"] = pending
			injected := errors.New("injected failure after quota write")
			const callback = "test:quota_write_failure"
			inject := func(tx *gorm.DB) { tx.AddError(injected) }
			if operation == "create" {
				require.NoError(t, db.Callback().Create().After("gorm:create").Register(callback, inject))
			} else {
				require.NoError(t, db.Callback().Update().After("gorm:update").Register(callback, inject))
			}

			require.ErrorIs(t, FlushQuotaDataCache(context.Background()), injected)
			require.Same(t, pending, CacheQuotaData["pending"])
			require.Zero(t, pending.Id, "rolled-back Create must not mutate the cached value")
			var saved QuotaData
			err := db.First(&saved).Error
			if operation == "create" {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.NoError(t, db.Callback().Create().Remove(callback))
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, saved.Count)
				require.Equal(t, 10, saved.Quota)
				require.Equal(t, 20, saved.TokenUsed)
				require.NoError(t, db.Callback().Update().Remove(callback))
			}
			require.NoError(t, FlushQuotaDataCache(context.Background()))
			require.NoError(t, FlushQuotaDataCache(context.Background()))
			require.Empty(t, CacheQuotaData)
			saved = QuotaData{}
			require.NoError(t, db.First(&saved).Error)
			wantCount := 1
			if operation == "update" {
				wantCount = 2
			}
			require.Equal(t, wantCount, saved.Count)
			require.Equal(t, wantCount*10, saved.Quota)
			require.Equal(t, wantCount*20, saved.TokenUsed)
		})
	}
}

// Refuse Commit before it reaches the database, so retry safety can be asserted
// without pretending that an ambiguous remote commit has exactly-once semantics.
type quotaCommitFailurePool struct {
	gorm.ConnPool
	database *sql.DB
	failure  error
}

func (pool *quotaCommitFailurePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.database.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &quotaCommitFailureTx{Tx: tx, failure: pool.failure}, nil
}

type quotaCommitFailureTx struct {
	*sql.Tx
	failure error
}

func (tx *quotaCommitFailureTx) Commit() error { return tx.failure }

func TestFlushQuotaDataCacheCommitFailureRetainsPendingDelta(t *testing.T) {
	db := newQuotaDataFlushDB(t)
	pending := pendingQuotaData("commit-error")
	CacheQuotaData["pending"] = pending
	sqlDB, err := db.DB()
	require.NoError(t, err)
	injected := errors.New("injected quota commit failure")
	failingDB := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	failingDB.Statement.ConnPool = &quotaCommitFailurePool{ConnPool: sqlDB, database: sqlDB, failure: injected}
	DB = failingDB

	require.ErrorIs(t, FlushQuotaDataCache(context.Background()), injected)
	require.Same(t, pending, CacheQuotaData["pending"])
	require.Zero(t, pending.Id)
	DB = db
	var count int64
	require.NoError(t, db.Model(&QuotaData{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, FlushQuotaDataCache(context.Background()))
	require.NoError(t, FlushQuotaDataCache(context.Background()))
	require.NoError(t, db.Model(&QuotaData{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestFlushQuotaDataCacheCancellationWhileWaitingForLock(t *testing.T) {
	newQuotaDataFlushDB(t)
	pending := pendingQuotaData("locked")
	CacheQuotaData["pending"] = pending
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- FlushQuotaDataCache(ctx) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not stop waiting for the cache lock")
	}
	require.Same(t, pending, CacheQuotaData["pending"])
}

func TestFlushQuotaDataCacheCancellationWhileWaitingForDatabase(t *testing.T) {
	db := newQuotaDataFlushDB(t)
	pending := pendingQuotaData("busy-database")
	CacheQuotaData["pending"] = pending
	sqlDB, err := db.DB()
	require.NoError(t, err)
	conn, err := sqlDB.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- FlushQuotaDataCache(ctx) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not stop waiting for a database connection")
	}
	require.Same(t, pending, CacheQuotaData["pending"])
	require.NoError(t, conn.Close())
	require.NoError(t, FlushQuotaDataCache(context.Background()))
	require.Empty(t, CacheQuotaData)
}

func TestSaveQuotaDataCacheKeepsFailureForNextAttempt(t *testing.T) {
	db := newQuotaDataFlushDB(t)
	pending := pendingQuotaData("compatibility")
	CacheQuotaData["pending"] = pending
	DB = nil
	SaveQuotaDataCache()
	require.Same(t, pending, CacheQuotaData["pending"])
	DB = db
	SaveQuotaDataCache()
	require.Empty(t, CacheQuotaData)
}
