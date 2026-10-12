package knowledge_test

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/tenantctx"
)

// TestUserNamespace_CollisionSafe pins that a tenant or user ID containing
// the separator character does not produce a key that collides with a
// different pair.
func TestUserNamespace_CollisionSafe(t *testing.T) {
	t.Parallel()

	// These two pairs would collide with naive "/" joining:
	//   tenant="a" user="b/c" → "a/b/c"
	//   tenant="a/b" user="c" → "a/b/c"
	ns1 := knowledge.UserNamespace("a", "b/c")
	ns2 := knowledge.UserNamespace("a/b", "c")
	if ns1 == ns2 {
		t.Fatalf("collision: namespace(%q, %q) == namespace(%q, %q) == %q",
			"a", "b/c", "a/b", "c", ns1)
	}
}

// TestUserNamespace_Deterministic pins that the same tenant+user pair always
// produces the same namespace.
func TestUserNamespace_Deterministic(t *testing.T) {
	t.Parallel()

	ns1 := knowledge.UserNamespace("tenant-1", "user-1")
	ns2 := knowledge.UserNamespace("tenant-1", "user-1")
	if ns1 != ns2 {
		t.Errorf("same pair should produce same namespace: %q vs %q", ns1, ns2)
	}
}

// TestUserNamespace_DifferentUsersDifferentNamespaces pins that different
// users in the same tenant get different namespaces.
func TestUserNamespace_DifferentUsersDifferentNamespaces(t *testing.T) {
	t.Parallel()

	ns1 := knowledge.UserNamespace("tenant-1", "user-1")
	ns2 := knowledge.UserNamespace("tenant-1", "user-2")
	if ns1 == ns2 {
		t.Error("different users in the same tenant should have different namespaces")
	}
}

// TestUserNamespace_EmptyUserReturnsEmpty pins that an empty userID returns
// an empty namespace so callers fall back to tenant namespace.
func TestUserNamespace_EmptyUserReturnsEmpty(t *testing.T) {
	t.Parallel()

	ns := knowledge.UserNamespace("tenant-1", "")
	if ns != "" {
		t.Errorf("empty user should return empty namespace, got %q", ns)
	}
}

// TestUserNamespace_UserOnly pins that a user with no tenant gets a
// user-only namespace.
func TestUserNamespace_UserOnly(t *testing.T) {
	t.Parallel()

	ns := knowledge.UserNamespace("", "user-1")
	if ns == "" {
		t.Error("user with no tenant should get a non-empty namespace")
	}
	// Should have the "u:" prefix to distinguish from combined keys.
	if len(ns) < 2 || ns[:2] != "u:" {
		t.Errorf("user-only namespace should have 'u:' prefix, got %q", ns)
	}
}

// TestResolveUserNamespace_Precedence pins the precedence: user > tenant > fallback.
func TestResolveUserNamespace_Precedence(t *testing.T) {
	t.Parallel()

	// User + tenant → user namespace.
	ns := knowledge.ResolveUserNamespace("t1", "u1", "default")
	if ns != knowledge.UserNamespace("t1", "u1") {
		t.Errorf("user+tenant should resolve to user namespace, got %q", ns)
	}

	// No user, tenant present → tenant.
	ns = knowledge.ResolveUserNamespace("t1", "", "default")
	if ns != "t1" {
		t.Errorf("no user should fall back to tenant, got %q", ns)
	}

	// No user, no tenant → fallback.
	ns = knowledge.ResolveUserNamespace("", "", "default")
	if ns != "default" {
		t.Errorf("no user no tenant should fall back to default, got %q", ns)
	}
}

// TestUserContext_RoundTrip pins that WithUserContext and UserFromContext
// work together to carry the user ID through a context.
func TestUserContext_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if uid := knowledge.UserFromContext(ctx); uid != "" {
		t.Errorf("empty context should return empty user, got %q", uid)
	}

	ctx = knowledge.WithUserContext(ctx, "user-1")
	if uid := knowledge.UserFromContext(ctx); uid != "user-1" {
		t.Errorf("UserFromContext = %q, want %q", uid, "user-1")
	}

	// Verify tenant context is NOT affected.
	ctx = tenantctx.With(ctx, "tenant-1")
	if uid := knowledge.UserFromContext(ctx); uid != "user-1" {
		t.Errorf("UserFromContext after tenant.With = %q, want %q", uid, "user-1")
	}
	if tid := tenantctx.From(ctx); tid != "tenant-1" {
		t.Errorf("tenant.From after user context = %q, want %q", tid, "tenant-1")
	}
}
