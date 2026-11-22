package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSchemaRegistryStartupRecognizesEveryMigratedTable(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "legacy-nexus"
		}
		t.Run(name, func(t *testing.T) {
			db := compatibilityTestDB(t)
			previousDB, previousLogDB, previousMetaDB := DB, LOG_DB, LogMetaDB
			previousSQLite, previousMySQL, previousPostgres := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
			DB, LOG_DB, LogMetaDB = db, db, db
			common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
			initCol()
			t.Cleanup(func() {
				DB, LOG_DB, LogMetaDB = previousDB, previousLogDB, previousMetaDB
				common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = previousSQLite, previousMySQL, previousPostgres
				initCol()
			})
			var original []string
			if legacy {
				seedLegacyCompatibilityTables(t, db)
				original = compatibilitySourceDDL(t, db)
			}
			require.NoError(t, migrateDB())
			require.NoError(t, migrateDBFast())
			require.NoError(t, migrateDB())
			report := CheckDatabaseIntegrity()
			require.True(t, report.Healthy, strings.Join(report.Issues, "\n"))
			require.NotContains(t, strings.Join(report.Warnings, "\n"), "非本系统原生")
			for _, spec := range primaryDBModelSpecs() {
				require.True(t, db.Migrator().HasTable(spec.Model), spec.Name)
			}
			if legacy {
				require.Equal(t, original, compatibilitySourceDDL(t, db))
				var plugin TaskPlugin
				require.NoError(t, db.First(&plugin, 404).Error)
				require.Equal(t, "units * 2", plugin.Expression)
			}
		})
	}
}

func TestSchemaRegistryRelationalLogMetadataMatchesStartup(t *testing.T) {
	db := compatibilityTestDB(t)
	previousLogDB := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previousLogDB })
	require.NoError(t, migrateLOGDB())
	require.NoError(t, migrateLOGDB())
	for _, spec := range relationalLogDBModelSpecs() {
		require.True(t, db.Migrator().HasTable(spec.Model), spec.Name)
	}
	require.False(t, db.Migrator().HasTable(&LogIDSequence{}))
	metadata := logMetadataModelSpecs(true)
	require.Len(t, metadata, 4)
	require.Equal(t, "LogIDSequence", metadata[3].Name)
}
