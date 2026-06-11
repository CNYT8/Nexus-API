package register_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type RegisterSetting struct {
	DefaultGroup string `json:"default_group"`
}

var registerSetting = RegisterSetting{
	DefaultGroup: "",
}

func init() {
	config.GlobalConfig.Register("register_setting", &registerSetting)
}

// GetDefaultGroup returns the configured group for newly registered users.
// Returns "" when unset or when the group no longer exists, so callers can
// fall back to the database column default.
func GetDefaultGroup() string {
	group := registerSetting.DefaultGroup
	if group == "" || group == "default" {
		return ""
	}
	if _, ok := ratio_setting.GetGroupRatioCopy()[group]; !ok {
		return ""
	}
	return group
}
