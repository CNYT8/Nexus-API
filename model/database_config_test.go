package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestChoosePostgresDisablesBothPreparedStatementLayers(t *testing.T) {
	oldPG, oldSQLite, oldMySQL := common.UsingPostgreSQL, common.UsingSQLite, common.UsingMySQL
	oldLogType := common.LogSqlType
	t.Cleanup(func() {
		common.UsingPostgreSQL, common.UsingSQLite, common.UsingMySQL = oldPG, oldSQLite, oldMySQL
		common.LogSqlType = oldLogType
		initCol()
	})

	for _, isLog := range []bool{false, true} {
		name := "main"
		if isLog {
			name = "log"
		}
		t.Run(name, func(t *testing.T) {
			// A nonexistent Unix socket makes the initial ping fail locally without
			// a live PostgreSQL server. GORM still exposes the constructed config.
			t.Setenv("TEST_POSTGRES_DSN", "postgres://test@/test?host="+t.TempDir()+"&sslmode=disable&connect_timeout=1")
			db, err := chooseDB("TEST_POSTGRES_DSN", isLog)
			require.Error(t, err)
			require.NotNil(t, db)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = pool.Close() })
			assert.False(t, db.PrepareStmt, "GORM must not prepare statements explicitly")
			_, prepared := db.ConnPool.(*gorm.PreparedStmtDB)
			assert.False(t, prepared)
			dialect, ok := db.Dialector.(*postgres.Dialector)
			require.True(t, ok)
			assert.True(t, dialect.PreferSimpleProtocol, "pgx implicit preparation must also be disabled")
		})
	}
}
