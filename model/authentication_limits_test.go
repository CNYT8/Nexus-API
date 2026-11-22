package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func authenticationLimitsDB(t *testing.T) (*gorm.DB, *User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/authentication.db?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(8)
	previousDB, previousLog := DB, LOG_DB
	previousSQLite, previousMySQL, previousPostgres, previousRedis := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled
	DB, LOG_DB = db, db
	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled = true, false, false, false
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLog
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled = previousSQLite, previousMySQL, previousPostgres, previousRedis
		_ = pool.Close()
	})
	require.NoError(t, db.AutoMigrate(&User{}, &Option{}))
	require.NoError(t, MigrateAuthenticationSchema(db))
	user := &User{Username: "credential-owner", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1,
		Setting: `{"unknown_auth_setting":{"preserve":true}}`}
	require.NoError(t, db.Create(user).Error)
	return db, user
}

func authenticationLimits(t *testing.T, accessTokens, active, issued int) {
	t.Helper()
	registered := config.GlobalConfig.Get("authentication_setting").(*operation_setting.AuthenticationSetting)
	common.OptionMapRWMutex.Lock()
	previous := *registered
	*registered = operation_setting.AuthenticationSetting{MaxAccessTokens: accessTokens, MaxActiveSessions: active,
		MaxSessionIssuance: issued, SessionWindowSeconds: 86400}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*registered = previous
		common.OptionMapRWMutex.Unlock()
	})
}

// Eight independent SQLite connections contend for the same owner, so the test
// exercises the database write lock instead of a one-connection test-pool lock.
func TestAuthenticationLimitsConcurrentIssuance(t *testing.T) {
	for _, kind := range []string{"pat", "session-active", "session-window"} {
		t.Run(kind, func(t *testing.T) {
			db, user := authenticationLimitsDB(t)
			active, issued := 3, 100
			if kind == "session-window" {
				active, issued = 50, 3
			}
			authenticationLimits(t, 3, active, issued)
			authenticationConcurrentIssuance(t, db, user, kind)
		})
	}
}

func authenticationConcurrentIssuance(t *testing.T, db *gorm.DB, user *User, kind string) {
	t.Helper()
	start := make(chan struct{})
	errorsOut := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			var secret string
			var err error
			if kind == "pat" {
				secret, _, err = CreateDashboardAccessToken(user, "parallel", []string{"self:read"}, time.Now().Add(time.Hour).Unix())
			} else {
				secret, err = CreateUserSession(user, "password", "", "parallel")
			}
			if err != nil && secret != "" {
				err = fmt.Errorf("failed issuance returned a secret: %w", err)
			}
			errorsOut <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errorsOut)
	succeeded := 0
	for err := range errorsOut {
		if err == nil {
			succeeded++
			continue
		}
		want := ErrAccessTokenLimit
		if kind == "session-active" {
			want = ErrSessionActiveLimit
		} else if kind == "session-window" {
			want = ErrSessionIssuanceLimit
		}
		require.ErrorIs(t, err, want, "lock errors are failures, not successful limit enforcement")
	}
	require.Equal(t, 3, succeeded)
	var count int64
	if kind == "pat" {
		require.NoError(t, db.Model(&UserAccessToken{}).Where("user_id = ?", user.Id).Count(&count).Error)
	} else {
		require.NoError(t, db.Model(&UserSession{}).Where("user_id = ?", user.Id).Count(&count).Error)
	}
	require.EqualValues(t, 3, count)
}

func TestAuthenticationLimitsSessionWindowBoundaries(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	authenticationLimits(t, 20, 1, 3)
	now := time.Now().Unix()
	rows := []UserSession{
		{UserId: user.Id, SecretHash: "revoked", AuthVersion: 1, CreatedAt: now - 86400, ExpiresAt: now + 100, RevokedAt: now},
		{UserId: user.Id, SecretHash: "expired", AuthVersion: 1, CreatedAt: now - 10, ExpiresAt: now},
		{UserId: user.Id, SecretHash: "invalidated", AuthVersion: 0, CreatedAt: now, ExpiresAt: now + 100},
		{UserId: user.Id, SecretHash: "outside", AuthVersion: 1, CreatedAt: now - 86401, ExpiresAt: now - 1},
		{UserId: user.Id + 100, SecretHash: "another-owner", AuthVersion: 1, CreatedAt: now, ExpiresAt: now + 100},
	}
	require.NoError(t, db.Create(&rows).Error)
	check := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
				return err
			}
			return checkUserSessionLimits(tx, user, now)
		})
	}
	require.ErrorIs(t, check(), ErrSessionIssuanceLimit, "inclusive window counts revoked, expired and invalidated rows")
	require.NoError(t, db.Model(&UserSession{}).Where("secret_hash = ?", "revoked").Update("created_at", now-86401).Error)
	require.NoError(t, check(), "expired-at-now is inactive, and other owners do not consume slots")
	require.NoError(t, db.Model(&UserSession{}).Where("secret_hash = ?", "expired").Update("expires_at", now+1).Error)
	require.ErrorIs(t, check(), ErrSessionActiveLimit)
}

func TestAuthenticationLimitsPATRotationAndHistory(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	authenticationLimits(t, 1, 50, 100)
	expires := time.Now().Add(time.Hour).Unix()
	_, original, err := RotateDashboardAccessToken(user, []string{"self:read"}, expires)
	require.NoError(t, err)
	_, replacement, err := RotateDashboardAccessToken(user, []string{"self:read"}, expires)
	require.NoError(t, err, "rotation replaces its own active slot")
	require.NotEqual(t, original.Id, replacement.Id)
	require.NoError(t, db.First(original, original.Id).Error)
	require.NotZero(t, original.RevokedAt)
	secret, record, err := CreateDashboardAccessToken(user, "blocked", []string{"self:read"}, expires)
	require.ErrorIs(t, err, ErrAccessTokenLimit)
	require.Empty(t, secret)
	require.Nil(t, record)
	require.NoError(t, RevokeDashboardAccessToken(user.Id, replacement.Id))
	_, current, err := CreateDashboardAccessToken(user, "new", []string{"self:read"}, expires)
	require.NoError(t, err)
	require.NoError(t, db.Model(&UserAccessToken{}).Where("id = ?", current.Id).Update("expires_at", time.Now().Unix()).Error)
	_, current, err = CreateDashboardAccessToken(user, "after-expiry", []string{"self:read"}, expires)
	require.NoError(t, err)
	require.NoError(t, RevokeAllUserSessions(user.Id))
	require.NoError(t, db.First(user, user.Id).Error)
	_, _, err = CreateDashboardAccessToken(user, "after-invalidation", []string{"self:read"}, expires)
	require.NoError(t, err)
	page, err := ListDashboardAccessTokensPage(user.Id, 1, 100)
	require.NoError(t, err)
	require.EqualValues(t, 5, page.Total)
	require.EqualValues(t, 1, page.ActiveCount)
}

func TestAuthenticationLimitsLegacyAdoptionCannotResetIssuance(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	authenticationLimits(t, 20, 1, 1)
	secret, err := AdoptLegacyUserSession(user, "signed-cookie-one", "", "legacy")
	require.NoError(t, err)
	again, err := AdoptLegacyUserSession(user, "signed-cookie-one", "", "legacy")
	require.NoError(t, err)
	require.Equal(t, secret, again)
	require.NoError(t, RevokeUserSession(secret, user.Id))
	again, err = AdoptLegacyUserSession(user, "signed-cookie-one", "", "legacy")
	require.NoError(t, err, "reusing the fingerprint must preserve the tombstone")
	_, _, err = ValidateUserSession(again, user.Id)
	require.Error(t, err)
	newSecret, err := AdoptLegacyUserSession(user, "signed-cookie-two", "", "legacy")
	require.ErrorIs(t, err, ErrSessionIssuanceLimit)
	require.Empty(t, newSecret)
	var count int64
	require.NoError(t, db.Model(&UserSession{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAuthenticationLimitsPATEditOwnershipAndImmutableIdentity(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	secret, original, err := CreateDashboardAccessToken(user, "original", []string{"token:read", "token:write"}, time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	require.False(t, original.Allows("token", "reveal"))
	name := "renamed"
	updated, err := UpdateDashboardAccessToken(user, original.Id, &name, nil)
	require.NoError(t, err)
	want := *original
	want.Name = name
	require.Equal(t, want, *updated)
	_, validated, err := ValidateDashboardAccessToken(secret)
	require.NoError(t, err)
	require.Equal(t, name, validated.Name)
	updated, err = UpdateDashboardAccessToken(user, original.Id, nil, []string{"token:read", "token:reveal"})
	require.NoError(t, err)
	require.True(t, updated.Allows("token", "reveal"))
	require.False(t, updated.Allows("token", "write"))
	want.Scopes = `["token:read","token:reveal"]`
	require.Equal(t, want, *updated)
	_, err = UpdateDashboardAccessToken(user, original.Id, nil, []string{})
	require.ErrorIs(t, err, ErrAccessTokenInvalid)
	other := &User{Username: "another-owner", AffCode: "another-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(other).Error)
	_, err = UpdateDashboardAccessToken(other, original.Id, &name, nil)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.ErrorIs(t, RevokeDashboardAccessToken(other.Id, original.Id), gorm.ErrRecordNotFound)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
	_, err = UpdateDashboardAccessToken(user, original.Id, nil, []string{"channel:write"})
	require.ErrorIs(t, err, ErrAccessTokenInvalid, "the stored role, not the caller snapshot, controls new scopes")
	require.NoError(t, RevokeDashboardAccessToken(user.Id, original.Id))
	_, err = UpdateDashboardAccessToken(user, original.Id, &name, nil)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "editing cannot revive revoked credentials")
	payload, err := json.Marshal(updated)
	require.NoError(t, err)
	require.NotContains(t, string(payload), secret)
	require.NotContains(t, string(payload), original.SecretHash)
}

func TestAuthenticationLimitsPATPaginationAndScopeBoundaries(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	now := time.Now().Unix()
	for i := 0; i < 105; i++ {
		require.NoError(t, db.Create(&UserAccessToken{UserId: user.Id, SecretHash: fmt.Sprintf("page-%d", i),
			Name: fmt.Sprint(i), AuthVersion: 1, CreatedAt: now, ExpiresAt: now + 100, RevokedAt: now}).Error)
	}
	require.NoError(t, db.Create(&UserAccessToken{UserId: user.Id + 100, SecretHash: "other-page", ExpiresAt: now + 100}).Error)
	first, err := ListDashboardAccessTokensPage(user.Id, 1, 100)
	require.NoError(t, err)
	second, err := ListDashboardAccessTokensPage(user.Id, 2, 100)
	require.NoError(t, err)
	require.Len(t, first.Items, 100)
	require.Len(t, second.Items, 5)
	require.EqualValues(t, 105, second.Total)
	require.EqualValues(t, 0, second.ActiveCount)
	require.Equal(t, "revoked", second.Items[0].Status)
	require.Greater(t, first.Items[99].Id, second.Items[0].Id)
	for _, args := range [][3]int{{0, 1, 20}, {user.Id, 0, 20}, {user.Id, 1, 0}, {user.Id, 1, 101}, {user.Id, int(^uint(0) >> 1), 100}} {
		_, err := ListDashboardAccessTokensPage(args[0], args[1], args[2])
		require.ErrorIs(t, err, ErrAccessTokenInvalid)
	}
	for _, scope := range []string{"token:reveal", "token:read", "token:write"} {
		normalized, err := NormalizeAccessTokenScopes([]string{scope, scope}, common.RoleCommonUser)
		require.NoError(t, err)
		require.Equal(t, []string{scope}, normalized)
	}
	for _, scope := range []string{"self:reveal", "channel:reveal", "system:reveal", "token:*", "token:reveal:write"} {
		_, err := NormalizeAccessTokenScopes([]string{scope}, common.RoleRootUser)
		require.Error(t, err)
	}
}

func TestAuthenticationLimitsFineAdminChangeRevokesOldCredentials(t *testing.T) {
	db, user := authenticationLimitsDB(t)
	secret, err := CreateUserSession(user, "password", "", "browser")
	require.NoError(t, err)
	pat, _, err := CreateDashboardAccessToken(user, "old", []string{"channel:write"}, time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	permissions := DefaultAdminPermissionConfig()
	permissions["channel:write"] = false
	version := user.AuthVersion
	require.NoError(t, SetAdminPermissionConfig(user, permissions))
	require.Equal(t, version+1, user.AuthVersion)
	_, _, err = ValidateUserSession(secret, user.Id)
	require.Error(t, err)
	validated, _, err := ValidateDashboardAccessToken(pat)
	require.NoError(t, err)
	require.Nil(t, validated)
	require.NoError(t, SetAdminPermissionConfig(user, permissions))
	require.Equal(t, version+1, user.AuthVersion, "saving equivalent grants must not revoke again")
	require.NoError(t, db.First(user, user.Id).Error)
	require.Contains(t, user.Setting, "unknown_auth_setting")
}
