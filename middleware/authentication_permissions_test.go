package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupAuthenticationPermissions(t *testing.T, role int) *model.User {
	t.Helper()
	previousDB, previousLog := model.DB, model.LOG_DB
	previousSQLite, previousMySQL, previousPostgres, previousRedis := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled = previousSQLite, previousMySQL, previousPostgres, previousRedis
	})
	setupAuthTestDB(t, role)
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	return &user
}

func TestAuthenticationPermissionsAdminReadWriteRoutes(t *testing.T) {
	user := setupAuthenticationPermissions(t, common.RoleAdminUser)
	permissions := model.DefaultAdminPermissionConfig()
	for _, module := range model.GetAdminPermissionModules() {
		permissions[module.Key+":write"] = false
	}
	require.NoError(t, model.SetAdminPermissionConfig(user, permissions))
	for _, test := range []struct {
		method string
		path   string
		allow  bool
	}{
		{http.MethodGet, "/api/channel/", true},
		{http.MethodPut, "/api/channel/", false},
		{http.MethodGet, "/api/channel/test/1", false},
		{http.MethodGet, "/api/channel/update_balance", false},
		{http.MethodGet, "/api/channel/1/codex/usage", false},
		{http.MethodPost, "/api/channel/multi_key/manage", false},
		{http.MethodGet, "/api/group/", true},
		{http.MethodPost, "/api/models/sync_upstream", false},
		{http.MethodGet, "/api/membership/admin/tiers", true},
		{http.MethodPost, "/api/tickets/admin/1/messages", false},
		{http.MethodGet, "/api/log/export", true},
		{http.MethodDelete, "/api/log/", false},
		{http.MethodGet, "/api/user/search", true},
		{http.MethodPut, "/api/user/", false},
		{http.MethodPost, "/api/redemption/", false},
		{http.MethodPut, "/api/subscription/admin/plans", false},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(test.method, test.path, nil)
			require.Equal(t, test.allow, enforceAdminPermission(c, common.RoleAdminUser, user.Role, user.Id), recorder.Body.String())
			require.Equal(t, !test.allow, c.IsAborted())
		})
	}
	permissions["channel"] = false
	permissions["channel:read"], permissions["channel:write"] = true, true
	require.NoError(t, model.SetAdminPermissionConfig(user, permissions))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/", nil)
	require.False(t, enforceAdminPermission(c, common.RoleAdminUser, user.Role, user.Id))
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/", nil)
	require.True(t, enforceAdminPermission(c, common.RoleAdminUser, common.RoleRootUser, user.Id))
	for _, path := range []string{"/api/channels", "/api/models-extra", "/api/userland", "/api/tickets/administer"} {
		require.Empty(t, adminPermissionModuleForPath(path), path)
	}
}

func TestAuthenticationPermissionsExplicitRevealAndNexusRoutes(t *testing.T) {
	for _, path := range []string{"/api/token/1/key", "/api/token/batch/keys"} {
		for _, scopes := range []string{"", `["token:read"]`, `["token:write"]`, `["token:read","token:write"]`, `["token:reveal"]`} {
			var token *model.UserAccessToken
			if scopes != "" {
				token = &model.UserAccessToken{Scopes: scopes}
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			c.Set("role", common.RoleRootUser)
			require.Equal(t, scopes == `["token:reveal"]`, enforceAccessTokenScope(c, token), path+scopes)
		}
	}
	for _, test := range []struct{ path, module string }{
		{"/api/membership/self", "self"}, {"/api/user/rebate", "self"},
		{"/api/tickets/1", "self"}, {"/api/model_monitor/availability", "self"},
		{"/api/user/sessions", "self"}, {"/api/user/access-tokens/1", "self"},
		{"/api/channel/1/codex/usage", "channel"}, {"/api/channel/multi_key/manage", "channel"},
		{"/api/group/", "models"}, {"/api/membership/admin/tiers", "subscription"},
	} {
		require.Equal(t, test.module, accessTokenModule(test.path), test.path)
	}
	legacy, _ := gin.CreateTestContext(httptest.NewRecorder())
	legacy.Request = httptest.NewRequest(http.MethodPost, "/api/token/", nil)
	require.True(t, enforceAccessTokenScope(legacy, nil), "legacy write permission stays compatible")
	require.False(t, model.AccessTokenAllows(legacy, "token", "reveal"))
}

func TestAuthenticationPermissionsRootPATCannotBypassReveal(t *testing.T) {
	user := setupAuthenticationPermissions(t, common.RoleRootUser)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("reveal-scope-test"))))
	router.POST("/api/token/:id/key", UserAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, scope := range []string{"token:read", "token:write", "token:reveal"} {
		secret, _, err := model.CreateDashboardAccessToken(user, scope, []string{scope}, time.Now().Add(time.Hour).Unix())
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/api/token/1/key", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("New-Api-User", "1")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if scope == "token:reveal" {
			require.Equal(t, http.StatusNoContent, recorder.Code)
		} else {
			require.Equal(t, http.StatusForbidden, recorder.Code)
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
			require.Equal(t, false, payload["success"])
			require.Equal(t, "TOKEN_REVEAL_REQUIRED", payload["code"])
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		}
	}
}
