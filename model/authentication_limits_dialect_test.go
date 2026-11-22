package model

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These DSNs must name disposable, empty databases (or an empty PostgreSQL
// schema). Never reuse the wallet tests' destructive reset helper here.
func TestAuthenticationLimitsExternalDialectConcurrency(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("AUTH_LIMIT_TEST_" + strings.ToUpper(dialect) + "_DSN")
			if dsn == "" {
				t.Skip("set AUTH_LIMIT_TEST_" + strings.ToUpper(dialect) + "_DSN to an empty disposable database")
			}
			var driver gorm.Dialector
			if dialect == "mysql" {
				driver = mysql.Open(dsn)
			} else {
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			pool.SetMaxOpenConns(8)
			t.Cleanup(func() { _ = pool.Close() })
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			if len(tables) != 0 {
				t.Fatal("refusing authentication limit tests: the selected database/schema is not empty")
			}
			created := []any{}
			t.Cleanup(func() {
				for i := len(created) - 1; i >= 0; i-- {
					if err := db.Migrator().DropTable(created[i]); err != nil {
						t.Errorf("cleanup of authentication test table: %v", err)
					}
				}
			})
			for _, value := range []any{&User{}, &UserSession{}, &UserAccessToken{}} {
				query := db
				if dialect == "mysql" {
					query = query.Set("gorm:table_options", "ENGINE=InnoDB")
				}
				require.NoError(t, query.Migrator().CreateTable(value))
				created = append(created, value)
			}
			previousDB, previousLog := DB, LOG_DB
			previousSQLite, previousMySQL, previousPostgres, previousRedis := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled
			DB, LOG_DB = db, db
			common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled = false, dialect == "mysql", dialect == "postgres", false
			t.Cleanup(func() {
				DB, LOG_DB = previousDB, previousLog
				common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled = previousSQLite, previousMySQL, previousPostgres, previousRedis
			})
			for _, kind := range []string{"pat", "session-active", "session-window"} {
				t.Run(kind, func(t *testing.T) {
					active, issued := 3, 100
					if kind == "session-window" {
						active, issued = 50, 3
					}
					authenticationLimits(t, 3, active, issued)
					user := &User{Username: kind, AffCode: kind, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
					require.NoError(t, db.Create(user).Error)
					authenticationConcurrentIssuance(t, db, user, kind)
				})
			}
		})
	}
}
