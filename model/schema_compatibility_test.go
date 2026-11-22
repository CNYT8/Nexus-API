package model

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

func compatibilityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	return db
}

// These are historical SQL layouts, not AutoMigrate of today's renamed models.
// Optional additions are intentionally missing and extra columns/indexes remain.
func seedLegacyCompatibilityTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, ddl := range []string{
		`CREATE TABLE user_sessions (id integer PRIMARY KEY, user_id integer NOT NULL, secret_hash char(64) NOT NULL UNIQUE, auth_version bigint NOT NULL, expires_at bigint, revoked_at bigint, extension text)`,
		`CREATE TABLE auth_flows (id integer PRIMARY KEY, secret_hash char(64) NOT NULL UNIQUE, purpose varchar(64) NOT NULL, provider varchar(64), binding_hash char(64) NOT NULL, user_id integer, auth_version bigint, payload text, expires_at bigint, consumed_at bigint)`,
		`CREATE TABLE user_access_tokens (id integer PRIMARY KEY, user_id integer NOT NULL, secret_hash char(64) NOT NULL UNIQUE, scopes text, auth_version bigint, expires_at bigint, revoked_at bigint)`,
		`CREATE TABLE task_plugins (id integer PRIMARY KEY, name varchar(96), version varchar(64), schema text, expression text)`,
		`CREATE UNIQUE INDEX idx_task_plugin_revision ON task_plugins(name, version)`,
		`CREATE TABLE system_tasks (id integer PRIMARY KEY, kind varchar(48) NOT NULL, status varchar(24) NOT NULL, user_id integer, target bigint, processed bigint)`,
		`CREATE INDEX original_session_extension ON user_sessions(extension)`,
		`CREATE TABLE options (key text PRIMARY KEY, value text)`,
		`INSERT INTO options VALUES ('task_setting.usage_plugins', '{"example":404}')`,
		`INSERT INTO user_sessions VALUES (101, 7, 'session-hash', 3, 4102444800, 0, 'preserve')`,
		`INSERT INTO auth_flows VALUES (202, 'flow-hash', 'oauth', 'provider', 'browser-hash', 7, 3, '{"nonce":"x"}', 4102444800, 0)`,
		`INSERT INTO user_access_tokens VALUES (303, 7, 'pat-hash', '["self:read"]', 3, 4102444800, 0)`,
		`INSERT INTO task_plugins VALUES (404, 'example', 'v1', '{}', 'units * 2')`,
		`INSERT INTO system_tasks VALUES (505, 'log_cleanup', 'running', 7, 1234, 19)`,
	} {
		require.NoError(t, db.Exec(ddl).Error, ddl)
	}
}

func compatibilitySourceDDL(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var ddl []string
	require.NoError(t, db.Raw(`SELECT sql FROM sqlite_master WHERE tbl_name IN ('user_sessions','auth_flows','user_access_tokens','task_plugins','system_tasks') AND sql IS NOT NULL ORDER BY name`).Scan(&ddl).Error)
	return ddl
}

func TestSchemaCompatibilityLegacyNexusPreservesIDsAndReferences(t *testing.T) {
	db := compatibilityTestDB(t)
	seedLegacyCompatibilityTables(t, db)
	ddl := compatibilitySourceDDL(t, db)
	for i := 0; i < 2; i++ {
		require.NoError(t, MigrateSchemaCompatibility(db))
	}
	require.Equal(t, ddl, compatibilitySourceDDL(t, db))
	var session UserSession
	var flow AuthFlow
	var pat UserAccessToken
	var plugin TaskPlugin
	var task SystemTask
	require.NoError(t, db.First(&session, 101).Error)
	require.NoError(t, db.First(&flow, 202).Error)
	require.NoError(t, db.First(&pat, 303).Error)
	require.NoError(t, db.First(&plugin, 404).Error)
	require.NoError(t, db.First(&task, 505).Error)
	require.Equal(t, "session-hash", session.SecretHash)
	require.EqualValues(t, 3, session.AuthVersion)
	require.EqualValues(t, 7, session.UserId)
	require.EqualValues(t, 4102444800, session.ExpiresAt)
	require.Zero(t, session.CreatedAt)
	require.Zero(t, session.VerifiedAt)
	require.Equal(t, "browser-hash", flow.BindingHash)
	require.Equal(t, `{"nonce":"x"}`, flow.Payload)
	require.Equal(t, `["self:read"]`, pat.Scopes)
	require.EqualValues(t, 3, pat.AuthVersion)
	require.Equal(t, "units * 2", plugin.Expression)
	require.Nil(t, plugin.RetainResult)
	require.Zero(t, plugin.CreatedAt)
	require.Equal(t, "running", task.Status)
	require.EqualValues(t, 19, task.Processed)
	require.Zero(t, task.UpdatedAt)
	require.Zero(t, task.ProgressVersion)
	require.False(t, task.ProgressExact)
	var bindings string
	require.NoError(t, db.Table("options").Select("value").Where("key = ?", "task_setting.usage_plugins").Scan(&bindings).Error)
	require.Equal(t, `{"example":404}`, bindings)
	var receipts []SchemaCompatibilityMigration
	require.NoError(t, db.Find(&receipts).Error)
	require.Len(t, receipts, 5)
	for _, receipt := range receipts {
		require.Equal(t, compatibilityNexus, receipt.Layout)
		require.EqualValues(t, 1, receipt.CopiedRows)
		require.Positive(t, receipt.CompletedAt)
	}
	allowed, err := allowLegacySessionAdoption(db)
	require.NoError(t, err)
	require.True(t, allowed)
	// Explicit old IDs must not collide with new writes; source index names
	// remain valid while isolated revision uniqueness is independently enforced.
	newPlugin := TaskPlugin{Name: "example", Version: "v2", Schema: "{}", Expression: "1"}
	require.NoError(t, db.Create(&newPlugin).Error)
	require.Greater(t, newPlugin.ID, int64(404))
	require.Error(t, db.Create(&TaskPlugin{Name: "example", Version: "v1"}).Error)
	require.True(t, db.Migrator().HasIndex(&TaskPlugin{}, "idx_nexus_task_plugin_revision"))
}

func TestSchemaCompatibilityRestartNeverReplaysArchives(t *testing.T) {
	db := compatibilityTestDB(t)
	seedLegacyCompatibilityTables(t, db)
	require.NoError(t, MigrateSchemaCompatibility(db))
	require.NoError(t, db.Model(&UserSession{}).Where("id = 101").Update("revoked_at", 1700000001).Error)
	require.NoError(t, db.Model(&AuthFlow{}).Where("id = 202").Update("consumed_at", 1700000002).Error)
	require.NoError(t, db.Delete(&UserAccessToken{}, 303).Error)
	require.NoError(t, db.Delete(&TaskPlugin{}, 404).Error)
	require.NoError(t, db.Model(&SystemTask{}).Where("id = 505").Update("processed", 50).Error)
	// Restore/modify an old archive: an existing receipt still forbids replays.
	require.NoError(t, db.Exec(`UPDATE user_sessions SET revoked_at = 0, extension = 'restored'`).Error)
	require.NoError(t, db.Exec(`INSERT INTO task_plugins VALUES (900, 'late-old-writer', 'v1', '{}', '0')`).Error)
	for i := 0; i < 2; i++ {
		require.NoError(t, MigrateSchemaCompatibility(db))
	}
	var session UserSession
	var flow AuthFlow
	var task SystemTask
	require.NoError(t, db.First(&session, 101).Error)
	require.NoError(t, db.First(&flow, 202).Error)
	require.NoError(t, db.First(&task, 505).Error)
	require.EqualValues(t, 1700000001, session.RevokedAt)
	require.EqualValues(t, 1700000002, flow.ConsumedAt)
	require.EqualValues(t, 50, task.Processed)
	var count int64
	require.NoError(t, db.Model(&UserAccessToken{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&TaskPlugin{}).Count(&count).Error)
	require.Zero(t, count)
	// A partial restore must fail closed, not rebuild and resurrect sessions.
	require.NoError(t, db.Migrator().DropTable(&UserSession{}))
	err := MigrateSchemaCompatibility(db)
	require.ErrorContains(t, err, "completed compatibility destination nexus_user_sessions is missing")
	require.False(t, db.Migrator().HasTable(&UserSession{}))
}

func seedUpstreamCompatibilityTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, ddl := range []string{
		`CREATE TABLE user_sessions (sid varchar(64) PRIMARY KEY, user_id integer NOT NULL, version bigint, user_auth_version bigint, status varchar(20), refresh_hash varchar(64), expires_at bigint)`,
		`CREATE TABLE auth_flows (id integer PRIMARY KEY, token_hash varchar(64) NOT NULL UNIQUE, purpose varchar(64), session_id varchar(64), expires_at datetime, consumed_at datetime)`,
		`CREATE TABLE user_access_tokens (id integer PRIMARY KEY, user_id integer, token_hash varchar(64) NOT NULL UNIQUE, token_hint varchar(32), scopes text, expires_at bigint)`,
		"CREATE TABLE task_plugins (id integer PRIMARY KEY, `key` varchar(128) NOT NULL, version varchar(64), api_version integer, source text, source_hash varchar(64), enabled numeric, active numeric, created_at bigint)",
		`CREATE TABLE system_tasks (id integer PRIMARY KEY, task_id varchar(64) NOT NULL, type varchar(64) NOT NULL, status varchar(24), payload text, state text, result text, locked_by varchar(64))`,
		`INSERT INTO user_sessions VALUES ('sid-1', 7, 1, 1, 'revoked', 'upstream-refresh', 4102444800)`,
		`INSERT INTO auth_flows VALUES (202, 'upstream-flow', 'oauth', 'sid-1', '2100-01-01 00:00:00', NULL)`,
		`INSERT INTO user_access_tokens VALUES (303, 7, 'upstream-pat', 'nap_hint', '["self:read"]', 4102444800)`,
		`INSERT INTO task_plugins VALUES (404, 'provider', 'v1', 1, 'export default {}', 'source-checksum', 1, 1, 1234)`,
		`INSERT INTO system_tasks VALUES (505, 'cleanup-old', 'log_cleanup', 'pending', '{"before":1}', '{"offset":19}', '{}', 'worker-old')`,
	} {
		require.NoError(t, db.Exec(ddl).Error, ddl)
	}
}

func TestSchemaCompatibilityUpstreamArchivesNoCredentialConversion(t *testing.T) {
	db := compatibilityTestDB(t)
	seedUpstreamCompatibilityTables(t, db)
	ddl := compatibilitySourceDDL(t, db)
	before := map[string][]map[string]interface{}{}
	for _, spec := range compatibilitySpecs() {
		var rows []map[string]interface{}
		require.NoError(t, db.Table(spec.Source).Find(&rows).Error)
		before[spec.Source] = rows
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, MigrateSchemaCompatibility(db))
	}
	require.Equal(t, ddl, compatibilitySourceDDL(t, db))
	for _, spec := range compatibilitySpecs() {
		var rows []map[string]interface{}
		require.NoError(t, db.Table(spec.Source).Find(&rows).Error)
		require.Equal(t, before[spec.Source], rows)
		var count int64
		require.NoError(t, db.Model(spec.Model).Count(&count).Error)
		require.Zero(t, count)
		var receipt SchemaCompatibilityMigration
		require.NoError(t, db.Where("source = ?", spec.Source).First(&receipt).Error)
		require.Equal(t, compatibilityUpstream, receipt.Layout)
		require.Equal(t, "preserved-not-converted", receipt.Disposition)
	}
	allowed, err := allowLegacySessionAdoption(db)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestSchemaCompatibilityConflictRollbackAndEqualResume(t *testing.T) {
	for _, unique := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-ID-different-data", true: "different-ID-same-secret"}[unique], func(t *testing.T) {
			db := compatibilityTestDB(t)
			seedLegacyCompatibilityTables(t, db)
			require.NoError(t, db.Exec(`INSERT INTO user_sessions VALUES (102, 7, 'second-hash', 3, 4102444800, 0, '')`).Error)
			require.NoError(t, ensureCompatibilityTable(db, &UserSession{}))
			id, hash := int64(102), "different-hash"
			if unique {
				id, hash = 999, "second-hash"
			}
			existing := UserSession{Id: id, UserId: 7, SecretHash: hash, AuthVersion: 3, CreatedAt: 17, ExpiresAt: 4102444800}
			require.NoError(t, db.Create(&existing).Error)
			require.ErrorContains(t, MigrateSchemaCompatibility(db), "conflict")
			var count int64
			require.NoError(t, db.Model(&UserSession{}).Count(&count).Error)
			require.EqualValues(t, 1, count) // first row in the same batch rolled back
			var preserved UserSession
			require.NoError(t, db.First(&preserved, id).Error)
			require.Equal(t, existing, preserved)
			require.NoError(t, db.Model(&SchemaCompatibilityMigration{}).Where("source = ?", "user_sessions").Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Delete(&UserSession{}, id).Error)
			// Simulate an old interrupted importer that committed an identical row.
			require.NoError(t, db.Exec(`INSERT INTO nexus_user_sessions (id,user_id,secret_hash,auth_version,created_at,expires_at,revoked_at,verified_at,verified_method,login_method,ip,user_agent) VALUES (101,7,'session-hash',3,0,4102444800,0,0,'','','','')`).Error)
			require.NoError(t, MigrateSchemaCompatibility(db))
			require.NoError(t, db.Model(&UserSession{}).Count(&count).Error)
			require.EqualValues(t, 2, count)
		})
	}
}

func TestSchemaCompatibilityRejectsUnknownMixedAndMissingRequired(t *testing.T) {
	for name, ddl := range map[string]string{
		"unknown":            `CREATE TABLE user_sessions (id integer PRIMARY KEY, custom text)`,
		"missing-revocation": `CREATE TABLE user_sessions (id integer PRIMARY KEY, secret_hash char(64), user_id integer, auth_version bigint, expires_at bigint)`,
		"mixed":              `CREATE TABLE user_sessions (id integer PRIMARY KEY, sid varchar(64), secret_hash char(64))`,
		"time-type":          `CREATE TABLE user_sessions (id integer PRIMARY KEY, user_id integer, secret_hash char(64), auth_version bigint, expires_at datetime, revoked_at bigint)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := compatibilityTestDB(t)
			require.NoError(t, db.Exec(ddl).Error)
			before := compatibilitySourceDDL(t, db)
			require.Error(t, MigrateSchemaCompatibility(db))
			require.False(t, db.Migrator().HasTable(&SchemaCompatibilityMigration{}))
			require.Equal(t, before, compatibilitySourceDDL(t, db))
		})
	}
	db := compatibilityTestDB(t)
	require.NoError(t, ensureCompatibilityTable(db, &SchemaCompatibilityMigration{}))
	allowed, err := allowLegacySessionAdoption(db)
	require.Error(t, err)
	require.False(t, allowed) // incomplete receipts never enable the legacy path
}

// Hermetic dialect checks: real GORM PostgreSQL/MySQL SQL generation, no DSNs,
// network or real server. Server execution/engine metadata is a separate boundary.
func TestSchemaCompatibilityDialectSQLAndTypes(t *testing.T) {
	pool, err := compatibilityTestDB(t).DB()
	require.NoError(t, err)
	for name, dialect := range map[string]gorm.Dialector{
		"postgres": postgres.New(postgres.Config{Conn: pool, PreferSimpleProtocol: true}),
		"mysql":    mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}),
	} {
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(dialect, &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
			require.NoError(t, err)
			stmt := db.Table((TaskPlugin{}).TableName()).Create(map[string]interface{}{"id": int64(404), "name": "old", "version": "v1", "schema": "{}", "expression": "1"}).Statement
			require.NoError(t, stmt.Error)
			sqlText := stmt.SQL.String()
			require.Contains(t, sqlText, quoteIdentifierReadOnly(name, "nexus_task_plugins"))
			require.NotContains(t, strings.ToUpper(sqlText), "CONFLICT")
			require.NotContains(t, strings.ToUpper(sqlText), "DUPLICATE")
			require.Contains(t, stmt.Vars, int64(404))
			if name == "postgres" {
				seq := db.Exec(compatibilitySequenceSQL, "public.nexus_task_plugins_id_seq", "public.nexus_task_plugins_id_seq", int64(404)).Statement
				require.NoError(t, seq.Error)
				require.Contains(t, seq.SQL.String(), "setval(CAST($1 AS regclass), GREATEST(COALESCE(pg_sequence_last_value(CAST($2 AS regclass)), 0), $3), true)")
				require.Equal(t, []interface{}{"public.nexus_task_plugins_id_seq", "public.nexus_task_plugins_id_seq", int64(404)}, seq.Vars)
			}
		})
	}
	for _, sample := range []struct {
		typ   string
		field schema.DataType
		valid bool
	}{
		{"int8", schema.Int, true}, {"bigint", schema.Int, true}, {"INTEGER", schema.Int, true},
		{"timestamp with time zone", schema.Int, false}, {"datetime", schema.Int, false},
		{"numeric", schema.Bool, true}, {"tinyint", schema.Bool, true}, {"boolean", schema.Bool, true},
		{"character varying", schema.String, true}, {"longtext", schema.String, true},
	} {
		column := migrator.ColumnType{DataTypeValue: sql.NullString{String: sample.typ, Valid: true}}
		require.Equal(t, sample.valid, compatibilityColumnMatches(column, &schema.Field{DataType: sample.field}), sample.typ)
	}
}
