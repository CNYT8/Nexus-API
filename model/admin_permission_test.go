package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestAdminPermissionConfigFromSettingDefaultsAndOverrides(t *testing.T) {
	config := GetAdminPermissionConfigFromSetting(dto.UserSetting{
		AdminPermissions: `{"channel":false,"models":false,"unknown":false}`,
	})

	if config[AdminPermissionChannel] {
		t.Fatalf("expected channel permission to be disabled")
	}
	if config[AdminPermissionModels] {
		t.Fatalf("expected models permission to be disabled")
	}
	if !config[AdminPermissionUser] {
		t.Fatalf("expected unspecified user permission to default enabled")
	}
}

func TestValidateAdminPermissionConfigRequiresEnabledPermission(t *testing.T) {
	config := AdminPermissionConfig{
		AdminPermissionChannel:      false,
		AdminPermissionModels:       false,
		AdminPermissionUser:         false,
		AdminPermissionRedemption:   false,
		AdminPermissionSubscription: false,
		AdminPermissionTicket:       false,
	}

	if err := ValidateAdminPermissionConfig(config); err != ErrAdminPermissionEmpty {
		t.Fatalf("expected ErrAdminPermissionEmpty, got %v", err)
	}
}

func TestApplyAdminPermissionsToSidebarModules(t *testing.T) {
	sidebar := `{"chat":{"enabled":true,"chat":false},"admin":{"enabled":false,"channel":true}}`
	config := AdminPermissionConfig{
		AdminPermissionChannel:      false,
		AdminPermissionModels:       true,
		AdminPermissionUser:         true,
		AdminPermissionRedemption:   true,
		AdminPermissionSubscription: true,
		AdminPermissionTicket:       true,
	}

	applied := ApplyAdminPermissionsToSidebarModules(sidebar, config)

	var parsed map[string]map[string]bool
	if err := json.Unmarshal([]byte(applied), &parsed); err != nil {
		t.Fatalf("failed to parse sidebar modules: %v", err)
	}
	if parsed["chat"]["chat"] {
		t.Fatalf("expected existing chat preference to be preserved")
	}
	if !parsed["admin"]["enabled"] {
		t.Fatalf("expected admin section visibility to follow admin permissions")
	}
	if parsed["admin"]["channel"] {
		t.Fatalf("expected admin channel permission to be overridden")
	}
	if parsed["admin"]["setting"] {
		t.Fatalf("expected admin system settings permission to stay disabled")
	}
}

func TestAdminPermissionSidebarConfig(t *testing.T) {
	config := AdminPermissionSidebarConfig(AdminPermissionConfig{
		AdminPermissionChannel:      false,
		AdminPermissionModels:       true,
		AdminPermissionUser:         false,
		AdminPermissionRedemption:   true,
		AdminPermissionSubscription: false,
		AdminPermissionTicket:       false,
	})

	if !config["enabled"] {
		t.Fatalf("expected admin section to stay enabled when any permission is enabled")
	}
	if config["channel"] {
		t.Fatalf("expected channel permission to be disabled")
	}
	if !config["models"] {
		t.Fatalf("expected models permission to be enabled")
	}
	if !config["deployment"] {
		t.Fatalf("expected deployment permission to follow models permission")
	}
	if config["setting"] {
		t.Fatalf("expected system setting permission to stay disabled")
	}
}

func TestStripAdminSidebarModules(t *testing.T) {
	stripped := StripAdminSidebarModules(`{"chat":{"enabled":true},"admin":{"enabled":true}}`)

	var parsed map[string]map[string]bool
	if err := json.Unmarshal([]byte(stripped), &parsed); err != nil {
		t.Fatalf("failed to parse sidebar modules: %v", err)
	}
	if _, ok := parsed["admin"]; ok {
		t.Fatalf("expected admin section to be stripped")
	}
	if !parsed["chat"]["enabled"] {
		t.Fatalf("expected non-admin sections to be preserved")
	}
}
