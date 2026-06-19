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

func TestApplyAdminPermissionsToSidebarModules(t *testing.T) {
	sidebar := `{"chat":{"enabled":true,"chat":false},"admin":{"enabled":true,"channel":true}}`
	config := AdminPermissionConfig{
		AdminPermissionChannel:      false,
		AdminPermissionModels:       true,
		AdminPermissionUser:         true,
		AdminPermissionRedemption:   true,
		AdminPermissionSubscription: true,
	}

	applied := ApplyAdminPermissionsToSidebarModules(sidebar, config)

	var parsed map[string]map[string]bool
	if err := json.Unmarshal([]byte(applied), &parsed); err != nil {
		t.Fatalf("failed to parse sidebar modules: %v", err)
	}
	if parsed["chat"]["chat"] {
		t.Fatalf("expected existing chat preference to be preserved")
	}
	if parsed["admin"]["channel"] {
		t.Fatalf("expected admin channel permission to be overridden")
	}
	if parsed["admin"]["setting"] {
		t.Fatalf("expected admin system settings permission to stay disabled")
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
