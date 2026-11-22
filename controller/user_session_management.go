package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func managedBrowserSession(c *gin.Context) (*model.User, *model.UserSession, bool) {
	c.Header("Cache-Control", "no-store")
	if c.GetBool("use_access_token") {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "AUTH_SESSION_REQUIRED", "message": "请使用当前浏览器登录会话管理设备"})
		return nil, nil, false
	}
	user, record, err := service.CurrentSessionUser(c)
	if err != nil || user == nil || record == nil || user.Id != c.GetInt("id") {
		if err != nil && !errors.Is(err, model.ErrAuthStateInvalid) && !errors.Is(err, gorm.ErrRecordNotFound) {
			writeManagedSessionError(c, err)
		} else {
			writeManagedSessionError(c, model.ErrAuthStateInvalid)
		}
		return nil, nil, false
	}
	return user, record, true
}

func writeManagedSessionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrManagedSessionNotActive):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "AUTH_SESSION_NOT_ACTIVE", "message": "会话不存在或已失效，请刷新列表"})
	case errors.Is(err, model.ErrAuthStateInvalid), errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_SESSION_INVALID", "message": "当前登录会话已失效，请重新登录"})
	default:
		common.SysError("session management: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "AUTH_SESSION_ERROR", "message": "会话管理失败，请刷新后重试"})
	}
}

// Preserve the existing access-token list shape while adding an authoritative
// status for tokens invalidated by an account security-version change.
func GetManagedUserAccessTokens(c *gin.Context) {
	user, _, ok := managedBrowserSession(c)
	if !ok {
		return
	}
	tokens, err := model.ListDashboardAccessTokens(user.Id)
	if err != nil {
		writeManagedSessionError(c, err)
		return
	}
	type item struct {
		model.UserAccessToken
		Status string `json:"status"`
	}
	items := make([]item, 0, len(tokens))
	now := time.Now().Unix()
	for _, token := range tokens {
		status := "active"
		switch {
		case token.RevokedAt != 0:
			status = "revoked"
		case token.AuthVersion != user.AuthVersion:
			status = "invalidated"
		case token.ExpiresAt <= now:
			status = "expired"
		}
		items = append(items, item{UserAccessToken: token, Status: status})
	}
	common.ApiSuccess(c, items)
}

func GetUserSessions(c *gin.Context) {
	user, current, ok := managedBrowserSession(c)
	if !ok {
		return
	}
	result, err := model.ListUserSessions(user.Id, current.Id)
	if err != nil {
		writeManagedSessionError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func DeleteUserSession(c *gin.Context) {
	user, current, ok := managedBrowserSession(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "AUTH_SESSION_ID_INVALID", "message": "无效的会话ID"})
		return
	}
	isCurrent, err := model.RevokeUserSessionByID(user.Id, current.Id, id)
	if err != nil {
		writeManagedSessionError(c, err)
		return
	}
	if isCurrent {
		session := sessions.Default(c)
		session.Clear()
		if err := session.Save(); err != nil {
			// The server-side revocation is already durable, even if clearing the
			// cookie fails. Report success so the client can discard local identity.
			common.SysError("clear revoked session cookie: " + err.Error())
		}
	}
	common.ApiSuccess(c, gin.H{"revoked_id": id, "current": isCurrent})
}

func RevokeOtherUserSessions(c *gin.Context) {
	user, current, ok := managedBrowserSession(c)
	if !ok {
		return
	}
	count, err := model.RevokeOtherUserSessions(user.Id, current.Id)
	if err != nil {
		writeManagedSessionError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"revoked_count": count})
}
