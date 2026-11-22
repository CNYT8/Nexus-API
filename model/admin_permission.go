package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	AdminPermissionChannel      = "channel"
	AdminPermissionModels       = "models"
	AdminPermissionUser         = "user"
	AdminPermissionRedemption   = "redemption"
	AdminPermissionSubscription = "subscription"
	AdminPermissionTicket       = "ticket"
)

type AdminPermissionModule struct {
	Key         string `json:"key"`
	Enabled     bool   `json:"enabled"`
	TitleKey    string `json:"title_key"`
	Description string `json:"description"`
}

type AdminPermissionConfig map[string]bool

var ErrAdminPermissionEmpty = errors.New("at least one admin permission must remain enabled")

var adminPermissionModules = []AdminPermissionModule{
	{
		Key:         AdminPermissionChannel,
		Enabled:     true,
		TitleKey:    "Channels",
		Description: "Configure upstream providers and routing.",
	},
	{
		Key:         AdminPermissionModels,
		Enabled:     true,
		TitleKey:    "Models",
		Description: "Manage catalog visibility, pricing, groups, vendors, and deployments.",
	},
	{
		Key:         AdminPermissionUser,
		Enabled:     true,
		TitleKey:    "Users",
		Description: "Administer user accounts and roles.",
	},
	{
		Key:         AdminPermissionRedemption,
		Enabled:     true,
		TitleKey:    "Redeem codes",
		Description: "Create and review invite or credit codes.",
	},
	{
		Key:         AdminPermissionSubscription,
		Enabled:     true,
		TitleKey:    "Subscription Management",
		Description: "Manage subscription plans and user subscriptions.",
	},
	{
		Key:         AdminPermissionTicket,
		Enabled:     true,
		TitleKey:    "Ticket Management",
		Description: "Review and respond to user support tickets.",
	},
}

func GetAdminPermissionModules() []AdminPermissionModule {
	modules := make([]AdminPermissionModule, len(adminPermissionModules))
	copy(modules, adminPermissionModules)
	return modules
}

func DefaultAdminPermissionConfig() AdminPermissionConfig {
	config := AdminPermissionConfig{}
	for _, module := range adminPermissionModules {
		config[module.Key] = module.Enabled
	}
	return config
}

func NormalizeAdminPermissionConfig(config AdminPermissionConfig) AdminPermissionConfig {
	normalized := DefaultAdminPermissionConfig()
	for _, module := range adminPermissionModules {
		if value, ok := config[module.Key]; ok {
			normalized[module.Key] = value
		}
		// Missing actions inherit the historical module switch. Explicit actions
		// never override a disabled module, and write does not imply read.
		for _, action := range []string{"read", "write"} {
			key := module.Key + ":" + action
			if value, ok := config[key]; ok {
				normalized[key] = value
			}
		}
	}
	return normalized
}

func CountEnabledAdminPermissions(config AdminPermissionConfig) int {
	enabled := 0
	normalized := NormalizeAdminPermissionConfig(config)
	for _, module := range adminPermissionModules {
		if normalized.Allows(module.Key, "read") || normalized.Allows(module.Key, "write") {
			enabled++
		}
	}
	return enabled
}

// Allows preserves legacy module booleans while enforcing explicit action grants.
func (config AdminPermissionConfig) Allows(module, action string) bool {
	if action != "read" && action != "write" {
		return false
	}
	for _, known := range adminPermissionModules {
		if known.Key != module {
			continue
		}
		enabled, exists := config[module]
		if !exists {
			enabled = known.Enabled
		}
		if !enabled {
			return false
		}
		if allowed, exists := config[module+":"+action]; exists {
			return allowed
		}
		return enabled
	}
	return false
}

func IsAdminPermissionActionAllowed(userId int, module, action string) (bool, error) {
	config, err := GetAdminPermissionConfigByUserId(userId)
	if err != nil {
		return false, err
	}
	return config.Allows(module, action), nil
}

func ValidateAdminPermissionConfig(config AdminPermissionConfig) error {
	if CountEnabledAdminPermissions(config) == 0 {
		return ErrAdminPermissionEmpty
	}
	return nil
}

func GetAdminPermissionConfigFromSetting(setting dto.UserSetting) AdminPermissionConfig {
	return NormalizeAdminPermissionConfig(parseAdminPermissionConfig(setting.AdminPermissions))
}

func GetAdminPermissionConfig(user *User) AdminPermissionConfig {
	if user == nil {
		return DefaultAdminPermissionConfig()
	}
	return GetAdminPermissionConfigFromSetting(user.GetSetting())
}

func GetAdminPermissionConfigByUserId(userId int) (AdminPermissionConfig, error) {
	setting, err := GetUserSetting(userId, false)
	if err != nil {
		return nil, err
	}
	return GetAdminPermissionConfigFromSetting(setting), nil
}

func SetAdminPermissionConfig(user *User, config AdminPermissionConfig) error {
	if user == nil {
		return nil
	}
	if err := ValidateAdminPermissionConfig(config); err != nil {
		return err
	}

	if err := PatchUserSetting(user.Id, map[string]any{
		"admin_permissions": serializeAdminPermissionConfig(config),
	}); err != nil {
		return err
	}
	return DB.First(user, user.Id).Error
}

func adminPermissionsChanged(before, after string) bool {
	oldUser, newUser := User{Setting: before}, User{Setting: after}
	oldConfig, newConfig := GetAdminPermissionConfig(&oldUser), GetAdminPermissionConfig(&newUser)
	for _, module := range adminPermissionModules {
		if oldConfig[module.Key] != newConfig[module.Key] ||
			oldConfig.Allows(module.Key, "read") != newConfig.Allows(module.Key, "read") ||
			oldConfig.Allows(module.Key, "write") != newConfig.Allows(module.Key, "write") {
			return true
		}
	}
	return false
}

func IsAdminPermissionAllowed(userId int, module string) (bool, error) {
	module = strings.TrimSpace(module)
	if module == "" {
		return true, nil
	}

	config, err := GetAdminPermissionConfigByUserId(userId)
	if err != nil {
		return false, err
	}
	return config[module] != false, nil
}

func parseAdminPermissionConfig(value string) AdminPermissionConfig {
	config := DefaultAdminPermissionConfig()
	if strings.TrimSpace(value) == "" {
		return config
	}

	var parsed map[string]bool
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return config
	}

	return NormalizeAdminPermissionConfig(parsed)
}

func serializeAdminPermissionConfig(config AdminPermissionConfig) string {
	normalized := NormalizeAdminPermissionConfig(config)
	bytes, err := json.Marshal(normalized)
	if err != nil {
		common.SysLog("failed to marshal admin permission config: " + err.Error())
		return ""
	}
	return string(bytes)
}

func ApplyAdminPermissionsToSidebarModules(value string, config AdminPermissionConfig) string {
	modules := map[string]map[string]bool{}
	if strings.TrimSpace(value) != "" {
		_ = json.Unmarshal([]byte(value), &modules)
	}

	modules["admin"] = AdminPermissionSidebarConfig(config)

	bytes, err := json.Marshal(modules)
	if err != nil {
		common.SysLog("failed to marshal sidebar modules with admin permissions: " + err.Error())
		return value
	}
	return string(bytes)
}

func AdminPermissionSidebarConfig(config AdminPermissionConfig) map[string]bool {
	normalized := NormalizeAdminPermissionConfig(config)
	adminConfig := map[string]bool{
		"enabled": CountEnabledAdminPermissions(normalized) > 0,
		"setting": false,
	}
	for _, module := range adminPermissionModules {
		adminConfig[module.Key] = normalized.Allows(module.Key, "read") || normalized.Allows(module.Key, "write")
	}
	adminConfig["deployment"] = adminConfig[AdminPermissionModels]
	adminConfig["ticket_admin"] = adminConfig[AdminPermissionTicket]
	return adminConfig
}

func StripAdminSidebarModules(value string) string {
	modules := map[string]map[string]bool{}
	if strings.TrimSpace(value) == "" {
		return value
	}
	if err := json.Unmarshal([]byte(value), &modules); err != nil {
		return value
	}
	delete(modules, "admin")

	bytes, err := json.Marshal(modules)
	if err != nil {
		common.SysLog("failed to marshal sidebar modules without admin section: " + err.Error())
		return value
	}
	return string(bytes)
}

func ListAdminUsersForPermission() ([]*User, error) {
	var users []*User
	err := DB.Omit("password").Where("role = ?", common.RoleAdminUser).Order("id asc").Find(&users).Error
	return users, err
}
