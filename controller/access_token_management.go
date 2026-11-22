package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetUserAccessTokensPage replaces the historical array response at integration.
// Authentication/ownership comes exclusively from the current browser session.
func GetUserAccessTokensPage(c *gin.Context) {
	user, _, ok := managedBrowserSession(c)
	if !ok {
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	result, err := model.ListDashboardAccessTokensPage(user.Id, page, pageSize)
	if err != nil {
		writeAccessTokenManagementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func verifiedAccessTokenManager(c *gin.Context) (*model.User, bool) {
	user, _, ok := managedBrowserSession(c)
	if !ok {
		return nil, false
	}
	if !service.HasFreshSessionVerification(c, "", 5*time.Minute) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "请先完成安全验证", "code": "VERIFICATION_REQUIRED"})
		return nil, false
	}
	return user, allowAccountSecurityAction(c, user.Id, "access_token")
}

// GenerateManagedAccessToken keeps the historical string response while using
// the same owned, verified browser boundary as the explicit PAT endpoints.
func GenerateManagedAccessToken(c *gin.Context) {
	user, ok := verifiedAccessTokenManager(c)
	if !ok {
		return
	}
	scopes := []string{"self:read", "self:write", "token:read", "token:write", "task:read"}
	if user.Role >= common.RoleAdminUser {
		for _, module := range model.GetAdminPermissionModules() {
			scopes = append(scopes, module.Key+":read", module.Key+":write")
		}
	}
	if user.Role >= common.RoleRootUser {
		scopes = append(scopes, "system:read", "system:write")
	}
	secret, _, err := model.RotateDashboardAccessToken(user, scopes, time.Now().Add(30*24*time.Hour).Unix())
	if err != nil {
		writeAccessTokenManagementError(c, err)
		return
	}
	common.ApiSuccess(c, secret)
}

func CreateManagedUserAccessToken(c *gin.Context) {
	user, ok := verifiedAccessTokenManager(c)
	if !ok {
		return
	}
	var request struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt int64    `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	now := time.Now().Unix()
	if _, err := model.NormalizeAccessTokenScopes(request.Scopes, user.Role); err != nil || len(request.Name) > 100 ||
		request.ExpiresAt <= now || request.ExpiresAt > now+int64(365*24*time.Hour/time.Second) {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	secret, record, err := model.CreateDashboardAccessToken(user, request.Name, request.Scopes, request.ExpiresAt)
	if err != nil {
		writeAccessTokenManagementError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"token": secret, "record": record})
}

func RevokeManagedUserAccessToken(c *gin.Context) {
	user, ok := verifiedAccessTokenManager(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	if err := model.RevokeDashboardAccessToken(user.Id, id); err != nil {
		writeAccessTokenManagementError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func UpdateUserAccessToken(c *gin.Context) {
	user, ok := verifiedAccessTokenManager(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	var request struct {
		Name   *string  `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAccessTokenManagementError(c, model.ErrAccessTokenInvalid)
		return
	}
	record, err := model.UpdateDashboardAccessToken(user, id, request.Name, request.Scopes)
	if err != nil {
		writeAccessTokenManagementError(c, err)
		return
	}
	common.ApiSuccess(c, record)
}

func writeAccessTokenManagementError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAccessTokenInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "ACCESS_TOKEN_INVALID", "message": "个人访问令牌参数无效"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "ACCESS_TOKEN_NOT_ACTIVE", "message": "个人访问令牌不存在或已失效"})
	case errors.Is(err, model.ErrAccessTokenLimit):
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "ACCESS_TOKEN_LIMIT", "message": "已达到个人访问令牌数量上限"})
	case errors.Is(err, model.ErrAuthStateInvalid):
		writeManagedSessionError(c, err)
	default:
		common.SysError("access token management: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "ACCESS_TOKEN_ERROR", "message": "个人访问令牌操作失败"})
	}
}
