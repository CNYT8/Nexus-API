package setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// This setting is intentionally separate from task_setting.usage_plugins.
// Disabling new work does not strand tasks that have already frozen a revision.
type ScriptPluginSetting struct {
	Disabled bool `json:"disabled"`
}

var scriptPluginSetting ScriptPluginSetting

func init() { config.GlobalConfig.Register("script_plugin_setting", &scriptPluginSetting) }
func ScriptPluginsDisabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return scriptPluginSetting.Disabled
}
