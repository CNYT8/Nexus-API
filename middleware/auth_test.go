package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuthTestDB(t *testing.T, userRole int) {
	t.Helper()

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.WebRiskState{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "tester",
		Password: "password",
		Role:     userRole,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
	ClearWebRiskChallengeCache(1)

	t.Cleanup(func() {
		ClearWebRiskChallengeCache(1)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestUserAuthAccessTokenBypassesWebRiskChallenge(t *testing.T) {
	setupAuthTestDB(t, common.RoleCommonUser)
	originalEnabled := common.TurnstileCheckEnabled
	originalSiteKey := common.TurnstileSiteKey
	originalSecretKey := common.TurnstileSecretKey
	originalCryptoSecret := common.CryptoSecret
	common.TurnstileCheckEnabled = true
	common.TurnstileSiteKey = "site-key"
	common.TurnstileSecretKey = "secret-key"
	common.CryptoSecret = "web-risk-access-token-test"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = originalEnabled
		common.TurnstileSiteKey = originalSiteKey
		common.TurnstileSecretKey = originalSecretKey
		common.CryptoSecret = originalCryptoSecret
	})

	accessToken := "web-risk-management-access-token"
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("access_token", accessToken).Error)
	for _, ip := range []string{"203.0.113.1", "198.51.100.2", "192.0.2.3"} {
		_, err := model.ObserveWebRiskIP(1, ip)
		require.NoError(t, err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("web-risk-token-test"))))
	router.GET("/api/self", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	request := httptest.NewRequest(http.MethodGet, "/api/self", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("New-Api-User", "1")
	request.RemoteAddr = "192.0.2.88:12345"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"success":true}`, recorder.Body.String())
}

func updateTestSessionCookies(current []*http.Cookie, response *http.Response) []*http.Cookie {
	byName := make(map[string]*http.Cookie, len(current)+len(response.Cookies()))
	for _, cookie := range current {
		byName[cookie.Name] = cookie
	}
	for _, cookie := range response.Cookies() {
		byName[cookie.Name] = cookie
	}
	next := make([]*http.Cookie, 0, len(byName))
	for _, cookie := range byName {
		next = append(next, cookie)
	}
	return next
}

func requestWithSession(router http.Handler, path string, ip string, cookies []*http.Cookie) (*httptest.ResponseRecorder, []*http.Cookie) {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = ip + ":12345"
	request.Header.Set("New-Api-User", "1")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder, updateTestSessionCookies(cookies, recorder.Result())
}

func TestUserAuthChallengesThirdDistinctWebIPAndNewSession(t *testing.T) {
	setupAuthTestDB(t, common.RoleCommonUser)
	originalEnabled := common.TurnstileCheckEnabled
	originalSiteKey := common.TurnstileSiteKey
	originalSecretKey := common.TurnstileSecretKey
	originalCryptoSecret := common.CryptoSecret
	common.TurnstileCheckEnabled = true
	common.TurnstileSiteKey = "site-key"
	common.TurnstileSecretKey = "secret-key"
	common.CryptoSecret = "web-risk-auth-test"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = originalEnabled
		common.TurnstileSiteKey = originalSiteKey
		common.TurnstileSecretKey = originalSecretKey
		common.CryptoSecret = originalCryptoSecret
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("web-risk-auth-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "tester")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/self", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	cookies := login.Result().Cookies()

	for _, ip := range []string{"203.0.113.1", "198.51.100.2"} {
		response, nextCookies := requestWithSession(router, "/api/self", ip, cookies)
		cookies = nextCookies
		require.Equal(t, http.StatusOK, response.Code)
	}
	response, _ := requestWithSession(router, "/api/self", "192.0.2.3", cookies)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), `"code":"WEB_RISK_VERIFICATION_REQUIRED"`)

	newLogin := httptest.NewRecorder()
	router.ServeHTTP(newLogin, httptest.NewRequest(http.MethodGet, "/login", nil))
	response, _ = requestWithSession(router, "/api/self", "192.0.2.44", newLogin.Result().Cookies())
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestUserAuthDoesNotLetLocalNegativeCacheHideAccountChallenge(t *testing.T) {
	setupAuthTestDB(t, common.RoleCommonUser)
	originalEnabled := common.TurnstileCheckEnabled
	originalSiteKey := common.TurnstileSiteKey
	originalSecretKey := common.TurnstileSecretKey
	originalCryptoSecret := common.CryptoSecret
	common.TurnstileCheckEnabled = true
	common.TurnstileSiteKey = "site-key"
	common.TurnstileSecretKey = "secret-key"
	common.CryptoSecret = "web-risk-negative-cache-test"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = originalEnabled
		common.TurnstileSiteKey = originalSiteKey
		common.TurnstileSecretKey = originalSecretKey
		common.CryptoSecret = originalCryptoSecret
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("web-risk-negative-cache-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "tester")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/self", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	cookies := login.Result().Cookies()

	response, nextCookies := requestWithSession(router, "/api/self", "203.0.113.1", cookies)
	require.Equal(t, http.StatusOK, response.Code)
	cookies = nextCookies

	for _, ip := range []string{"198.51.100.2", "192.0.2.3"} {
		_, err := model.ObserveWebRiskIP(1, ip)
		require.NoError(t, err)
	}

	response, _ = requestWithSession(router, "/api/self", "203.0.113.1", cookies)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), `"code":"WEB_RISK_VERIFICATION_REQUIRED"`)
}

func TestUserAuthSkipsWebRiskWhenTurnstileIsNotConfigured(t *testing.T) {
	setupAuthTestDB(t, common.RoleCommonUser)
	originalEnabled := common.TurnstileCheckEnabled
	originalSiteKey := common.TurnstileSiteKey
	originalSecretKey := common.TurnstileSecretKey
	common.TurnstileCheckEnabled = false
	common.TurnstileSiteKey = ""
	common.TurnstileSecretKey = ""
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = originalEnabled
		common.TurnstileSiteKey = originalSiteKey
		common.TurnstileSecretKey = originalSecretKey
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("web-risk-disabled-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "tester")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/self", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	cookies := login.Result().Cookies()
	for _, ip := range []string{"203.0.113.1", "198.51.100.2", "192.0.2.3"} {
		response, nextCookies := requestWithSession(router, "/api/self", ip, cookies)
		cookies = nextCookies
		require.Equal(t, http.StatusOK, response.Code)
	}
}

func TestAdminAuthRefreshesStaleSessionRole(t *testing.T) {
	setupAuthTestDB(t, common.RoleAdminUser)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("auth-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "tester")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/admin", AdminAuth(), func(c *gin.Context) {
		session := sessions.Default(c)
		c.JSON(http.StatusOK, gin.H{
			"role":         c.GetInt("role"),
			"session_role": session.Get("role"),
		})
	})

	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	request.Header.Set("New-Api-User", "1")
	for _, cookie := range loginRecorder.Result().Cookies() {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"role":10,"session_role":10}`, recorder.Body.String())
}
