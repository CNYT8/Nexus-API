package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

type additiveIndexed struct {
	ID      int64  `gorm:"primaryKey"`
	Code    string `gorm:"uniqueIndex:idx_additive_indexed_code"`
	Other   string
	Deleted int64
}

func (additiveIndexed) TableName() string { return "additive_indexed" }

type additivePartial struct {
	ID      int64  `gorm:"primaryKey"`
	Code    string `gorm:"uniqueIndex:idx_additive_partial_code,where:deleted IS NULL"`
	Deleted *int64
}

func (additivePartial) TableName() string { return "additive_partial" }

func TestSchemaAdditiveRejectsMismatchedIndexDefinition(t *testing.T) {
	for name, ddl := range map[string]string{
		"non-unique":   `CREATE INDEX idx_additive_indexed_code ON additive_indexed(code)`,
		"wrong-column": `CREATE UNIQUE INDEX idx_additive_indexed_code ON additive_indexed(other)`,
		"partial":      `CREATE UNIQUE INDEX idx_additive_indexed_code ON additive_indexed(code) WHERE deleted = 0`,
		"expression":   `CREATE UNIQUE INDEX idx_additive_indexed_code ON additive_indexed(lower(code))`,
	} {
		t.Run(name, func(t *testing.T) {
			db := compatibilityTestDB(t)
			require.NoError(t, db.Exec(`CREATE TABLE additive_indexed (id INTEGER PRIMARY KEY, code TEXT, other TEXT, deleted INTEGER)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO additive_indexed VALUES (1, 'old-code', 'old-other', 0)`).Error)
			require.NoError(t, db.Exec(ddl).Error)
			var before string
			require.NoError(t, db.Raw(`SELECT sql FROM sqlite_master WHERE name = 'idx_additive_indexed_code'`).Scan(&before).Error)
			require.ErrorContains(t, migrateAdditive(db, &additiveIndexed{}), "unexpected definition")
			var after string
			require.NoError(t, db.Raw(`SELECT sql FROM sqlite_master WHERE name = 'idx_additive_indexed_code'`).Scan(&after).Error)
			require.Equal(t, before, after)
			var row additiveIndexed
			require.NoError(t, db.First(&row, 1).Error)
			require.Equal(t, "old-code", row.Code)
			require.Equal(t, "old-other", row.Other)
		})
	}
}

func TestSchemaAdditivePreservesPartialUniqueSemantics(t *testing.T) {
	for _, correct := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrong-predicate", true: "matching-predicate"}[correct], func(t *testing.T) {
			db := compatibilityTestDB(t)
			require.NoError(t, db.Exec(`CREATE TABLE additive_partial (id INTEGER PRIMARY KEY, code TEXT, deleted INTEGER)`).Error)
			predicate := "deleted IS NOT NULL"
			if correct {
				predicate = `("deleted" IS NULL)`
			}
			require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_additive_partial_code ON additive_partial(code) WHERE `+predicate).Error)
			if !correct {
				require.ErrorContains(t, migrateAdditive(db, &additivePartial{}), "unexpected definition")
				return
			}
			for i := 0; i < 3; i++ {
				require.NoError(t, migrateAdditive(db, &additivePartial{}))
			}
			// Index parsing must not add an unconditional unique constraint.
			require.NoError(t, db.Exec(`INSERT INTO additive_partial VALUES (1, 'same', NULL), (2, 'same', 1), (3, 'same', 2)`).Error)
			require.Error(t, db.Exec(`INSERT INTO additive_partial VALUES (4, 'same', NULL)`).Error)
		})
	}
}

func TestSchemaAdditiveOptionsWithoutPrimaryKeySupportsUpsert(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE options (key TEXT, value TEXT, extension TEXT)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO options VALUES ('historical', 'old-value', 'keep')`).Error)
	for i := 0; i < 3; i++ {
		require.NoError(t, migrateAdditive(db, &Option{}))
	}
	require.NoError(t, db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&Option{Key: "historical", Value: "updated"}).Error)
	var rows []map[string]interface{}
	require.NoError(t, db.Table("options").Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "updated", rows[0]["value"])
	require.Equal(t, "keep", rows[0]["extension"])
	var ddl string
	require.NoError(t, db.Raw(`SELECT sql FROM sqlite_master WHERE name = 'options'`).Scan(&ddl).Error)
	require.Equal(t, "CREATE TABLE options (key TEXT, value TEXT, extension TEXT)", ddl)
}

func TestSchemaAdditiveOptionsConflictPreservesEveryRow(t *testing.T) {
	for name, values := range map[string]string{
		"duplicates": `('same', 'first'), ('same', 'second')`,
		"null":       `(NULL, 'historical'), ('other', 'keep')`,
	} {
		t.Run(name, func(t *testing.T) {
			db := compatibilityTestDB(t)
			require.NoError(t, db.Exec(`CREATE TABLE options (key TEXT, value TEXT)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO options VALUES `+values).Error)
			var before, after []map[string]interface{}
			require.NoError(t, db.Table("options").Order("rowid").Find(&before).Error)
			require.ErrorContains(t, migrateAdditive(db, &Option{}), "NULL or duplicate historical values")
			require.NoError(t, db.Table("options").Order("rowid").Find(&after).Error)
			require.Equal(t, before, after)
			var indexes int64
			require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pragma_index_list('options')`).Scan(&indexes).Error)
			require.Zero(t, indexes)
		})
	}
}

func TestSchemaAdditiveChecksRealPrimaryKey(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "named-constraint"}[named], func(t *testing.T) {
			db := compatibilityTestDB(t)
			ddl := `CREATE TABLE additive_required (id INTEGER, optional TEXT, owner INTEGER`
			if named {
				ddl += `, CONSTRAINT historical_pk PRIMARY KEY (id)`
			}
			require.NoError(t, db.Exec(ddl+")").Error)
			require.NoError(t, db.Exec(`INSERT INTO additive_required VALUES (81, 'preserved', 7)`).Error)
			err := migrateAdditive(db, &additiveRequired{})
			if named {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "explicit primary key migration")
			}
			var row additiveRequired
			require.NoError(t, db.First(&row, 81).Error)
			require.Equal(t, "preserved", row.Optional)
			require.EqualValues(t, 7, row.Owner)
		})
	}
}
