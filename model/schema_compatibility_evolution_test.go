package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type compatibilityFutureSession struct {
	UserSession
	FutureNote   *string
	FutureNumber int64 `gorm:"not null;default:7"`
}

func (compatibilityFutureSession) TableName() string { return "nexus_user_sessions" }

func futureCompatibilitySpecs() []compatibilitySpec {
	specs := compatibilitySpecs()
	specs[0].Model = &compatibilityFutureSession{}
	return specs
}

func TestSchemaCompatibilityFutureColumnsNeverChangeArchiveContract(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-import", true: "completed-receipt"}[upgrade], func(t *testing.T) {
			db := compatibilityTestDB(t)
			seedLegacyCompatibilityTables(t, db)
			// A coincidentally matching archive extension is not part of v1.
			require.NoError(t, db.Exec(`ALTER TABLE user_sessions ADD COLUMN future_note TEXT`).Error)
			require.NoError(t, db.Exec(`ALTER TABLE user_sessions ADD COLUMN future_number INTEGER`).Error)
			require.NoError(t, db.Exec(`UPDATE user_sessions SET future_note = 'archive-only', future_number = 99`).Error)
			originalDDL := compatibilitySourceDDL(t, db)
			if upgrade {
				require.NoError(t, MigrateSchemaCompatibility(db))
				require.NoError(t, db.Model(&UserSession{}).Where("id = 101").Update("revoked_at", 1700000001).Error)
				require.NoError(t, db.Model(&AuthFlow{}).Where("id = 202").Update("consumed_at", 1700000002).Error)
				require.NoError(t, db.Delete(&UserAccessToken{}, 303).Error)
			}
			for i := 0; i < 3; i++ {
				require.NoError(t, migrateSchemaCompatibility(db, futureCompatibilitySpecs()))
			}
			require.Equal(t, originalDDL, compatibilitySourceDDL(t, db))
			var session compatibilityFutureSession
			require.NoError(t, db.First(&session, 101).Error)
			require.Nil(t, session.FutureNote)
			require.EqualValues(t, 7, session.FutureNumber)
			require.Equal(t, "session-hash", session.SecretHash)
			if upgrade {
				require.EqualValues(t, 1700000001, session.RevokedAt)
				var flow AuthFlow
				require.NoError(t, db.First(&flow, 202).Error)
				require.EqualValues(t, 1700000002, flow.ConsumedAt)
				var count int64
				require.NoError(t, db.Model(&UserAccessToken{}).Count(&count).Error)
				require.Zero(t, count)
			}
			var before, after []SchemaCompatibilityMigration
			require.NoError(t, db.Order("source").Find(&before).Error)
			require.NoError(t, db.Model(&compatibilityFutureSession{}).Where("id = 101").Updates(map[string]interface{}{"future_note": "current", "future_number": 11}).Error)
			require.NoError(t, migrateSchemaCompatibility(db, futureCompatibilitySpecs()))
			require.NoError(t, db.First(&session, 101).Error)
			require.NotNil(t, session.FutureNote)
			require.Equal(t, "current", *session.FutureNote)
			require.EqualValues(t, 11, session.FutureNumber)
			require.NoError(t, db.Order("source").Find(&after).Error)
			require.Equal(t, before, after)
			require.NoError(t, db.Migrator().DropTable(&UserSession{}))
			require.ErrorContains(t, migrateSchemaCompatibility(db, futureCompatibilitySpecs()), "completed compatibility destination nexus_user_sessions is missing")
			require.False(t, db.Migrator().HasTable(&UserSession{}))
		})
	}
}

func TestSchemaCompatibilityNewInstallationCanAppendFutureColumns(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, MigrateSchemaCompatibility(db))
	require.NoError(t, db.Create(&UserSession{Id: 17, UserId: 7, SecretHash: "fresh", AuthVersion: 3, RevokedAt: 29}).Error)
	for i := 0; i < 3; i++ {
		require.NoError(t, migrateSchemaCompatibility(db, futureCompatibilitySpecs()))
	}
	var row compatibilityFutureSession
	require.NoError(t, db.First(&row, 17).Error)
	require.Nil(t, row.FutureNote)
	require.EqualValues(t, 7, row.FutureNumber)
	require.EqualValues(t, 29, row.RevokedAt)
}

func TestSchemaCompatibilityDestinationRequiresRevocationColumn(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE nexus_user_sessions (id INTEGER PRIMARY KEY, user_id INTEGER, secret_hash TEXT, auth_version BIGINT, expires_at BIGINT)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO nexus_user_sessions VALUES (17, 7, 'preserved', 3, 4102444800)`).Error)
	require.ErrorContains(t, MigrateSchemaCompatibility(db), "missing required column revoked_at")
	require.False(t, db.Migrator().HasColumn(&UserSession{}, "revoked_at"))
	var count int64
	require.NoError(t, db.Table("nexus_user_sessions").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSchemaCompatibilityRejectsWrongDestinationUniqueIndex(t *testing.T) {
	db := compatibilityTestDB(t)
	require.NoError(t, MigrateSchemaCompatibility(db))
	index := "idx_nexus_user_sessions_secret_hash"
	require.NoError(t, db.Migrator().DropIndex(&UserSession{}, index))
	require.NoError(t, db.Exec(`CREATE INDEX idx_nexus_user_sessions_secret_hash ON nexus_user_sessions(secret_hash)`).Error)
	require.ErrorContains(t, MigrateSchemaCompatibility(db), "unexpected definition")
	var unique bool
	require.NoError(t, db.Raw(`SELECT "unique" FROM pragma_index_list('nexus_user_sessions') WHERE name = ?`, index).Scan(&unique).Error)
	require.False(t, unique)
}
