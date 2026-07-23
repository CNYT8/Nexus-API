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

	allowed, err := model.IsAdminPermissionAllowed(userId, module)
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
	case strings.HasPrefix(path, "/api/channel"):
		return model.AdminPermissionChannel
	case strings.HasPrefix(path, "/api/models"),
		strings.HasPrefix(path, "/api/deployments"),
		strings.HasPrefix(path, "/api/vendors"),
		strings.HasPrefix(path, "/api/prefill_group"):
		return model.AdminPermissionModels
	case strings.HasPrefix(path, "/api/user"):
		return model.AdminPermissionUser
	case strings.HasPrefix(path, "/api/redemption"):
		return model.AdminPermissionRedemption
	case strings.HasPrefix(path, "/api/subscription/admin"):
		return model.AdminPermissionSubscription
	case strings.HasPrefix(path, "/api/tickets/admin"):
		return model.AdminPermissionTicket
	default:
		return ""
	}
}
