package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

func TestLockForUpdateEmitsRowLock(t *testing.T) {
	dummyDB, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true})
	require.NoError(t, err)
	buildSQL := func() string {
		var rows []Redemption
		return lockForUpdate(dummyDB).Where("id = ?", 1).Find(&rows).Statement.SQL.String()
	}

	oldUsingSQLite := common.UsingSQLite
	t.Cleanup(func() {
		common.UsingSQLite = oldUsingSQLite
	})

	common.UsingSQLite = false
	assert.Contains(t, buildSQL(), "FOR UPDATE")

	common.UsingSQLite = true
	assert.NotContains(t, buildSQL(), "FOR UPDATE")
}

func TestPaymentRowLockSQLAcrossSupportedDialects(t *testing.T) {
	oldUsingSQLite := common.UsingSQLite
	common.UsingSQLite = false
	t.Cleanup(func() { common.UsingSQLite = oldUsingSQLite })

	mysqlDB, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "user:password@tcp(127.0.0.1:3306)/nexus",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	mysqlSQL := lockForUpdate(mysqlDB).
		Where("`trade_no` = ?", "trade-1").
		First(&TopUp{}).Statement.SQL.String()
	assert.Contains(t, mysqlSQL, "`trade_no` = ?")
	assert.Contains(t, mysqlSQL, "FOR UPDATE")

	postgresDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  "host=127.0.0.1 user=nexus password=nexus dbname=nexus port=5432 sslmode=disable",
		PreferSimpleProtocol: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	postgresSQL := lockForUpdate(postgresDB).
		Where(`"trade_no" = ?`, "trade-1").
		First(&TopUp{}).Statement.SQL.String()
	assert.Contains(t, postgresSQL, `"trade_no" = $1`)
	assert.Contains(t, postgresSQL, "FOR UPDATE")
}
