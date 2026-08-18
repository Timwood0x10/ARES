package ares_security

import (
	"errors"
	"fmt"
	"strings"
)

// Role is the identity class carried in a signed token. Roles form a
// hierarchy: admin ⊃ operator ⊃ agent (each grants the lower role's
// permissions plus its own).
type Role string

// Permission is a capability a role may hold. The middleware maps each
// protected route to the minimum permission required; a request without that
// permission is rejected (403) before the handler runs.
type Permission string

const (
	// RoleAdmin is the platform operator: full control, including destructive
	// actions (kill agents, run chaos scenarios, purge data).
	RoleAdmin Role = "admin"
	// RoleOperator can start/stop agents and view runtime state, but cannot
	// run destructive chaos/arena scenarios.
	RoleOperator Role = "operator"
	// RoleAgent is the narrowest role: read-only access to state an agent
	// needs (its own tasks, results, health).
	RoleAgent Role = "agent"

	// PermRead allows viewing state (list agents, read health, fetch spans).
	PermRead Permission = "read"
	// PermWrite allows mutating runtime state (create/kill agents, submit
	// feedback, invoke tools). Destructive chaos actions require the admin
	// role directly (see AllowRole), not merely write.
	PermWrite Permission = "write"
	// PermAdmin allows destructive chaos/arena operations (kill leader,
	// corrupt memory, partition networks). Only RoleAdmin holds it.
	PermAdmin Permission = "admin"
)

// ErrUnknownRole is returned by ParseRole for a role string that is not one
// of the three supported roles. Role strings come from tokens — never trust
// them silently.
var ErrUnknownRole = errors.New("unknown role")

// rolePermissions is the static role→permission matrix. A role implicitly
// holds the permissions of every role below it in the hierarchy.
var rolePermissions = map[Role]map[Permission]bool{
	RoleAdmin:    {PermRead: true, PermWrite: true, PermAdmin: true},
	RoleOperator: {PermRead: true, PermWrite: true},
	RoleAgent:    {PermRead: true},
}

// ParseRole converts a token role string into a Role. Unknown or empty values
// are rejected so an attacker cannot mint a role the system does not know.
func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	switch r {
	case RoleAdmin, RoleOperator, RoleAgent:
		return r, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownRole, s)
	}
}

// AllowRole reports whether the role may perform an action that requires the
// given permission. Empty role is denied (default deny).
func AllowRole(role Role, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	return perms[perm]
}

// HasPermission is an alias of AllowRole for callers that phrase the check as
// "principal has permission". It keeps call sites readable at route mounting.
func HasPermission(role Role, perm Permission) bool {
	return AllowRole(role, perm)
}
