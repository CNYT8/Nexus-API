package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func CreateTaskPlugin(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	var plugin model.TaskPlugin
	if err := decodeTaskPluginRequest(c, 32*1024, &plugin); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.ValidateTaskPlugin(&plugin); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateTaskPlugin(&plugin); err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, plugin)
}

func ListTaskPlugins(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil || before < 0 {
		common.ApiErrorMsg(c, "invalid before_id")
		return
	}
	plugins, err := model.ListTaskPlugins(before, 100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, plugins)
}
