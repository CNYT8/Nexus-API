package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func responsesWSIdentityTestDB(t *testing.T) (*gorm.DB, *model.User, *model.Token) {
	t.Helper()
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldSQLite, oldMySQL, oldPostgreSQL := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	oldRedis, oldMode := common.RedisEnabled, gin.Mode()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = oldSQLite, oldMySQL, oldPostgreSQL
		common.RedisEnabled = oldRedis
		gin.SetMode(oldMode)
	})
	initModelListColumnNames(t)
	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	user := &model.User{Username: "responsesws", Password: "unused-password", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default", Quota: 1000, AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	token := &model.Token{UserId: user.Id, Key: "responseswskey", Name: "responses-ws", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1, AuthVersion: 1}
	require.NoError(t, db.Create(token).Error)
	return db, user, token
}

func responsesWSIdentityTestContext(key string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test"}`))
	c.Request.Header.Set("Authorization", "Bearer sk-"+key)
	c.Request.RemoteAddr = "192.0.2.1:1234"
	return c
}

func TestResponsesWebSocketRequiresAuthenticatedContext(t *testing.T) {
	for _, partial := range []string{"none", "user only", "user and token"} {
		t.Run(partial, func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			c.Request.Header.Set("Connection", "Upgrade")
			c.Request.Header.Set("Upgrade", "websocket")
			if partial != "none" {
				c.Set("id", 1)
			}
			if partial == "user and token" {
				c.Set("token_id", 1)
			}
			ResponsesWebSocket(c)
			require.Equal(t, http.StatusUnauthorized, writer.Code)
			require.True(t, c.IsAborted())
		})
	}
}

func TestResponsesWSControllerRejectsUpgradeDuringDrain(t *testing.T) {
	originalDrain := common.ShutdownRequests
	drain := common.NewRequestDrain()
	common.ShutdownRequests = drain
	t.Cleanup(func() { common.ShutdownRequests = originalDrain })
	drain.BeginDrain()
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("Connection", "Upgrade")
	c.Request.Header.Set("Upgrade", "websocket")
	c.Set("id", 1)
	c.Set("token_id", 1)
	c.Set("token_key", "authenticated")
	ResponsesWebSocket(c)
	require.Equal(t, http.StatusServiceUnavailable, writer.Code)
	require.True(t, c.IsAborted())
	require.Contains(t, writer.Body.String(), "server is shutting down")
}

func TestResponsesWSRunnerRejectsMissingCredentials(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("id", 1)
	c.Set("token_id", 1)
	runner := newResponsesWSRequestRunner(c)
	called := false
	apiErr := runner(httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test"}`)), "request-one", func(*gin.Context) *types.NewAPIError {
		called = true
		return nil
	})
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	require.False(t, called)
}

func TestResponsesWSIdentityRefreshesPermissions(t *testing.T) {
	db, user, token := responsesWSIdentityTestDB(t)
	c := responsesWSIdentityTestContext(token.Key)
	require.Nil(t, revalidateResponsesWSIdentity(c, user.Id, token.Id, nil))
	version := responsesWSIdentityVersion{user: c.GetInt64("responses_ws_user_auth_version"), token: c.GetInt64("responses_ws_token_auth_version")}
	require.Equal(t, responsesWSIdentityVersion{user: 1, token: 1}, version)
	require.NoError(t, db.Model(token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "allowed-model", "auto_groups": `["allowed-group"]`}).Error)
	require.NoError(t, db.Model(user).UpdateColumn("quota", 321).Error)
	fresh := responsesWSIdentityTestContext(token.Key)
	require.Nil(t, revalidateResponsesWSIdentity(fresh, user.Id, token.Id, &version))
	require.Equal(t, user.Id, fresh.GetInt("id"))
	require.Equal(t, token.Id, fresh.GetInt("token_id"))
	require.True(t, common.GetContextKeyBool(fresh, constant.ContextKeyTokenModelLimitEnabled))
	limits, _ := common.GetContextKey(fresh, constant.ContextKeyTokenModelLimit)
	require.Equal(t, map[string]bool{"allowed-model": true}, limits)
	require.Equal(t, []string{"allowed-group"}, fresh.GetStringSlice("token_auto_groups"))
	require.Equal(t, "Bearer sk-"+token.Key, fresh.GetHeader("Authorization"))
}

func TestResponsesWSIdentityObservesRevocationAndChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		user   bool
		fields map[string]any
		pin    bool
		status int
	}{
		{name: "token disabled", fields: map[string]any{"status": common.TokenStatusDisabled}, status: http.StatusUnauthorized},
		{name: "token rotated", fields: map[string]any{"key": "rotatedkey"}, status: http.StatusUnauthorized},
		{name: "token version changed", fields: map[string]any{"auth_version": 2}, status: http.StatusUnauthorized},
		{name: "token expired", fields: map[string]any{"expired_time": time.Now().Unix() - 60}, status: http.StatusUnauthorized},
		{name: "token quota exhausted", fields: map[string]any{"unlimited_quota": false, "remain_quota": 0}, status: http.StatusUnauthorized},
		{name: "token owner changed", fields: map[string]any{"user_id": 999}, status: http.StatusUnauthorized},
		{name: "IP permission changed", fields: map[string]any{"allow_ips": "198.51.100.0/24"}, status: http.StatusForbidden},
		{name: "user disabled", user: true, fields: map[string]any{"status": common.UserStatusDisabled}, status: http.StatusForbidden},
		{name: "password auth version changed", user: true, fields: map[string]any{"auth_version": 2}, status: http.StatusUnauthorized},
		{name: "channel pin requires fresh admin role", pin: true, status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, token := responsesWSIdentityTestDB(t)
			key := token.Key
			c := responsesWSIdentityTestContext(key)
			require.Nil(t, revalidateResponsesWSIdentity(c, user.Id, token.Id, nil))
			version := responsesWSIdentityVersion{user: user.AuthVersion, token: token.AuthVersion}
			if tc.fields != nil {
				var target any = token
				if tc.user {
					target = user
				}
				require.NoError(t, db.Model(target).UpdateColumns(tc.fields).Error)
			}
			if tc.pin {
				key += "-12"
			}
			fresh := responsesWSIdentityTestContext(key)
			fresh.Set("role", common.RoleAdminUser) // stale context must not grant access
			apiErr := revalidateResponsesWSIdentity(fresh, user.Id, token.Id, &version)
			require.NotNil(t, apiErr)
			require.Equal(t, tc.status, apiErr.StatusCode)
		})
	}
}
