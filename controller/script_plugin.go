package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func requireScriptPluginRoot(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return false
	}
	return true
}
func CreateScriptPlugin(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	var request struct {
		Source  string `json:"source"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeTaskPluginRequest(c, 2<<20, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	plugin, err := service.InstallScriptPlugin(c.Request.Context(), request.Source, request.Enabled)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	plugin.Source = ""
	common.ApiSuccess(c, plugin)
}
func ListScriptPlugins(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil || before < 0 {
		common.ApiErrorMsg(c, "invalid before_id")
		return
	}
	plugins, err := model.ListScriptPlugins(before, 100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, plugins)
}
func GetScriptPlugin(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid script plugin id")
		return
	}
	plugin, err := model.GetScriptPlugin(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, plugin)
}
func SetScriptPluginEnabled(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid script plugin id")
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err = decodeTaskPluginRequest(c, 1024, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Enabled == nil {
		common.ApiErrorMsg(c, "enabled is required")
		return
	}
	if err = model.SetScriptPluginEnabled(id, *request.Enabled); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func ListScriptPluginBindings(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	channelID, err := strconv.Atoi(c.DefaultQuery("channel_id", "0"))
	if err != nil || channelID < 0 {
		common.ApiErrorMsg(c, "invalid channel_id")
		return
	}
	bindings, err := model.ListScriptPluginBindings(channelID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, bindings)
}
func PutScriptPluginBinding(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	var binding model.ScriptPluginBinding
	if err := decodeTaskPluginRequest(c, 4096, &binding); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.SaveScriptPluginBinding(c.Request.Context(), &binding); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, binding)
}
