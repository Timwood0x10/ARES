package main

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/agents"
)

// TestResolveRoleProfileKnownBuiltinID verifies the happy path: a configured
// role id that matches a built-in profile resolves to that profile (W4 write
// side — both createExecutor and newPeerChatCognition share this resolution).
func TestResolveRoleProfileKnownBuiltinID(t *testing.T) {
	profile := resolveRoleProfile(agents.RolePlanner)
	if profile == nil {
		t.Fatal("built-in planner role must resolve to a profile")
	}
	if profile.ID != agents.RolePlanner {
		t.Fatalf("resolved profile id = %q, want %q", profile.ID, agents.RolePlanner)
	}
	if profile.Instructions == "" {
		t.Fatal("resolved profile must carry role instructions")
	}
}

// TestResolveRoleProfileEmptyRoleIsNil pins the roleless default: an empty
// configured role resolves to nil (no profile pinning), without touching the
// default profile set or logging a warning.
func TestResolveRoleProfileEmptyRoleIsNil(t *testing.T) {
	if profile := resolveRoleProfile(""); profile != nil {
		t.Fatalf("empty role must resolve to nil, got %+v", profile)
	}
}

// TestResolveRoleProfileUnknownIDIsNil verifies the degrade contract: an
// unknown role id (config typo) resolves to nil so the peer runs roleless
// instead of failing startup — matching the old registry.Get behavior the
// helper replaced.
func TestResolveRoleProfileUnknownIDIsNil(t *testing.T) {
	if profile := resolveRoleProfile("does-not-exist"); profile != nil {
		t.Fatalf("unknown role must resolve to nil, got %+v", profile)
	}
}
