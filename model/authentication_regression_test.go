package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func setupAuthenticationRegressionTest(t *testing.T, role int) *User {
	t.Helper()
	setupUserUpdateIntegrityTest(t)
	require.NoError(t, MigrateAuthenticationSchema(DB))
	t.Cleanup(func() {
		for _, record := range []any{&UserSession{}, &AuthFlow{}, &UserAccessToken{}, &AccountSecurityLimit{}} {
			require.NoError(t, DB.Where("1 = 1").Delete(record).Error)
		}
	})
	user := &User{
		Username: "auth-regression", Password: "old-password", Role: role,
		Status: common.UserStatusEnabled, Group: "default", Email: "auth@example.test",
		Quota: 1200, UsedQuota: 50, AffQuota: 300,
		Setting: `{"unknown_auth_setting":{"preserve":true},"accept_unset_model_ratio_model":true}`,
	}
	user.SetAccessToken("legacy-auth-regression")
	require.NoError(t, DB.Create(user).Error)
	require.EqualValues(t, 1, user.AuthVersion)
	return user
}

func TestAuthenticationSessionRevocation(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleAdminUser)
	first, err := CreateUserSession(user, "password", "192.0.2.1", "first-browser")
	require.NoError(t, err)
	second, err := CreateUserSession(user, "oauth", "192.0.2.2", "second-browser")
	require.NoError(t, err)
	_, record, err := ValidateUserSession(first, user.Id)
	require.NoError(t, err)
	require.NotEqual(t, first, record.SecretHash)
	require.Greater(t, record.VerifiedAt, int64(0))

	require.NoError(t, RevokeUserSession(first, user.Id))
	_, _, err = ValidateUserSession(first, user.Id)
	require.Error(t, err)
	_, record, err = ValidateUserSession(second, user.Id)
	require.NoError(t, err, "revoking one login must preserve the other login")
	require.Zero(t, record.VerifiedAt, "OAuth alone must not grant recent local verification")

	require.NoError(t, RevokeAllUserSessions(user.Id))
	_, _, err = ValidateUserSession(second, user.Id)
	require.Error(t, err)
	_, err = CreateUserSession(user, "password", "", "stale-user-snapshot")
	require.ErrorIs(t, err, ErrAuthStateInvalid)
	require.NoError(t, DB.First(user, user.Id).Error)
	require.EqualValues(t, 2, user.AuthVersion)
	require.Empty(t, user.GetAccessToken())
	fresh, err := CreateUserSession(user, "password", "", "fresh-login")
	require.NoError(t, err)
	_, _, err = ValidateUserSession(fresh, user.Id)
	require.NoError(t, err)
}

func TestAuthenticationSecurityChangesRevokeAllCredentials(t *testing.T) {
	permissionSetting := func(t *testing.T, user *User) string {
		t.Helper()
		var setting map[string]any
		require.NoError(t, json.Unmarshal([]byte(user.Setting), &setting))
		setting["admin_permissions"] = serializeAdminPermissionConfig(AdminPermissionConfig{AdminPermissionChannel: false})
		encoded, err := json.Marshal(setting)
		require.NoError(t, err)
		return string(encoded)
	}
	tests := []struct {
		name   string
		change func(*testing.T, *User) error
	}{
		{"password-update", func(_ *testing.T, user *User) error {
			user.Password = "new-password"
			return user.Update(true)
		}},
		{"password-edit", func(_ *testing.T, user *User) error {
			user.Password = "new-password"
			return user.Edit(true)
		}},
		{"password-reset", func(_ *testing.T, user *User) error {
			return ResetUserPasswordByEmail(user.Email, "new-password")
		}},
		{"role-demotion", func(_ *testing.T, user *User) error {
			user.Role = common.RoleCommonUser
			return user.Update(false)
		}},
		{"account-ban", func(_ *testing.T, user *User) error {
			user.Status = common.UserStatusDisabled
			return user.Update(false)
		}},
		{"module-permission", func(_ *testing.T, user *User) error {
			return SetAdminPermissionConfig(user, AdminPermissionConfig{AdminPermissionChannel: false})
		}},
		{"serialized-setting", func(t *testing.T, user *User) error {
			return UpdateUserSetting(user.Id, permissionSetting(t, user))
		}},
		{"profile-setting", func(t *testing.T, user *User) error {
			user.Setting = permissionSetting(t, user)
			return user.Update(false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ACCOUNT_PASSWORD_HASH_ALGORITHM", "bcrypt")
			user := setupAuthenticationRegressionTest(t, common.RoleAdminUser)
			oldVersion := user.AuthVersion
			session, err := CreateUserSession(user, "password", "", "browser")
			require.NoError(t, err)
			pat, _, err := CreateDashboardAccessToken(user, "test", []string{"self:read"}, time.Now().Add(time.Hour).Unix())
			require.NoError(t, err)
			match := AuthFlowMatch{Purpose: "login_2fa", Provider: "password", Binding: "browser", UserId: user.Id}
			flow, err := CreateAuthFlow(match, "pending-login", time.Minute)
			require.NoError(t, err)

			require.NoError(t, tt.change(t, user))
			var current User
			require.NoError(t, DB.First(&current, user.Id).Error)
			require.Equal(t, oldVersion+1, current.AuthVersion)
			require.Empty(t, current.GetAccessToken())
			require.Equal(t, 1200, current.Quota)
			require.Equal(t, 50, current.UsedQuota)
			require.Equal(t, 300, current.AffQuota)
			_, _, err = ValidateUserSession(session, user.Id)
			require.Error(t, err)
			validated, _, err := ValidateDashboardAccessToken(pat)
			require.NoError(t, err)
			require.Nil(t, validated)
			validated, _, err = ValidateDashboardAccessToken("legacy-auth-regression")
			require.NoError(t, err)
			require.Nil(t, validated)
			_, err = ConsumeAuthFlow(flow, match)
			require.ErrorIs(t, err, ErrAuthStateInvalid)
			if strings.HasPrefix(tt.name, "password-") {
				require.True(t, common.VerifyAccountPassword("new-password", current.Password))
			}
		})
	}
}

func TestAuthenticationPermissionPatchPreservesSettingsAndAvoidsSpuriousRevocation(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleAdminUser)
	config := AdminPermissionConfig{AdminPermissionChannel: false}
	require.NoError(t, SetAdminPermissionConfig(user, config))
	version := user.AuthVersion
	session, err := CreateUserSession(user, "password", "", "browser")
	require.NoError(t, err)
	require.NoError(t, SetAdminPermissionConfig(user, config), "saving equivalent permissions must not revoke a login")
	require.NoError(t, PatchUserSetting(user.Id, map[string]any{"quota_warning_enabled": false, "quota_warning_threshold": 0}))
	require.NoError(t, DB.First(user, user.Id).Error)
	require.Equal(t, version, user.AuthVersion)
	_, _, err = ValidateUserSession(session, user.Id)
	require.NoError(t, err)
	var setting map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(user.Setting), &setting))
	require.JSONEq(t, `{"preserve":true}`, string(setting["unknown_auth_setting"]))
	require.JSONEq(t, `true`, string(setting["accept_unset_model_ratio_model"]))
	require.JSONEq(t, `false`, string(setting["quota_warning_enabled"]))
	require.False(t, GetAdminPermissionConfig(user)[AdminPermissionChannel])
}

func TestAuthenticationFlowBindingExpiryAndSingleConsumption(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	match := AuthFlowMatch{Purpose: "oauth_login", Provider: "github", Binding: "browser-a", UserId: user.Id}
	secret, err := CreateAuthFlow(match, "server-only-payload", time.Minute)
	require.NoError(t, err)
	wrongMatches := []AuthFlowMatch{
		{Purpose: "oauth_bind", Provider: match.Provider, Binding: match.Binding, UserId: user.Id},
		{Purpose: match.Purpose, Provider: "telegram", Binding: match.Binding, UserId: user.Id},
		{Purpose: match.Purpose, Provider: match.Provider, Binding: "browser-b", UserId: user.Id},
		{Purpose: match.Purpose, Provider: match.Provider, Binding: match.Binding, UserId: user.Id + 1},
	}
	for _, wrong := range wrongMatches {
		_, err := ConsumeAuthFlow(secret, wrong)
		require.ErrorIs(t, err, ErrAuthStateInvalid)
	}
	record, err := GetAuthFlow(secret, match)
	require.NoError(t, err, "mismatched requests must not consume the valid browser's flow")
	require.Zero(t, record.ConsumedAt)

	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var workers sync.WaitGroup
	for i := 0; i < contenders; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := ConsumeAuthFlow(secret, match)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrAuthStateInvalid)
		}
	}
	require.Equal(t, 1, successes)
	_, err = GetAuthFlow(secret, match)
	require.Error(t, err)

	expired, err := CreateAuthFlow(match, "expired-payload", time.Minute)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AuthFlow{}).Where("secret_hash = ?", authSecretHash("flow", expired)).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
	_, err = ConsumeAuthFlow(expired, match)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
}

func TestAuthenticationPATStorageExpiryAndIndependentRevocation(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleRootUser)
	first, firstRecord, err := CreateDashboardAccessToken(user, "first", []string{"self:read"}, time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	second, secondRecord, err := CreateDashboardAccessToken(user, "second", []string{"channel:write"}, time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	var stored UserAccessToken
	require.NoError(t, DB.First(&stored, firstRecord.Id).Error)
	require.NotEqual(t, first, stored.SecretHash)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), first)
	require.NotContains(t, string(encoded), stored.SecretHash)
	validated, token, err := ValidateDashboardAccessToken(first)
	require.NoError(t, err)
	require.NotNil(t, validated)
	require.True(t, token.Allows("self", "read"))
	require.False(t, token.Allows("system", "write"), "root ownership does not grant extra scopes")
	require.Error(t, RevokeDashboardAccessToken(user.Id+1, firstRecord.Id))
	require.NoError(t, RevokeDashboardAccessToken(user.Id, firstRecord.Id))
	validated, _, err = ValidateDashboardAccessToken(first)
	require.NoError(t, err)
	require.Nil(t, validated)
	validated, _, err = ValidateDashboardAccessToken(second)
	require.NoError(t, err)
	require.NotNil(t, validated)
	require.NoError(t, DB.Model(&UserAccessToken{}).Where("id = ?", secondRecord.Id).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
	validated, _, err = ValidateDashboardAccessToken(second)
	require.NoError(t, err)
	require.Nil(t, validated)
}

func TestAuthenticationPATIssuanceRejectsEscalationAndStaleRole(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleAdminUser)
	_, _, err := CreateDashboardAccessToken(user, "root-scope", []string{"system:write"}, time.Now().Add(time.Hour).Unix())
	require.Error(t, err)
	_, err = NormalizeAccessTokenScopes([]string{"channel:read"}, common.RoleCommonUser)
	require.Error(t, err)
	_, err = NormalizeAccessTokenScopes([]string{"system:*"}, common.RoleRootUser)
	require.Error(t, err)
	_, err = NormalizeAccessTokenScopes([]string{"*:write"}, common.RoleRootUser)
	require.Error(t, err)
	stale := *user
	user.Role = common.RoleCommonUser
	require.NoError(t, user.Update(false))
	_, _, err = CreateDashboardAccessToken(&stale, "stale-admin", []string{"channel:write"}, time.Now().Add(time.Hour).Unix())
	require.ErrorIs(t, err, ErrAuthStateInvalid)
}
