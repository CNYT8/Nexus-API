package model

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// All dialects share an in-memory SQLite pool and only generate SQL. No external
// database is contacted; server execution remains a separate integration check.
func TestGetTokenKeysByIdsDialectProjection(t *testing.T) {
	local, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := local.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	previousDB, previousKeyCol := DB, commonKeyCol
	t.Cleanup(func() { DB, commonKeyCol = previousDB, previousKeyCol })
	commonKeyCol = "" // The projection must not depend on initCol having run.

	for _, test := range []struct {
		name      string
		dialector gorm.Dialector
		quote     string
		predicate string
	}{
		{"sqlite", sqlite.Dialector{Conn: pool}, "`", "user_id = ? AND id IN (?,?)"},
		{"mysql", mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), "`", "user_id = ? AND id IN (?,?)"},
		{"postgres", postgres.New(postgres.Config{Conn: pool, PreferSimpleProtocol: true}), `"`, "user_id = $1 AND id IN ($2,$3)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(test.dialector, &gorm.Config{
				DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
			})
			require.NoError(t, err)
			DB = db
			var sqlText string
			var vars []interface{}
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:token-key-projection", func(tx *gorm.DB) {
				sqlText = tx.Statement.SQL.String()
				vars = append([]interface{}(nil), tx.Statement.Vars...)
			}))
			_, err = GetTokenKeysByIds([]int{11, 22}, 7)
			require.NoError(t, err)
			quote := func(name string) string { return test.quote + name + test.quote }
			require.Equal(t, fmt.Sprintf("SELECT %s,%s FROM %s WHERE (%s) AND %s.%s IS NULL",
				quote("id"), quote("key"), quote("tokens"), test.predicate, quote("tokens"), quote("deleted_at")), sqlText)
			require.Equal(t, []interface{}{7, 11, 22}, vars)
		})
	}
}
