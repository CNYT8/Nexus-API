package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type adminPermissionUserResponse struct {
	Id          int                         `json:"id"`
	Username    string                      `json:"username"`
	DisplayName string                      `json:"display_name"`
	Email       string                      `json:"email"`
	Role        int                         `json:"role"`
	Permissions model.AdminPermissionConfig `json:"permissions"`
}

type adminPermissionListResponse struct {
	Modules []model.AdminPermissionModule `json:"modules"`
	Admins  []adminPermissionUserResponse `json:"admins"`
}

type updateAdminPermissionRequest struct {
	Permissions model.AdminPermissionConfig `json:"permissions"`
}

func ListAdminPermissions(c *gin.Context) {
	users, err := model.ListAdminUsersForPermission()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	admins := make([]adminPermissionUserResponse, 0, len(users))
	for _, user := range users {
		admins = append(admins, buildAdminPermissionUserResponse(user))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": adminPermissionListResponse{
			Modules: model.GetAdminPermissionModules(),
			Admins:  admins,
		},
	})
}

func UpdateAdminPermissions(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	var request updateAdminPermissionRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.Role != common.RoleAdminUser {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	if err := model.SetAdminPermissionConfig(user, request.Permissions); errors.Is(err, model.ErrAdminPermissionEmpty) {
		common.ApiErrorI18n(c, i18n.MsgAdminPermissionEmpty)
		return
	} else if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildAdminPermissionUserResponse(user),
	})
}

func buildAdminPermissionUserResponse(user *model.User) adminPermissionUserResponse {
	return adminPermissionUserResponse{
		Id:          user.Id,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        user.Role,
		Permissions: model.GetAdminPermissionConfig(user),
	}
}
