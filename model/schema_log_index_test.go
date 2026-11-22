package model

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyLegacyLogIndexMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_created_at_id"))
	require.NoError(t, db.Exec(`CREATE INDEX idx_created_at_id ON logs (id, created_at)`).Error)
	original := Log{Id: 81, CreatedAt: 1700000000, UserId: 7, Content: "historical", Quota: 123}
	require.NoError(t, db.Create(&original).Error)
	before, err := migrationIndexes(db, "logs")
	require.NoError(t, err)
	require.Equal(t, []string{"id", "created_at"}, before["idx_created_at_id"].Columns)
	var originalOID int64
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Raw(`SELECT 'idx_created_at_id'::regclass::oid::bigint`).Scan(&originalOID).Error)
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, migrateAdditive(db, &Log{}))
	}
	after, err := migrationIndexes(db, "logs")
	require.NoError(t, err)
	require.Equal(t, before, after)
	if db.Dialector.Name() == "postgres" {
		var currentOID int64
		require.NoError(t, db.Raw(`SELECT 'idx_created_at_id'::regclass::oid::bigint`).Scan(&currentOID).Error)
		require.Equal(t, originalOID, currentOID, "migration must preserve the existing index object")
	}
	var restored Log
	require.NoError(t, db.First(&restored, 81).Error)
	require.Equal(t, original, restored)
}

func TestSchemaLegacyLogIndexPreserved(t *testing.T) {
	verifyLegacyLogIndexMigration(t, compatibilityTestDB(t))
}

func TestSchemaLegacyLogIndexPostgres(t *testing.T) {
	dsn := os.Getenv("SCHEMA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SCHEMA_MIGRATION_TEST_POSTGRES_DSN to an empty disposable PostgreSQL schema")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "refusing migration tests: PostgreSQL schema must be empty and disposable")
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&Log{})) })
	verifyLegacyLogIndexMigration(t, db)
}

func TestSchemaLegacyLogIndexExceptionIsNarrow(t *testing.T) {
	want := migrationIndex{Name: "idx_created_at_id", Columns: []string{"created_at", "id"}}
	legacy := migrationIndex{Name: want.Name, Columns: []string{"id", "created_at"}, Valid: true}
	for _, name := range []string{"legacy", "other-table", "other-name", "unique-required", "unique-actual", "invalid", "expression", "partial", "wrong-column", "extra-column"} {
		t.Run(name, func(t *testing.T) {
			table, expected, actual := "logs", want, legacy
			switch name {
			case "other-table":
				table = "other_logs"
			case "other-name":
				expected.Name, actual.Name = "other_index", "other_index"
			case "unique-required":
				expected.Unique = true
			case "unique-actual":
				actual.Unique = true
			case "invalid":
				actual.Valid = false
			case "expression":
				actual.Unsupported = true
			case "partial":
				actual.Predicate = "id > 0"
			case "wrong-column":
				actual.Columns = []string{"id", "user_id"}
			case "extra-column":
				actual.Columns = []string{"id", "created_at", "user_id"}
			}
			err := requireMigrationIndex(table, expected, map[string]migrationIndex{actual.Name: actual})
			if name == "legacy" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "unexpected definition")
				require.ErrorContains(t, err, "actual exists=true")
			}
		})
	}
}
