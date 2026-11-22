package model

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// common intentionally has no private-key getter/reset API. Re-executing only
// this test in the already-built test binary keeps its key changes out of the
// parent process, including when the original key was uninitialized. No go
// command, network service, real database, or new build is used here.
func passwordCryptoModelProcess(t *testing.T) bool {
	t.Helper()
	const marker = "NEXUS_PASSWORD_CRYPTO_MODEL_TEST"
	if os.Getenv(marker) == t.Name() {
		return true
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.timeout=50s")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, marker+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, marker+"="+t.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated password crypto regression failed: %v\n%s", err, output)
	}
	return false
}

func passwordCryptoModelDB(t *testing.T, migrate bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	// One live connection keeps SQLite's memory database stable and avoids
	// testing SQLite table-lock timing instead of the unique active slot.
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	oldDB, oldLogDB := DB, LOG_DB
	oldEnabled, oldRedis := common.PasswordLoginEncryptionEnabled, common.RedisEnabled
	oldSQLite, oldMySQL, oldPostgres := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLogDB
		common.PasswordLoginEncryptionEnabled, common.RedisEnabled = oldEnabled, oldRedis
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = oldSQLite, oldMySQL, oldPostgres
		if err := pool.Close(); err != nil {
			t.Errorf("close password crypto memory database: %v", err)
		}
	})
	DB, LOG_DB = db, db
	common.PasswordLoginEncryptionEnabled, common.RedisEnabled = true, false
	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
	if migrate {
		require.NoError(t, db.AutoMigrate(&LoginEncryptionKey{}))
	}
	return db
}

func passwordCryptoModelAssertStoredKey(t *testing.T, stored LoginEncryptionKey) {
	t.Helper()
	block, rest := pem.Decode([]byte(stored.PrivateKeyPEM))
	require.NotNil(t, block)
	require.Empty(t, rest)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)
	privateKey, ok := parsed.(*rsa.PrivateKey)
	require.True(t, ok)
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	digest := sha256.Sum256(der)
	kid, publicKey := common.PasswordEncryptionPublicKey()
	require.Equal(t, hex.EncodeToString(digest[:16]), kid)
	require.Equal(t, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), publicKey)
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &privateKey.PublicKey, []byte("persisted-key-password"), nil)
	require.NoError(t, err)
	plaintext, err := common.DecryptPassword(base64.StdEncoding.EncodeToString(ciphertext), kid)
	require.NoError(t, err)
	require.True(t, plaintext == "persisted-key-password", "serving key must match the persisted key")
}

func TestInitPasswordEncryptionDisabledDoesNotAccessDB(t *testing.T) {
	if !passwordCryptoModelProcess(t) {
		return
	}
	db := passwordCryptoModelDB(t, true)
	kid, publicKey := common.PasswordEncryptionPublicKey()
	common.PasswordLoginEncryptionEnabled = false
	DB = nil
	require.NoError(t, InitPasswordEncryption())
	DB = db
	require.NoError(t, InitPasswordEncryption())
	var count int64
	require.NoError(t, db.Model(&LoginEncryptionKey{}).Count(&count).Error)
	require.Zero(t, count)
	gotID, gotPublicKey := common.PasswordEncryptionPublicKey()
	require.Equal(t, kid, gotID)
	require.Equal(t, publicKey, gotPublicKey)
}

func TestInitPasswordEncryptionRepeatedUsesPersistedActiveSlot(t *testing.T) {
	if !passwordCryptoModelProcess(t) {
		return
	}
	db := passwordCryptoModelDB(t, true)
	require.NoError(t, InitPasswordEncryption())
	var stored LoginEncryptionKey
	require.NoError(t, db.Where("slot = ?", activeLoginEncryptionKeySlot).First(&stored).Error)
	passwordCryptoModelAssertStoredKey(t, stored)
	for i := 0; i < 3; i++ {
		// Model a replica whose local key differs: startup must reload the
		// database winner rather than keep or persist its local candidate.
		otherKey, err := common.GeneratePasswordEncryptionPrivateKey()
		require.NoError(t, err)
		require.NoError(t, common.LoadPasswordEncryptionPrivateKey(otherKey))
		require.NoError(t, InitPasswordEncryption())
		passwordCryptoModelAssertStoredKey(t, stored)
	}
	duplicate := LoginEncryptionKey{Slot: activeLoginEncryptionKeySlot, PrivateKeyPEM: "must-not-replace-the-winner"}
	require.Error(t, db.Create(&duplicate).Error, "active slot must have a database uniqueness constraint")
	var rows []LoginEncryptionKey
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.True(t, rows[0] == stored, "initialization must never replace persisted key material")
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(encoded), "internal key fields must not be serialized")
}

func TestInitPasswordEncryptionConcurrentInitializersConverge(t *testing.T) {
	if !passwordCryptoModelProcess(t) {
		return
	}
	db := passwordCryptoModelDB(t, true)
	const workers = 6
	var arrived atomic.Int32
	ready := make(chan struct{})
	// Force every initializer to observe an empty slot and generate a
	// different candidate before ANY INSERT starts. Run before transaction
	// acquisition so the single memory connection cannot deadlock here.
	const callback = "password_crypto_test:all_candidates_ready"
	require.NoError(t, db.Callback().Create().Before("gorm:begin_transaction").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*LoginEncryptionKey); !ok {
			return
		}
		if arrived.Add(1) == workers {
			close(ready)
		}
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		select {
		case <-ready:
		case <-timer.C:
			tx.AddError(errors.New("password crypto candidate barrier timed out"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
	type result struct {
		err       error
		kid       string
		publicKey string
	}
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		go func() {
			err := InitPasswordEncryption()
			kid, publicKey := common.PasswordEncryptionPublicKey()
			results <- result{err: err, kid: kid, publicKey: publicKey}
		}()
	}
	// Drain ALL workers before assertions/cleanup can close the database.
	got := make([]result, workers)
	for i := range got {
		got[i] = <-results
	}
	require.EqualValues(t, workers, arrived.Load())
	for _, result := range got {
		require.NoError(t, result.err)
	}
	var rows []LoginEncryptionKey
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, activeLoginEncryptionKeySlot, rows[0].Slot)
	passwordCryptoModelAssertStoredKey(t, rows[0])
	kid, publicKey := common.PasswordEncryptionPublicKey()
	for _, result := range got {
		require.Equal(t, kid, result.kid)
		require.Equal(t, publicKey, result.publicKey)
	}
}

func TestInitPasswordEncryptionUnavailableOrCorruptStoreFailsClosed(t *testing.T) {
	if !passwordCryptoModelProcess(t) {
		return
	}
	for _, name := range []string{"nil_database", "missing_schema", "corrupt_active_key"} {
		t.Run(name, func(t *testing.T) {
			db := passwordCryptoModelDB(t, name != "missing_schema")
			key, err := common.GeneratePasswordEncryptionPrivateKey()
			require.NoError(t, err)
			require.NoError(t, common.LoadPasswordEncryptionPrivateKey(key))
			kid, publicKey := common.PasswordEncryptionPublicKey()
			switch name {
			case "nil_database":
				DB = nil
			case "corrupt_active_key":
				require.NoError(t, db.Create(&LoginEncryptionKey{Slot: activeLoginEncryptionKeySlot, PrivateKeyPEM: "corrupt-private-key-sentinel"}).Error)
			}
			err = InitPasswordEncryption()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "corrupt-private-key-sentinel")
			gotID, gotPublicKey := common.PasswordEncryptionPublicKey()
			require.Equal(t, kid, gotID)
			require.Equal(t, publicKey, gotPublicKey)
			if name == "corrupt_active_key" {
				var rows []LoginEncryptionKey
				require.NoError(t, db.Find(&rows).Error)
				require.Len(t, rows, 1)
				require.True(t, rows[0].PrivateKeyPEM == "corrupt-private-key-sentinel", "bad persisted key must not be silently replaced")
			}
		})
	}
}
