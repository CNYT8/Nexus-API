package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthenticationLimitsPreserveLegacyTablesAndReceipts(t *testing.T) {
	for _, upstream := range []bool{false, true} {
		name := "legacy-nexus"
		if upstream {
			name = "upstream-archive"
		}
		t.Run(name, func(t *testing.T) {
			db := compatibilityTestDB(t)
			if upstream {
				seedUpstreamCompatibilityTables(t, db)
			} else {
				seedLegacyCompatibilityTables(t, db)
			}
			previousDB, previousLog, previousRedis := DB, LOG_DB, common.RedisEnabled
			DB, LOG_DB, common.RedisEnabled = db, db, false
			t.Cleanup(func() { DB, LOG_DB, common.RedisEnabled = previousDB, previousLog, previousRedis })
			authenticationLimits(t, 2, 2, 2)
			ddl := compatibilitySourceDDL(t, db)
			before := map[string][]map[string]interface{}{}
			for _, spec := range compatibilitySpecs() {
				var rows []map[string]interface{}
				require.NoError(t, db.Table(spec.Source).Find(&rows).Error)
				before[spec.Source] = rows
			}
			require.NoError(t, db.AutoMigrate(&User{}))
			user := &User{Id: 7, Username: "legacy-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 3}
			require.NoError(t, db.Create(user).Error)
			require.NoError(t, MigrateAuthenticationSchema(db))
			var receipts []SchemaCompatibilityMigration
			require.NoError(t, db.Order("source").Find(&receipts).Error)
			var legacyPAT UserAccessToken
			if !upstream {
				require.NoError(t, db.First(&legacyPAT, 303).Error)
				require.False(t, legacyPAT.Allows("token", "reveal"))
			}
			secret, err := CreateUserSession(user, "password", "", "new-browser")
			require.NoError(t, err)
			_, current, err := ValidateUserSession(secret, user.Id)
			require.NoError(t, err)
			limits, err := GetUserSessionLimits(user.Id, current.Id)
			require.NoError(t, err)
			wantActive := int64(2)
			if upstream {
				wantActive = 1
			}
			require.Equal(t, wantActive, limits.ActiveCount)
			require.EqualValues(t, 1, limits.IssuanceCount, "missing historical created_at must not be rewritten as a new issuance")
			_, pat, err := CreateDashboardAccessToken(user, "new-pat", []string{"token:read"}, time.Now().Add(time.Hour).Unix())
			require.NoError(t, err)
			newName := "new-name"
			_, err = UpdateDashboardAccessToken(user, pat.Id, &newName, nil)
			require.NoError(t, err)
			require.NoError(t, RevokeDashboardAccessToken(user.Id, pat.Id))
			require.NoError(t, RevokeUserSession(secret, user.Id))
			if !upstream {
				require.NoError(t, RevokeDashboardAccessToken(user.Id, 303))
				require.NoError(t, db.Model(&UserSession{}).Where("id = ?", 101).Update("revoked_at", time.Now().Unix()).Error)
			}
			require.NoError(t, MigrateAuthenticationSchema(db))
			require.Equal(t, ddl, compatibilitySourceDDL(t, db))
			for _, spec := range compatibilitySpecs() {
				var rows []map[string]interface{}
				require.NoError(t, db.Table(spec.Source).Find(&rows).Error)
				require.Equal(t, before[spec.Source], rows, spec.Source)
			}
			var afterReceipts []SchemaCompatibilityMigration
			require.NoError(t, db.Order("source").Find(&afterReceipts).Error)
			require.Equal(t, receipts, afterReceipts, "management operations must not rewrite migration receipts")
			if !upstream {
				var session UserSession
				require.NoError(t, db.First(&session, 101).Error)
				require.NotZero(t, session.RevokedAt, "a restart cannot replay the archived active session")
				require.NoError(t, db.First(&legacyPAT, 303).Error)
				require.NotZero(t, legacyPAT.RevokedAt)
				require.Equal(t, `["self:read"]`, legacyPAT.Scopes)
			} else {
				_, _, err := ValidateUserSession("sid-1", user.Id)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				owner, _, err := ValidateDashboardAccessToken("nap_upstream-token")
				require.NoError(t, err)
				require.Nil(t, owner)
				var archived UserAccessToken
				require.ErrorIs(t, db.First(&archived, 303).Error, gorm.ErrRecordNotFound)
			}
		})
	}
}

func TestAuthenticationLimitsSummaryRequiresOwnedActiveSession(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	authenticationLimits(t, 20, 50, 100)
	secret, err := CreateUserSession(user, "password", "", "")
	require.NoError(t, err)
	_, record, err := ValidateUserSession(secret, user.Id)
	require.NoError(t, err)
	limits, err := GetUserSessionLimits(user.Id, record.Id)
	require.NoError(t, err)
	require.Equal(t, &UserSessionLimits{ActiveCount: 1, ActiveLimit: 50, IssuanceCount: 1, IssuanceLimit: 100, WindowSeconds: 86400}, limits)
	other := &User{Username: "summary-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(other).Error)
	_, err = GetUserSessionLimits(other.Id, record.Id)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
	require.NoError(t, RevokeUserSession(secret, user.Id))
	_, err = GetUserSessionLimits(user.Id, record.Id)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
}
