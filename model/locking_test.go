package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
