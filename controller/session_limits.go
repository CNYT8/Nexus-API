package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// WriteSessionLimitError lets the shared login entrypoint preserve specific
// limit errors. False means the caller should use its normal error response.
func WriteSessionLimitError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, model.ErrSessionActiveLimit):
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "AUTH_SESSION_ACTIVE_LIMIT", "message": "活跃会话已达上限，请先退出其他设备"})
	case errors.Is(err, model.ErrSessionIssuanceLimit):
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "code": "AUTH_SESSION_ISSUANCE_LIMIT", "message": "当前窗口内登录次数已达上限，请稍后重试"})
	default:
		return false
	}
	return true
}

// GetUserSessionsWithLimits preserves the session list fields and adds counts
// over all owned sessions, independent of the bounded list of displayed rows.
func GetUserSessionsWithLimits(c *gin.Context) {
	user, current, ok := managedBrowserSession(c)
	if !ok {
		return
	}
	result, err := model.ListUserSessions(user.Id, current.Id)
	if err != nil {
		writeManagedSessionError(c, err)
		return
	}
	limits, err := model.GetUserSessionLimits(user.Id, current.Id)
	if err != nil {
		writeManagedSessionError(c, err)
		return
	}
	common.ApiSuccess(c, struct {
		*model.ManagedUserSessionList
		*model.UserSessionLimits
	}{ManagedUserSessionList: result, UserSessionLimits: limits})
}
