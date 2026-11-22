package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func authenticationControllerDB(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()
	previousDB, previousLog := model.DB, model.LOG_DB
	previousSQLite, previousMySQL, previousPostgres, previousRedis := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL, common.RedisEnabled = previousSQLite, previousMySQL, previousPostgres, previousRedis
	})
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.Token{}))
	require.NoError(t, model.MigrateAuthenticationSchema(db))
	user := &model.User{Username: "auth-controller", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	return db, user
}

func accessTokenControllerRequest(t *testing.T, user *model.User, secret, method, target string, body any, contextID int, pat bool) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("auth-management-contract-test"))))
	router.Use(func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set(service.AuthSessionKey, secret)
		c.Set("id", contextID)
		c.Set("use_access_token", pat)
		c.Set("account_security_checked", user.Id)
		c.Next()
	})
	router.GET("/api/user/access-tokens", GetUserAccessTokensPage)
	router.POST("/api/user/access-tokens", CreateManagedUserAccessToken)
	router.PUT("/api/user/access-tokens/:id", UpdateUserAccessToken)
	router.DELETE("/api/user/access-tokens/:id", RevokeManagedUserAccessToken)
	router.GET("/api/user/sessions", GetUserSessionsWithLimits)
	router.GET("/api/user/token", GenerateManagedAccessToken)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(method, target, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	return recorder
}

func TestAuthenticationControllerPATPageEditAndCreationContracts(t *testing.T) {
	db, user := authenticationControllerDB(t)
	secret, err := model.CreateUserSession(user, "password", "", "")
	require.NoError(t, err)
	expires := time.Now().Add(time.Hour).Unix()
	created := accessTokenControllerRequest(t, user, secret, http.MethodPost, "/api/user/access-tokens",
		gin.H{"name": "original", "scopes": []string{"token:read"}, "expires_at": expires, "user_id": user.Id + 10}, user.Id, false)
	require.Equal(t, http.StatusOK, created.Code)
	response := decodeAPIResponse(t, created)
	require.True(t, response.Success)
	var issued struct {
		Token  string                `json:"token"`
		Record model.UserAccessToken `json:"record"`
	}
	require.NoError(t, json.Unmarshal(response.Data, &issued))
	require.Contains(t, issued.Token, model.DashboardAccessTokenPrefix)
	owner, original, err := model.ValidateDashboardAccessToken(issued.Token)
	require.NoError(t, err)
	require.Equal(t, user.Id, owner.Id, "body-supplied ownership must be ignored")
	target := fmt.Sprintf("/api/user/access-tokens/%d", original.Id)
	edited := accessTokenControllerRequest(t, user, secret, http.MethodPut, target,
		gin.H{"name": "renamed", "scopes": []string{"token:read", "token:reveal"}, "expires_at": expires + 100, "revoked_at": 1}, user.Id, false)
	require.Equal(t, http.StatusOK, edited.Code)
	require.True(t, decodeAPIResponse(t, edited).Success)
	require.NotContains(t, edited.Body.String(), issued.Token)
	require.NotContains(t, edited.Body.String(), original.SecretHash)
	_, updated, err := model.ValidateDashboardAccessToken(issued.Token)
	require.NoError(t, err)
	require.Equal(t, original.Id, updated.Id)
	require.Equal(t, original.SecretHash, updated.SecretHash)
	require.Equal(t, original.CreatedAt, updated.CreatedAt)
	require.Equal(t, original.ExpiresAt, updated.ExpiresAt)
	require.Zero(t, updated.RevokedAt)
	require.True(t, updated.Allows("token", "reveal"))
	pageResponse := accessTokenControllerRequest(t, user, secret, http.MethodGet, "/api/user/access-tokens?page=1&page_size=1", nil, user.Id, false)
	var page model.DashboardAccessTokenPage
	require.NoError(t, json.Unmarshal(decodeAPIResponse(t, pageResponse).Data, &page))
	require.Equal(t, 1, page.Page)
	require.Equal(t, 1, page.PageSize)
	require.EqualValues(t, 1, page.Total)
	require.EqualValues(t, 1, page.ActiveCount)
	require.Len(t, page.Items, 1)
	require.Equal(t, "active", page.Items[0].Status)
	require.Equal(t, `["token:read","token:reveal"]`, page.Items[0].Scopes)
	for _, query := range []string{"page=0", "page=abc", "page_size=0", "page_size=101", "page_size=no"} {
		recorder := accessTokenControllerRequest(t, user, secret, http.MethodGet, "/api/user/access-tokens?"+query, nil, user.Id, false)
		require.Equal(t, http.StatusBadRequest, recorder.Code, query)
	}
	for _, body := range []any{gin.H{}, gin.H{"scopes": []string{}}, gin.H{"scopes": []string{"system:write"}}} {
		recorder := accessTokenControllerRequest(t, user, secret, http.MethodPut, target, body, user.Id, false)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	other := &model.User{Username: "pat-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(other).Error)
	_, foreign, err := model.CreateDashboardAccessToken(other, "foreign", []string{"self:read"}, expires)
	require.NoError(t, err)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		recorder := accessTokenControllerRequest(t, user, secret, method, fmt.Sprintf("/api/user/access-tokens/%d", foreign.Id), gin.H{"name": "stolen"}, user.Id, false)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	}
	revoked := accessTokenControllerRequest(t, user, secret, http.MethodDelete, target, nil, user.Id, false)
	require.True(t, decodeAPIResponse(t, revoked).Success)
	owner, _, err = model.ValidateDashboardAccessToken(issued.Token)
	require.NoError(t, err)
	require.Nil(t, owner)
}

func TestAuthenticationControllerPATRequiresOwnedVerifiedBrowser(t *testing.T) {
	db, user := authenticationControllerDB(t)
	secret, err := model.CreateUserSession(user, "password", "", "")
	require.NoError(t, err)
	_, record, err := model.CreateDashboardAccessToken(user, "existing", []string{"self:read"}, time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/user/access-tokens"}, {http.MethodGet, "/api/user/sessions"},
		{http.MethodGet, "/api/user/token"},
		{http.MethodPost, "/api/user/access-tokens"},
		{http.MethodPut, fmt.Sprintf("/api/user/access-tokens/%d", record.Id)},
		{http.MethodDelete, fmt.Sprintf("/api/user/access-tokens/%d", record.Id)},
	} {
		pat := accessTokenControllerRequest(t, user, secret, route.method, route.path, nil, user.Id, true)
		require.Equal(t, http.StatusForbidden, pat.Code)
		require.Contains(t, pat.Body.String(), "AUTH_SESSION_REQUIRED")
		mismatch := accessTokenControllerRequest(t, user, secret, route.method, route.path, nil, user.Id+1, false)
		require.Equal(t, http.StatusUnauthorized, mismatch.Code)
		require.Contains(t, mismatch.Body.String(), "AUTH_SESSION_INVALID")
	}
	_, current, err := model.ValidateUserSession(secret, user.Id)
	require.NoError(t, err)
	for _, verifiedAt := range []int64{time.Now().Unix() - 300, time.Now().Unix() + 3600, 0} {
		require.NoError(t, db.Model(&model.UserSession{}).Where("id = ?", current.Id).Update("verified_at", verifiedAt).Error)
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			target := "/api/user/access-tokens"
			if method != http.MethodPost {
				target = fmt.Sprintf("%s/%d", target, record.Id)
			}
			recorder := accessTokenControllerRequest(t, user, secret, method, target, gin.H{"name": "new"}, user.Id, false)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Contains(t, recorder.Body.String(), "VERIFICATION_REQUIRED")
		}
		legacy := accessTokenControllerRequest(t, user, secret, http.MethodGet, "/api/user/token", nil, user.Id, false)
		require.Equal(t, http.StatusForbidden, legacy.Code)
		require.Contains(t, legacy.Body.String(), "VERIFICATION_REQUIRED")
		list := accessTokenControllerRequest(t, user, secret, http.MethodGet, "/api/user/access-tokens", nil, user.Id, false)
		require.True(t, decodeAPIResponse(t, list).Success, "listing does not require step-up verification")
	}
}

func TestAuthenticationControllerLimitsAndSessionSummary(t *testing.T) {
	_, user := authenticationControllerDB(t)
	registered := config.GlobalConfig.Get("authentication_setting").(*operation_setting.AuthenticationSetting)
	common.OptionMapRWMutex.Lock()
	previous := *registered
	registered.MaxAccessTokens = 1
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*registered = previous
		common.OptionMapRWMutex.Unlock()
	})
	secret, err := model.CreateUserSession(user, "password", "", "")
	require.NoError(t, err)
	body := gin.H{"name": "one", "scopes": []string{"self:read"}, "expires_at": time.Now().Add(time.Hour).Unix()}
	first := accessTokenControllerRequest(t, user, secret, http.MethodPost, "/api/user/access-tokens", body, user.Id, false)
	require.True(t, decodeAPIResponse(t, first).Success)
	second := accessTokenControllerRequest(t, user, secret, http.MethodPost, "/api/user/access-tokens", body, user.Id, false)
	require.Equal(t, http.StatusConflict, second.Code)
	require.Contains(t, second.Body.String(), "ACCESS_TOKEN_LIMIT")
	summary := accessTokenControllerRequest(t, user, secret, http.MethodGet, "/api/user/sessions", nil, user.Id, false)
	var data struct {
		model.ManagedUserSessionList
		model.UserSessionLimits
	}
	require.NoError(t, json.Unmarshal(decodeAPIResponse(t, summary).Data, &data))
	require.EqualValues(t, 1, data.ActiveCount)
	require.EqualValues(t, 1, data.IssuanceCount)
	require.Equal(t, 50, data.ActiveLimit)
	require.Equal(t, 100, data.IssuanceLimit)
	require.EqualValues(t, 86400, data.WindowSeconds)
	require.Len(t, data.Items, 1)
	require.True(t, data.Items[0].Current)
	require.NotContains(t, summary.Body.String(), secret)
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("wrapped: %w", model.ErrSessionActiveLimit), http.StatusConflict, "AUTH_SESSION_ACTIVE_LIMIT"},
		{model.ErrSessionIssuanceLimit, http.StatusTooManyRequests, "AUTH_SESSION_ISSUANCE_LIMIT"},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		require.True(t, WriteSessionLimitError(c, test.err))
		require.Equal(t, test.status, recorder.Code)
		require.Contains(t, recorder.Body.String(), test.code)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.False(t, WriteSessionLimitError(c, model.ErrAuthStateInvalid))
	require.Empty(t, recorder.Body.String())
}

func TestAuthenticationControllerLegacyGeneratorNeverInfersReveal(t *testing.T) {
	_, user := authenticationControllerDB(t)
	secret, err := model.CreateUserSession(user, "password", "", "")
	require.NoError(t, err)
	generated := accessTokenControllerRequest(t, user, secret, http.MethodGet, "/api/user/token", nil, user.Id, false)
	var token string
	require.NoError(t, json.Unmarshal(decodeAPIResponse(t, generated).Data, &token))
	owner, record, err := model.ValidateDashboardAccessToken(token)
	require.NoError(t, err)
	require.NotNil(t, owner)
	require.NotNil(t, record)
	require.Equal(t, user.Id, owner.Id)
	require.True(t, record.Allows("token", "read"))
	require.True(t, record.Allows("token", "write"))
	require.False(t, record.Allows("token", "reveal"))
}

// This deliberately exercises the shared setupLogin entrypoint so an omitted
// WriteSessionLimitError integration cannot pass by testing the helper alone.
func TestAuthenticationControllerLoginReportsSessionLimits(t *testing.T) {
	for _, issuance := range []bool{false, true} {
		t.Run(fmt.Sprint(issuance), func(t *testing.T) {
			db, user := authenticationControllerDB(t)
			registered := config.GlobalConfig.Get("authentication_setting").(*operation_setting.AuthenticationSetting)
			common.OptionMapRWMutex.Lock()
			previous := *registered
			registered.MaxActiveSessions, registered.MaxSessionIssuance = 1, 100
			if issuance {
				registered.MaxActiveSessions, registered.MaxSessionIssuance = 50, 1
			}
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				*registered = previous
				common.OptionMapRWMutex.Unlock()
			})
			secret, err := model.CreateUserSession(user, "password", "", "")
			require.NoError(t, err)
			if issuance {
				require.NoError(t, model.RevokeUserSession(secret, user.Id))
			}
			c, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/login", nil, user.Id)
			setupLogin(user, c)
			status, code := http.StatusConflict, "AUTH_SESSION_ACTIVE_LIMIT"
			if issuance {
				status, code = http.StatusTooManyRequests, "AUTH_SESSION_ISSUANCE_LIMIT"
			}
			require.Equal(t, status, recorder.Code)
			require.Contains(t, recorder.Body.String(), code)
			require.Empty(t, recorder.Header().Get("Set-Cookie"))
			var count int64
			require.NoError(t, db.Model(&model.UserSession{}).Where("user_id = ?", user.Id).Count(&count).Error)
			require.EqualValues(t, 1, count, "rejected login must not mint a session")
		})
	}
}
