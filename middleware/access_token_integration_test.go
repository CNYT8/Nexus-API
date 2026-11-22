package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationPermissionsIntegratedRouteScopes(t *testing.T) {
	for _, test := range []struct {
		name, method, path, scopes string
		allowed                    bool
	}{
		{"本人可读日志策略", http.MethodGet, "/api/log/settings", `["self:read"]`, true},
		{"日志策略不借用用户管理权限", http.MethodGet, "/api/log/settings", `["user:read"]`, false},
		{"管理员日志仍需用户管理权限", http.MethodGet, "/api/log/export", `["self:read"]`, false},
		{"脚本插件目录使用系统读取", http.MethodGet, "/api/task/script_plugin/marketplace/catalog", `["system:read"]`, true},
		{"脚本插件不借用用户管理权限", http.MethodGet, "/api/task/script_plugin", `["user:read"]`, false},
		{"激活需要系统写入", http.MethodPost, "/api/task/script_plugin/1/activate", `["system:read"]`, false},
		{"系统写入允许激活", http.MethodPost, "/api/task/script_plugin/1/activate", `["system:write"]`, true},
		{"旧插件路由保持系统权限", http.MethodGet, "/api/task/plugin/schema", `["system:read"]`, true},
		{"相似名称不误命中策略", http.MethodGet, "/api/log/settings-extra", `["self:read"]`, false},
		{"相似名称不误命中插件", http.MethodGet, "/api/task/script_plugin-extra", `["system:read"]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(test.method, test.path, nil)
			token := &model.UserAccessToken{Id: 7, Scopes: test.scopes}
			require.Equal(t, test.allowed, enforceAccessTokenScope(c, token))
			require.Equal(t, !test.allowed, c.IsAborted())
		})
	}
}
