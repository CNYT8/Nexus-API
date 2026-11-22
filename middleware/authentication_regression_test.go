package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationLegacyCookieAdoptionDoesNotInheritVerification(t *testing.T) {
	setupAuthTestDB(t, common.RoleCommonUser)
	oldTurnstile := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = false
	t.Cleanup(func() { common.TurnstileCheckEnabled = oldTurnstile })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("authentication-regression-cookie-key"))))
	router.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", 1)
		session.Set("username", "tester")
		session.Set("role", common.RoleRootUser)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		session.Set(SecureVerificationSessionKey, time.Now().Unix())
		session.Set(secureVerificationMethodSessionKey, "passkey")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	var adoptedSecret string
	router.GET("/api/user/self", UserAuth(), func(c *gin.Context) {
		_, record, err := service.CurrentSessionUser(c)
		require.NoError(t, err)
		require.Equal(t, "legacy", record.LoginMethod)
		require.Zero(t, record.VerifiedAt)
		require.Empty(t, record.VerifiedMethod)
		adoptedSecret = service.SessionBinding(c)
		c.JSON(http.StatusOK, gin.H{
			"role":  c.GetInt("role"),
			"fresh": service.HasFreshSessionVerification(c, "", 5*time.Minute),
		})
	})
	router.GET("/api/user/self/sensitive", UserAuth(), SecureVerificationRequired(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	seed, originalCookies := requestWithSession(router, "/seed", "192.0.2.10", nil)
	require.Equal(t, http.StatusNoContent, seed.Code)
	require.NotEmpty(t, originalCookies)
	response, adoptedCookies := requestWithSession(router, "/api/user/self", "192.0.2.10", originalCookies)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Role  int  `json:"role"`
		Fresh bool `json:"fresh"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, common.RoleCommonUser, result.Role, "the database role must replace a stale cookie role")
	require.False(t, result.Fresh, "legacy cookie timestamps must not grant recent verification")
	require.NotEmpty(t, adoptedSecret)
	firstSecret := adoptedSecret
	response, _ = requestWithSession(router, "/api/user/self", "192.0.2.10", originalCookies)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, firstSecret, adoptedSecret)
	var count int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ?", 1).Count(&count).Error)
	require.EqualValues(t, 1, count)
	response, _ = requestWithSession(router, "/api/user/self/sensitive", "192.0.2.10", adoptedCookies)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "VERIFICATION_REQUIRED")

	require.NoError(t, model.RevokeUserSession(firstSecret, 1))
	for _, cookies := range [][]*http.Cookie{originalCookies, adoptedCookies} {
		response, _ = requestWithSession(router, "/api/user/self", "192.0.2.10", cookies)
		require.Equal(t, http.StatusUnauthorized, response.Code, "neither the original nor the adopted cookie may revive a revoked session")
	}
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ?", 1).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAuthenticationPATScopeRoutesAndGETWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		method string
		path   string
		module string
		action string
	}{
		{http.MethodGet, "/api/log/self/export", "self", "read"},
		{http.MethodGet, "/api/log/self", "self", "read"},
		{http.MethodGet, "/api/log/export", "user", "read"},
		{http.MethodPost, "/api/log/cleanup", "user", "write"},
		{http.MethodGet, "/api/log/cleanup", "user", "read"},
		{http.MethodGet, "/api/data/self", "self", "read"},
		{http.MethodGet, "/api/tickets/1", "self", "read"},
		{http.MethodPost, "/api/tickets/1/messages", "self", "write"},
		{http.MethodGet, "/api/tickets/admin", "ticket", "read"},
		{http.MethodGet, "/api/channel/test", "channel", "write"},
		{http.MethodGet, "/api/channel/test/1", "channel", "write"},
		{http.MethodHead, "/api/channel/test", "channel", "write"},
		{http.MethodGet, "/api/channel/update_balance", "channel", "write"},
		{http.MethodGet, "/api/channel/update_balance/1", "channel", "write"},
		{http.MethodGet, "/api/channel/1/codex/usage", "channel", "write"},
		{http.MethodGet, "/api/channel/1", "channel", "read"},
		{http.MethodGet, "/api/user/token", "self", "write"},
		{http.MethodGet, "/api/user/aff", "self", "write"},
		{http.MethodGet, "/api/oauth/telegram/bind", "self", "write"},
		{http.MethodPost, "/api/user/topup", "self", "write"},
		{http.MethodGet, "/api/user/topup", "user", "read"},
		{http.MethodGet, "/api/models", "self", "read"},
		{http.MethodGet, "/api/models/", "models", "read"},
		{http.MethodGet, "/api/task/plugin", "system", "read"},
		{http.MethodPost, "/api/task/plugin", "system", "write"},
		{http.MethodGet, "/api/task/plugin/schema", "system", "read"},
		{http.MethodPost, "/api/task/plugin/preview", "system", "write"},
		{http.MethodGet, "/api/task/plugin/bindings", "system", "read"},
		{http.MethodPut, "/api/task/plugin/bindings", "system", "write"},
		{http.MethodGet, "/api/task/self/task-1/artifacts", "task", "read"},
		{http.MethodGet, "/api/task/self", "self", "read"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			encoded, err := json.Marshal([]string{tt.module + ":" + tt.action})
			require.NoError(t, err)
			token := &model.UserAccessToken{Id: 42, Scopes: string(encoded)}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(tt.method, tt.path, nil)
			require.True(t, enforceAccessTokenScope(c, token))
			stored, exists := c.Get("access_token")
			require.True(t, exists)
			require.Same(t, token, stored)
			require.True(t, model.AccessTokenAllows(c, tt.module, tt.action))

			wrongAction := "write"
			if tt.action == "write" {
				wrongAction = "read"
			}
			encoded, err = json.Marshal([]string{tt.module + ":" + wrongAction})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ = gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(tt.method, tt.path, nil)
			require.False(t, enforceAccessTokenScope(c, &model.UserAccessToken{Scopes: string(encoded)}))
			require.True(t, c.IsAborted())
			require.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}
}

func TestAuthenticationCommonPATSelfServiceRoutes(t *testing.T) {
	setupAuthTestDB(t, common.RoleCommonUser)
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	secret, _, err := model.CreateDashboardAccessToken(&user, "self-service", []string{"self:read", "task:read"}, time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("authentication-regression-pat-key"))))
	paths := []string{
		"/api/log/self", "/api/log/self/export", "/api/data/self", "/api/tickets/1",
		"/api/task/self/task-1/artifacts", "/api/models",
	}
	for _, path := range paths {
		router.GET(path, UserAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("New-Api-User", "1")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusNoContent, recorder.Code, path+": "+recorder.Body.String())
	}
}

func TestAuthenticationRootPATCannotBypassScopes(t *testing.T) {
	setupAuthTestDB(t, common.RoleRootUser)
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("authentication-regression-root-key"))))
	router.GET("/api/channel/test", AdminAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/api/task/plugin", RootAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	tests := []struct {
		name   string
		path   string
		scopes []string
		status int
	}{
		{"root-read-cannot-test-channels", "/api/channel/test", []string{"channel:read"}, http.StatusForbidden},
		{"root-scoped-channel-write", "/api/channel/test", []string{"channel:write"}, http.StatusNoContent},
		{"root-task-scope-cannot-administer-plugins", "/api/task/plugin", []string{"task:read"}, http.StatusForbidden},
		{"root-scoped-system-read", "/api/task/plugin", []string{"system:read"}, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, _, err := model.CreateDashboardAccessToken(&user, tt.name, tt.scopes, time.Now().Add(time.Hour).Unix())
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header.Set("Authorization", "Bearer "+secret)
			request.Header.Set("New-Api-User", "1")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tt.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestAuthenticationPATScopeHelperCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, model.AccessTokenAllows(c, "system", "write"), "sessions retain the existing role and permission checks")
	c.Set("use_access_token", true)
	require.False(t, model.AccessTokenAllows(c, "system", "write"), "missing PAT context must fail closed")
	c.Set("access_token", "invalid-context")
	require.False(t, model.AccessTokenAllows(c, "system", "write"))
	c.Set("access_token", (*model.UserAccessToken)(nil))
	require.True(t, model.AccessTokenAllows(c, "system", "write"), "authenticated legacy PATs retain their existing permission checks")
	c.Set("role", common.RoleRootUser)
	c.Set("access_token", &model.UserAccessToken{Scopes: `["models:write"]`})
	require.True(t, model.AccessTokenAllows(c, "models", "write"))
	require.False(t, model.AccessTokenAllows(c, "channel", "write"))
	require.False(t, model.AccessTokenAllows(c, "system", "write"))
}
