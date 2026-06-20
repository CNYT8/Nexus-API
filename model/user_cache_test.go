package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestUserToBaseUserIncludesRole(t *testing.T) {
	user := &User{
		Id:       12,
		Username: "admin",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}

	base := user.ToBaseUser()
	if base.Role != common.RoleAdminUser {
		t.Fatalf("expected cached user role %d, got %d", common.RoleAdminUser, base.Role)
	}
}
