package jsplugin

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

// PrepareNativeRoute decodes a request that arrived on a plugin-declared native
// endpoint and pins the resulting intent for the task adaptor. The model the
// route decoder resolved is published for the distributor, which then selects
// the channel bound to this revision instead of parsing the vendor body as an
// OpenAI chat body.
func PrepareNativeRoute(revisionID int64, plugin *pluginruntime.LoadedPlugin, route pluginruntime.Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		if plugin == nil {
			rejectNativeRoute(c, "script native route has no revision")
			return
		}
		prepared, err := PreparePresentation(c, revisionID, plugin, route, "")
		if err != nil {
			rejectNativeRoute(c, "script native request rejected")
			return
		}
		if prepared == nil || prepared.Intent.Model == "" {
			rejectNativeRoute(c, "script native request did not name a model")
			return
		}
		common.SetContextKey(c, constant.ContextKeyNativePluginModel, prepared.Intent.Model)
		c.Next()
	}
}

func rejectNativeRoute(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": message})
	c.Abort()
}
