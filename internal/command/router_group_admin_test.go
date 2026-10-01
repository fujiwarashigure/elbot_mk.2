package command

import (
	"testing"

	"elbot/internal/security"
)

func TestRouterAllowsGroupAdminsWhenConfigured(t *testing.T) {
	r := NewRouter([]string{"/"})
	if err := r.Register(NewFunc(Info{Name: "overflow", AllowGroupAdmin: true}, nil)); err != nil {
		t.Fatalf("Register overflow: %v", err)
	}

	user := security.Actor{Role: security.RoleUser, GroupRole: security.GroupRoleMember}
	if _, ok := r.CommandInfoForActor("overflow", user); ok {
		t.Fatal("regular member should not access group-admin command")
	}
	groupAdmin := security.Actor{Role: security.RoleUser, GroupRole: security.GroupRoleAdmin}
	if _, ok := r.CommandInfoForActor("overflow", groupAdmin); !ok {
		t.Fatal("group admin should access group-admin command")
	}
	groupOwner := security.Actor{Role: security.RoleUser, GroupRole: security.GroupRoleOwner}
	if _, ok := r.CommandInfoForActor("overflow", groupOwner); !ok {
		t.Fatal("group owner should access group-admin command")
	}
	superadmin := security.Actor{Role: security.RoleSuperadmin, GroupRole: security.GroupRoleMember}
	if _, ok := r.CommandInfoForActor("overflow", superadmin); !ok {
		t.Fatal("superadmin should access group-admin command")
	}
}
