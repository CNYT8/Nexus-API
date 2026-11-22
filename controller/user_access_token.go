package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func accessTokenSessionUser(c *gin.Context) (*model.User, bool) {
	user, _, err := service.CurrentSessionUser(c)
	if err != nil || !service.HasFreshSessionVerification(c, "", 5*time.Minute) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "请先完成安全验证", "code": "VERIFICATION_REQUIRED"})
		return nil, false
	}
	if !allowAccountSecurityAction(c, user.Id, "access_token") {
		return nil, false
	}
	return user, true
}

func generateCompatibleAccessToken(c *gin.Context) {
	user, ok := accessTokenSessionUser(c)
	if !ok {
		return
	}
	scopes := []string{"self:read", "self:write", "token:read", "token:write", "task:read"}
	if user.Role >= common.RoleAdminUser {
		for _, module := range []string{"channel", "models", "user", "redemption", "subscription", "ticket"} {
			scopes = append(scopes, module+":read", module+":write")
		}
	}
	if user.Role >= common.RoleRootUser {
		scopes = append(scopes, "system:read", "system:write")
	}
	secret, _, err := model.RotateDashboardAccessToken(user, scopes, time.Now().Add(30*24*time.Hour).Unix())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": secret})
}

func CreateUserAccessToken(c *gin.Context) {
	user, ok := accessTokenSessionUser(c)
	if !ok {
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt int64    `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	secret, record, err := model.CreateDashboardAccessToken(user, req.Name, req.Scopes, req.ExpiresAt)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"token": secret, "record": record})
}

func ListUserAccessTokens(c *gin.Context) {
	user, _, err := service.CurrentSessionUser(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tokens, err := model.ListDashboardAccessTokens(user.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, tokens)
}

func RevokeUserAccessToken(c *gin.Context) {
	user, ok := accessTokenSessionUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的令牌ID")
		return
	}
	if err := model.RevokeDashboardAccessToken(user.Id, id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
