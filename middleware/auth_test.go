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
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "tester",
		Password: "password",
		Role:     userRole,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
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
