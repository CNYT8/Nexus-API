package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func enforceAdminPermission(c *gin.Context, minRole int, role int, userId int) bool {
	if minRole != common.RoleAdminUser || role != common.RoleAdminUser {
		return true
	}

	module := adminPermissionModuleForPath(c.Request.URL.Path)
	if module == "" {
		return true
	}

	allowed, err := model.IsAdminPermissionActionAllowed(userId, module, accessTokenAction(c.Request.Method, c.Request.URL.Path))
	if err != nil {
		common.SysLog("failed to check admin permission: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
		})
		c.Abort()
		return false
	}
	if allowed {
		return true
	}

	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": common.TranslateMessage(c, i18n.MsgAuthInsufficientPrivilege),
	})
	c.Abort()
	return false
}

func adminPermissionModuleForPath(path string) string {
	path = "/" + strings.Trim(strings.TrimSpace(path), "/")

	switch {
	case permissionPathMatches(path, "/api/channel"), path == "/api/log/channel_affinity_usage_cache":
		return model.AdminPermissionChannel
	case permissionPathMatches(path, "/api/models"),
		permissionPathMatches(path, "/api/deployments"),
		permissionPathMatches(path, "/api/vendors"),
		permissionPathMatches(path, "/api/group"),
		permissionPathMatches(path, "/api/prefill_group"):
		return model.AdminPermissionModels
	case permissionPathMatches(path, "/api/user"), permissionPathMatches(path, "/api/log"),
		permissionPathMatches(path, "/api/data"), permissionPathMatches(path, "/api/mj"), permissionPathMatches(path, "/api/task"):
		return model.AdminPermissionUser
	case permissionPathMatches(path, "/api/redemption"):
		return model.AdminPermissionRedemption
	case permissionPathMatches(path, "/api/subscription/admin"), permissionPathMatches(path, "/api/membership/admin"):
		return model.AdminPermissionSubscription
	case permissionPathMatches(path, "/api/tickets/admin"):
		return model.AdminPermissionTicket
	default:
		return ""
	}
}

func permissionPathMatches(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
