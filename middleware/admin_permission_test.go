package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestAdminPermissionModuleForPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "channel", path: "/api/channel/1", want: model.AdminPermissionChannel},
		{name: "models", path: "/api/models/search", want: model.AdminPermissionModels},
		{name: "deployments", path: "/api/deployments/settings", want: model.AdminPermissionModels},
		{name: "vendors", path: "/api/vendors/search", want: model.AdminPermissionModels},
		{name: "prefill groups", path: "/api/prefill_group", want: model.AdminPermissionModels},
		{name: "users", path: "/api/user/search", want: model.AdminPermissionUser},
		{name: "redemption", path: "/api/redemption/search", want: model.AdminPermissionRedemption},
		{name: "subscription", path: "/api/subscription/admin/plans", want: model.AdminPermissionSubscription},
		{name: "root only option", path: "/api/option/", want: ""},
		{name: "user token", path: "/api/token/", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adminPermissionModuleForPath(test.path); got != test.want {
				t.Fatalf("adminPermissionModuleForPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}
