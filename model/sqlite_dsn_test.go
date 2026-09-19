package model

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const defaultSQLiteDSN = "one-api.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"

func TestSQLiteDefaultDSNShape(t *testing.T) {
	if os.Getenv("SQLITE_PATH") != "" {
		t.Skip("SQLITE_PATH override is set; the compiled-in default is not in effect")
	}
	assert.Equal(t, defaultSQLiteDSN, common.SQLitePath)
}

func openSQLiteDSNTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	require.Contains(t, common.SQLitePath, "one-api.db", "default DSN must reference one-api.db")
	dsn := strings.Replace(common.SQLitePath, "one-api.db", filepath.Join(t.TempDir(), "dsn-test.db"), 1)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// _busy_timeout= 的旧写法被纯 Go 驱动忽略,必须用 _pragma=busy_timeout(N)
// 才能真正生效;WAL 让读不再被唯一写者阻塞。
func TestSQLiteDSNAppliesBusyTimeoutAndWAL(t *testing.T) {
	db := openSQLiteDSNTestDB(t)

	var busyTimeout int
	require.NoError(t, db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error)
	assert.Equal(t, 30000, busyTimeout)

	var journalMode string
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
	assert.Equal(t, "wal", strings.ToLower(journalMode))
}

// _txlock=immediate + busy_timeout 下,多协程同时执行“先读后写”事务不应出现
// database is locked,且每次自增都不能丢失。
func TestSQLiteDSNConcurrentTransactionsDoNotLock(t *testing.T) {
	db := openSQLiteDSNTestDB(t)
	require.NoError(t, db.Exec("CREATE TABLE dsn_counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO dsn_counter (id, value) VALUES (1, 0)").Error)

	const workers = 8
	const incrementsPerWorker = 20
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < incrementsPerWorker; i++ {
				err := db.Transaction(func(tx *gorm.DB) error {
					var value int
					if err := tx.Raw("SELECT value FROM dsn_counter WHERE id = 1").Scan(&value).Error; err != nil {
						return err
					}
					return tx.Exec("UPDATE dsn_counter SET value = ? WHERE id = 1", value+1).Error
				})
				if err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	var finalValue int
	require.NoError(t, db.Raw("SELECT value FROM dsn_counter WHERE id = 1").Scan(&finalValue).Error)
	require.Equal(t, workers*incrementsPerWorker, finalValue)
}
