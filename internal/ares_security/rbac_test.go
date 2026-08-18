package ares_security

import "testing"

func TestParseRole(t *testing.T) {
	for _, s := range []string{"admin", "operator", "agent"} {
		if _, err := ParseRole(s); err != nil {
			t.Fatalf("ParseRole(%q): %v", s, err)
		}
	}
	// Case-insensitive.
	if r, err := ParseRole("ADMIN"); err != nil || r != RoleAdmin {
		t.Fatalf("ParseRole(ADMIN) = %q, %v", r, err)
	}
	// Unknown roles rejected (default deny).
	for _, s := range []string{"", "root", "superuser", "Administrator "} {
		if _, err := ParseRole(s); err == nil {
			t.Fatalf("ParseRole(%q) must error", s)
		}
	}
}

func TestRolePermissionMatrix(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleAdmin, PermRead, true},
		{RoleAdmin, PermWrite, true},
		{RoleAdmin, PermAdmin, true},
		{RoleOperator, PermRead, true},
		{RoleOperator, PermWrite, true},
		{RoleOperator, PermAdmin, false}, // operator cannot run destructive chaos
		{RoleAgent, PermRead, true},
		{RoleAgent, PermWrite, false},
		{RoleAgent, PermAdmin, false},
		{"", PermRead, false}, // empty role → deny
		{Role("hacker"), PermRead, false},
	}
	for _, tc := range cases {
		if got := AllowRole(tc.role, tc.perm); got != tc.want {
			t.Errorf("AllowRole(%q, %q) = %v, want %v", tc.role, tc.perm, got, tc.want)
		}
	}
}

func TestHasPermissionMatchesAllowRole(t *testing.T) {
	if !HasPermission(RoleOperator, PermWrite) {
		t.Fatal("operator should have write")
	}
	if HasPermission(RoleAgent, PermWrite) {
		t.Fatal("agent must not have write")
	}
}
