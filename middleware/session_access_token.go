package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func accessTokenModule(path string) string {
	// These are distinct registered routes: dashboard /api/models and the
	// administrator catalog /api/models/. Do not collapse them before matching.
	if path == "/api/models" {
		return "self"
	}
	path = "/" + strings.Trim(path, "/")
	switch {
	case path == "/api/task/plugin" || strings.HasPrefix(path, "/api/task/plugin/") ||
		path == "/api/task/script_plugin" || strings.HasPrefix(path, "/api/task/script_plugin/"):
		return "system"
	case strings.HasPrefix(path, "/api/task/self/") && strings.HasSuffix(path, "/artifacts"):
		return "task"
	case path == "/api/verify", path == "/api/membership/self", path == "/api/log/settings",
		path == "/api/model_monitor", path == "/api/model_monitor/availability",
		path == "/api/oauth/email/bind", path == "/api/oauth/wechat/bind", path == "/api/oauth/telegram/bind":
		return "self"
	case (path == "/api/tickets" || strings.HasPrefix(path, "/api/tickets/")) && path != "/api/tickets/admin" && !strings.HasPrefix(path, "/api/tickets/admin/"):
		return "self"
	case (path == "/api/empty-responses" || strings.HasPrefix(path, "/api/empty-responses/")) && path != "/api/empty-responses/settings":
		return "self"
	case path == "/api/token" || strings.HasPrefix(path, "/api/token/"):
		return "token"
	}
	// Explicit self-service paths must precede the broader admin /api/user rule.
	// Keep numeric account IDs and admin 2FA statistics in the user module.
	for _, selfPath := range []string{
		"/api/user/self", "/api/user/web-risk", "/api/user/models", "/api/user/token",
		"/api/user/access-tokens", "/api/user/sessions", "/api/user/passkey", "/api/user/aff", "/api/user/rebate",
		"/api/user/topup/info", "/api/user/topup/self", "/api/user/pay", "/api/user/amount",
		"/api/user/stripe", "/api/user/creem", "/api/user/waffo", "/api/user/waffo-pancake",
		"/api/user/aff_transfer", "/api/user/setting", "/api/user/2fa/status", "/api/user/2fa/setup",
		"/api/user/2fa/enable", "/api/user/2fa/disable", "/api/user/2fa/backup_codes",
		"/api/user/checkin", "/api/user/oauth/bindings",
		"/api/log/self", "/api/data/self", "/api/mj/self", "/api/task/self",
	} {
		if path == selfPath || strings.HasPrefix(path, selfPath+"/") {
			return "self"
		}
	}
	if strings.HasPrefix(path, "/api/subscription/") && path != "/api/subscription/admin" && !strings.HasPrefix(path, "/api/subscription/admin/") {
		return "self"
	}
	if path == "/api/log/channel_affinity_usage_cache" {
		return "channel"
	}
	for _, adminPath := range []string{"/api/log", "/api/data", "/api/mj", "/api/task"} {
		if path == adminPath || strings.HasPrefix(path, adminPath+"/") {
			return "user"
		}
	}
	if module := adminPermissionModuleForPath(path); module != "" {
		return module
	}
	return "system"
}

func accessTokenAction(method, path string) string {
	path = "/" + strings.Trim(path, "/")
	if path == "/api/token/batch/keys" ||
		(strings.HasPrefix(path, "/api/token/") && strings.HasSuffix(path, "/key")) {
		return "reveal"
	}
	if method != http.MethodGet && method != http.MethodHead {
		return "write"
	}
	// These historical GET handlers create credentials or persist channel state.
	if path == "/api/user/token" || path == "/api/user/aff" || path == "/api/oauth/telegram/bind" ||
		path == "/api/channel/test" || strings.HasPrefix(path, "/api/channel/test/") ||
		path == "/api/channel/update_balance" || strings.HasPrefix(path, "/api/channel/update_balance/") ||
		(strings.HasPrefix(path, "/api/channel/") && strings.HasSuffix(path, "/codex/usage")) {
		return "write"
	}
	return "read"
}

func enforceAccessTokenScope(c *gin.Context, token *model.UserAccessToken) bool {
	action := accessTokenAction(c.Request.Method, c.Request.URL.Path)
	if token == nil && action != "reveal" {
		// Legacy credentials keep their old scope behavior, but gain no reveal grant.
		c.Set("access_token", token)
		return true
	}
	path := c.Request.URL.Path
	module := accessTokenModule(path)
	if path == "/api/user/topup" && c.Request.Method == http.MethodPost {
		module = "self"
	}
	if token.Allows(module, action) {
		c.Set("access_token", token)
		c.Set("access_token_id", token.Id)
		return true
	}
	response := gin.H{"success": false, "message": common.TranslateMessage(c, i18n.MsgAuthInsufficientPrivilege)}
	if action == "reveal" {
		response["code"] = "TOKEN_REVEAL_REQUIRED"
		c.Header("Cache-Control", "no-store")
	}
	c.JSON(http.StatusForbidden, response)
	c.Abort()
	return false
}
