package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type additiveHistory struct {
	ID      int64   `gorm:"primaryKey"`
	Amount  float64 `gorm:"type:decimal(24,12);not null"`
	Label   string  `gorm:"type:varchar(32);not null;default:'';index"`
	Counter int64   `gorm:"not null;default:0"`
	Code    *string `gorm:"unique"`
}

func (additiveHistory) TableName() string { return "additive_history" }

type additiveRequired struct {
	ID       int64 `gorm:"primaryKey"`
	Optional string
	Owner    int64 `gorm:"not null"`
}

func (additiveRequired) TableName() string { return "additive_required" }

func TestSchemaAdditivePreservesHistoricalDefinitionsAndData(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE additive_history (id INTEGER PRIMARY KEY, amount decimal(24,12), label TEXT DEFAULT 'historical', extra TEXT)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO additive_history VALUES (71, 12.345678901234, NULL, 'preserved')`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX historical_extra ON additive_history(extra)`).Error)
	var original map[string]interface{}
	require.NoError(t, db.Raw(`SELECT id, amount, label, extra FROM additive_history`).Scan(&original).Error)
	for i := 0; i < 3; i++ {
		require.NoError(t, migrateAdditive(db, &additiveHistory{}))
	}
	var after map[string]interface{}
	require.NoError(t, db.Raw(`SELECT id, amount, label, extra FROM additive_history`).Scan(&after).Error)
	require.Equal(t, original, after)
	var ddl string
	require.NoError(t, db.Raw(`SELECT sql FROM sqlite_master WHERE name = 'additive_history'`).Scan(&ddl).Error)
	require.Contains(t, ddl, "amount decimal(24,12)")
	require.Contains(t, ddl, "label TEXT DEFAULT 'historical'")
	require.True(t, db.Migrator().HasIndex(&additiveHistory{}, "historical_extra"))
	require.True(t, db.Migrator().HasIndex(&additiveHistory{}, "idx_additive_history_label"))
	var value additiveHistory
	require.NoError(t, db.First(&value, 71).Error)
	require.Zero(t, value.Counter)
	require.Nil(t, value.Code)
	code := "unique-code"
	require.NoError(t, db.Create(&additiveHistory{ID: 72, Amount: 1, Code: &code}).Error)
	require.Error(t, db.Create(&additiveHistory{ID: 73, Amount: 2, Code: &code}).Error)
}

func TestSchemaAdditiveRejectsUnsafeRequiredColumnBeforeChangingTable(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE additive_required (id INTEGER PRIMARY KEY)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO additive_required VALUES (81)`).Error)
	err := migrateAdditive(db, &additiveRequired{})
	require.ErrorContains(t, err, "explicit backfill")
	require.False(t, db.Migrator().HasColumn(&additiveRequired{}, "optional"))
	require.False(t, db.Migrator().HasColumn(&additiveRequired{}, "owner"))
	var id int64
	require.NoError(t, db.Raw(`SELECT id FROM additive_required`).Scan(&id).Error)
	require.EqualValues(t, 81, id)
}

func TestSchemaAdditiveUniqueConflictKeepsHistoricalRows(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE additive_history (id INTEGER PRIMARY KEY, amount REAL, label TEXT, counter INTEGER DEFAULT 0, code TEXT)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO additive_history VALUES (1, 2, '', 0, 'duplicate'), (2, 3, '', 0, 'duplicate')`).Error)
	err := migrateAdditive(db, &additiveHistory{})
	require.ErrorContains(t, err, "add unique index")
	var count int64
	require.NoError(t, db.Table("additive_history").Count(&count).Error)
	require.EqualValues(t, 2, count)
}
