package controller

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type updateUserGroupRatioRequest struct {
	Group string  `json:"group"`
	Ratio float64 `json:"ratio"`
}

func getManageableUserForGroupRatio(c *gin.Context) (*model.User, bool) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return nil, false
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if !canManageTargetRole(c.GetInt("role"), user.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return nil, false
	}
	return user, true
}

func respondUserGroupRatios(c *gin.Context, user *model.User) {
	details, err := service.GetUserGroupRatioDetails(user.Id, user.Group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    details,
	})
}

func GetUserGroupRatios(c *gin.Context) {
	user, ok := getManageableUserForGroupRatio(c)
	if !ok {
		return
	}
	respondUserGroupRatios(c, user)
}

func UpdateUserGroupRatio(c *gin.Context) {
	user, ok := getManageableUserForGroupRatio(c)
	if !ok {
		return
	}
	request := updateUserGroupRatioRequest{}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	request.Group = strings.TrimSpace(request.Group)
	if !ratio_setting.ContainsGroupRatio(request.Group) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := model.SetUserGroupRatioOverride(user.Id, request.Group, request.Ratio); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	recordManageAuditFor(c, user.Id, "user.group_ratio.update", map[string]interface{}{
		"group": request.Group,
		"ratio": request.Ratio,
	})
	respondUserGroupRatios(c, user)
}

func DeleteUserGroupRatio(c *gin.Context) {
	user, ok := getManageableUserForGroupRatio(c)
	if !ok {
		return
	}
	group := strings.TrimSpace(c.Query("group"))
	if !ratio_setting.ContainsGroupRatio(group) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := model.DeleteUserGroupRatioOverride(user.Id, group); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "user.group_ratio.delete", map[string]interface{}{
		"group": group,
	})
	respondUserGroupRatios(c, user)
}
