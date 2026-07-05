package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTurnstileCheckSkipsDisabledRegister(t *testing.T) {
	oldTurnstileCheckEnabled := common.TurnstileCheckEnabled
	oldRegisterEnabled := common.RegisterEnabled
	common.TurnstileCheckEnabled = true
	common.RegisterEnabled = false
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = oldTurnstileCheckEnabled
		common.RegisterEnabled = oldRegisterEnabled
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("turnstile-test"))))
	router.POST("/api/user/register", TurnstileCheck(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	router.POST("/api/user/login", TurnstileCheck(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	registerRecorder := httptest.NewRecorder()
	registerRequest := httptest.NewRequest(http.MethodPost, "/api/user/register", nil)
	router.ServeHTTP(registerRecorder, registerRequest)
	require.Equal(t, http.StatusOK, registerRecorder.Code)
	require.JSONEq(t, `{"success":true}`, registerRecorder.Body.String())

	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/user/login", nil)
	router.ServeHTTP(loginRecorder, loginRequest)
	require.Equal(t, http.StatusOK, loginRecorder.Code)
	require.JSONEq(t, `{"success":false,"message":"Turnstile token 为空"}`, loginRecorder.Body.String())
}
