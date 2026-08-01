package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupWebRiskControllerTest(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.WebRiskState{}))
	require.NoError(t, db.Create(&model.User{
		Id:       7,
		Username: "web-risk-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
	middleware.ClearWebRiskChallengeCache(7)

	oldEnabled := common.TurnstileCheckEnabled
	oldSiteKey := common.TurnstileSiteKey
	oldSecretKey := common.TurnstileSecretKey
	oldCryptoSecret := common.CryptoSecret
	oldVerifier := verifyWebRiskTurnstile
	common.TurnstileCheckEnabled = true
	common.TurnstileSiteKey = "site-key"
	common.TurnstileSecretKey = "secret-key"
	common.CryptoSecret = "web-risk-controller-test-secret"
	t.Cleanup(func() {
		middleware.ClearWebRiskChallengeCache(7)
		verifyWebRiskTurnstile = oldVerifier
		common.TurnstileCheckEnabled = oldEnabled
		common.TurnstileSiteKey = oldSiteKey
		common.TurnstileSecretKey = oldSecretKey
		common.CryptoSecret = oldCryptoSecret
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestGetWebRiskStatusImmediatelyBlocksFollowingSessionRequest(t *testing.T) {
	setupWebRiskControllerTest(t)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("web-risk-controller-session"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "web-risk-user")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 7)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/user/web-risk/status", middleware.UserAuth(), GetWebRiskStatus)
	router.GET("/api/protected", middleware.UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	cookies := login.Result().Cookies()
	request := func(path string, ip string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = ip + ":12345"
		req.Header.Set("New-Api-User", "7")
		for _, item := range cookies {
			req.AddCookie(item)
		}
		router.ServeHTTP(recorder, req)
		if nextCookies := recorder.Result().Cookies(); len(nextCookies) > 0 {
			cookies = nextCookies
		}
		return recorder
	}

	require.Equal(t, http.StatusOK, request("/api/protected", "198.51.100.1").Code)
	require.Equal(t, http.StatusOK, request("/api/user/web-risk/status", "198.51.100.2").Code)
	require.Equal(t, http.StatusOK, request("/api/user/web-risk/status", "198.51.100.3").Code)
	blocked := request("/api/protected", "198.51.100.1")
	require.Equal(t, http.StatusForbidden, blocked.Code)
	require.Contains(t, blocked.Body.String(), middleware.WebRiskVerificationRequiredCode)
}

func runWebRiskControllerRequest(method string, path string, body string, ip string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.RemoteAddr = ip + ":12345"
	ctx.Set("id", 7)
	handler(ctx)
	return recorder
}

func decodeWebRiskControllerResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

func TestGetWebRiskStatusObservesCurrentIPAndRequiresThirdDistinctIP(t *testing.T) {
	setupWebRiskControllerTest(t)

	first := runWebRiskControllerRequest(http.MethodGet, "/api/user/web-risk/status", "", "198.51.100.1", GetWebRiskStatus)
	second := runWebRiskControllerRequest(http.MethodGet, "/api/user/web-risk/status", "", "198.51.100.2", GetWebRiskStatus)
	third := runWebRiskControllerRequest(http.MethodGet, "/api/user/web-risk/status", "", "198.51.100.3", GetWebRiskStatus)

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, false, decodeWebRiskControllerResponse(t, first)["data"].(map[string]interface{})["required"])
	require.Equal(t, false, decodeWebRiskControllerResponse(t, second)["data"].(map[string]interface{})["required"])
	thirdData := decodeWebRiskControllerResponse(t, third)["data"].(map[string]interface{})
	require.Equal(t, true, thirdData["required"])
	require.Equal(t, true, thirdData["configured"])
	require.Equal(t, "site-key", thirdData["turnstile_site_key"])
}

func TestVerifyWebRiskUnlocksEntireAccountAfterTurnstileSuccess(t *testing.T) {
	setupWebRiskControllerTest(t)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		_, err := model.ObserveWebRiskIP(7, ip)
		require.NoError(t, err)
	}

	var receivedToken string
	var receivedIP string
	verifyWebRiskTurnstile = func(_ context.Context, token string, remoteIP string) error {
		receivedToken = token
		receivedIP = remoteIP
		return nil
	}
	recorder := runWebRiskControllerRequest(
		http.MethodPost,
		"/api/user/web-risk/verify",
		`{"turnstile":"verified-token"}`,
		"203.0.113.8",
		VerifyWebRisk,
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "verified-token", receivedToken)
	require.Equal(t, "203.0.113.8", receivedIP)
	status, err := model.GetWebRiskStatus(7)
	require.NoError(t, err)
	require.False(t, status.Challenged)
	require.Zero(t, status.DistinctIPs)
}

func TestVerifyWebRiskKeepsLockWhenTurnstileFails(t *testing.T) {
	setupWebRiskControllerTest(t)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		_, err := model.ObserveWebRiskIP(7, ip)
		require.NoError(t, err)
	}
	verifyWebRiskTurnstile = func(_ context.Context, _ string, _ string) error {
		return service.ErrTurnstileVerificationFailed
	}

	recorder := runWebRiskControllerRequest(
		http.MethodPost,
		"/api/user/web-risk/verify",
		`{"turnstile":"bad-token"}`,
		"203.0.113.8",
		VerifyWebRisk,
	)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	status, err := model.GetWebRiskStatus(7)
	require.NoError(t, err)
	require.True(t, status.Challenged)
}

func TestGetWebRiskStatusSkipsTrackingWhenTurnstileNotConfigured(t *testing.T) {
	setupWebRiskControllerTest(t)
	common.TurnstileSecretKey = ""

	recorder := runWebRiskControllerRequest(http.MethodGet, "/api/user/web-risk/status", "", "198.51.100.1", GetWebRiskStatus)

	require.Equal(t, http.StatusOK, recorder.Code)
	data := decodeWebRiskControllerResponse(t, recorder)["data"].(map[string]interface{})
	require.Equal(t, false, data["required"])
	require.Equal(t, false, data["configured"])
	var count int64
	require.NoError(t, model.DB.Model(&model.WebRiskState{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestGetWebRiskStatusSkipsTrackingForAccessTokenAuth(t *testing.T) {
	setupWebRiskControllerTest(t)

	for _, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/web-risk/status", nil)
		ctx.Request.RemoteAddr = ip + ":12345"
		ctx.Set("id", 7)
		ctx.Set("use_access_token", true)
		GetWebRiskStatus(ctx)
		require.Equal(t, http.StatusOK, recorder.Code)
		data := decodeWebRiskControllerResponse(t, recorder)["data"].(map[string]interface{})
		require.Equal(t, false, data["required"])
	}

	var count int64
	require.NoError(t, model.DB.Model(&model.WebRiskState{}).Count(&count).Error)
	require.Zero(t, count)
}
