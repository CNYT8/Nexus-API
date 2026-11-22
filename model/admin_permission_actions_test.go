package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminPermissionActionsLegacyAndExplicitGrants(t *testing.T) {
	for _, module := range GetAdminPermissionModules() {
		t.Run(module.Key, func(t *testing.T) {
			legacy := NormalizeAdminPermissionConfig(AdminPermissionConfig{module.Key: true})
			require.True(t, legacy.Allows(module.Key, "read"))
			require.True(t, legacy.Allows(module.Key, "write"))
			legacy[module.Key+":write"] = false
			require.True(t, legacy.Allows(module.Key, "read"))
			require.False(t, legacy.Allows(module.Key, "write"))
			encoded := serializeAdminPermissionConfig(legacy)
			require.Equal(t, legacy, parseAdminPermissionConfig(encoded))
			legacy[module.Key+":read"] = false
			legacy[module.Key+":write"] = true
			require.False(t, legacy.Allows(module.Key, "read"), "write is independent from read")
			require.True(t, legacy.Allows(module.Key, "write"))
			legacy[module.Key] = false
			require.False(t, legacy.Allows(module.Key, "write"), "the legacy module switch is an absolute veto")
			require.False(t, legacy.Allows(module.Key, "reveal"))
		})
	}
	require.False(t, DefaultAdminPermissionConfig().Allows("unknown", "read"))
	config := NormalizeAdminPermissionConfig(AdminPermissionConfig{"unknown:read": true, "channel:reveal": true})
	require.NotContains(t, config, "unknown:read")
	require.NotContains(t, config, "channel:reveal")
}

func TestAdminPermissionActionsDisabledSidebarAndValidation(t *testing.T) {
	config := DefaultAdminPermissionConfig()
	for _, module := range GetAdminPermissionModules() {
		config[module.Key+":read"] = false
		config[module.Key+":write"] = false
	}
	require.Zero(t, CountEnabledAdminPermissions(config))
	require.ErrorIs(t, ValidateAdminPermissionConfig(config), ErrAdminPermissionEmpty)
	sidebar := AdminPermissionSidebarConfig(config)
	require.False(t, sidebar["enabled"])
	require.False(t, sidebar["deployment"])
	require.False(t, sidebar["ticket_admin"])
	config["ticket:read"] = true
	require.NoError(t, ValidateAdminPermissionConfig(config))
	require.True(t, AdminPermissionSidebarConfig(config)["ticket_admin"])
}
