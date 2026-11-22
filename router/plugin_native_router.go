package router

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	taskjsplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// SetPluginNativeRouter mounts the native endpoints declared by activated
// script plugin revisions. It is additive: a deployment without activated
// plugins gets no extra route, a route that would shadow a host route is
// skipped, and failures are logged instead of aborting startup.
func SetPluginNativeRouter(engine *gin.Engine) {
	if engine == nil {
		return
	}
	activations, err := model.ListScriptPluginActivations()
	if err != nil {
		common.SysLog("plugin native router: activations unavailable: " + err.Error())
		return
	}
	if len(activations) == 0 {
		return
	}
	occupied := make(map[string]struct{})
	for _, info := range engine.Routes() {
		occupied[info.Method+" "+info.Path] = struct{}{}
	}
	mounted := make(map[string]struct{})
	for _, activation := range activations {
		plugin, loadErr := service.LoadScriptPlugin(context.Background(), activation.PluginID, false)
		if loadErr != nil {
			common.SysLog("plugin native router: revision unavailable: " + loadErr.Error())
			continue
		}
		for _, route := range plugin.Meta.Routes {
			shape := route.Method + " " + route.Path
			if route.Type == "query" || strings.Contains(route.Path, "*") {
				common.SysLog("plugin native router: route is not mounted by this path: " + shape)
				continue
			}
			if _, clash := occupied[shape]; clash {
				common.SysLog("plugin native router: route shadows a host route: " + shape)
				continue
			}
			if _, duplicate := mounted[shape]; duplicate {
				common.SysLog("plugin native router: duplicate route shape: " + shape)
				continue
			}
			mounted[shape] = struct{}{}
			engine.Handle(route.Method, route.Path,
				middleware.SystemPerformanceCheck(),
				middleware.TokenAuth(),
				middleware.ModelRequestRateLimit(),
				taskjsplugin.PrepareNativeRoute(activation.PluginID, plugin, route),
				middleware.Distribute(),
				controller.RelayTask,
			)
		}
	}
}
