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
	}
	return normalized
}

func CountEnabledAdminPermissions(config AdminPermissionConfig) int {
	enabled := 0
	normalized := NormalizeAdminPermissionConfig(config)
	for _, module := range adminPermissionModules {
		if normalized[module.Key] {
			enabled++
		}
	}
	return enabled
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

	setting := user.GetSetting()
	setting.AdminPermissions = serializeAdminPermissionConfig(config)
	user.SetSetting(setting)
	return user.Update(false)
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

	for _, module := range adminPermissionModules {
		if value, ok := parsed[module.Key]; ok {
			config[module.Key] = value
		}
	}
	return config
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
		adminConfig[module.Key] = normalized[module.Key]
	}
	adminConfig["deployment"] = normalized[AdminPermissionModels]
	adminConfig["ticket_admin"] = normalized[AdminPermissionTicket]
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
