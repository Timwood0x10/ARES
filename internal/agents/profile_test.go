package agents

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ProfileRegistry basic operations ────────

func TestProfileRegistry_RegisterGet(t *testing.T) {
	registry := NewProfileRegistry()
	profile := &AgentProfile{ID: "coder", Role: RoleCoder, Instructions: "write code"}

	registry.Register(profile)
	assert.Same(t, profile, registry.Get("coder"))
	assert.Nil(t, registry.Get("missing"))
}

func TestProfileRegistry_RegisterOverwrites(t *testing.T) {
	registry := NewProfileRegistry()
	registry.Register(&AgentProfile{ID: "coder", Role: RoleCoder, Instructions: "v1"})
	registry.Register(&AgentProfile{ID: "coder", Role: RoleCoder, Instructions: "v2"})

	got := registry.Get("coder")
	require.NotNil(t, got)
	assert.Equal(t, "v2", got.Instructions)
}

func TestProfileRegistry_List(t *testing.T) {
	registry := NewProfileRegistry()
	registry.Register(&AgentProfile{ID: "coder", Role: RoleCoder})
	registry.Register(&AgentProfile{ID: "reviewer", Role: RoleReviewer})

	profiles := registry.List()
	assert.Len(t, profiles, 2)

	ids := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		ids[p.ID] = true
	}
	assert.True(t, ids["coder"])
	assert.True(t, ids["reviewer"])
}

func TestProfileRegistry_ListEmpty(t *testing.T) {
	registry := NewProfileRegistry()
	assert.Empty(t, registry.List())
}

func TestProfileRegistry_Has(t *testing.T) {
	registry := NewProfileRegistry()
	registry.Register(&AgentProfile{ID: "coder", Role: RoleCoder})

	assert.True(t, registry.Has("coder"))
	assert.False(t, registry.Has("planner"))
	assert.False(t, registry.Has(""))
}

// ── ApplyToContext / GetFromContext ─────────

func TestApplyToContext_Success(t *testing.T) {
	registry := NewProfileRegistry()
	registry.Register(&AgentProfile{ID: RoleCoder, Role: RoleCoder, Instructions: "write code"})

	ctx := context.Background()
	newCtx, profile, err := registry.ApplyToContext(ctx, RoleCoder)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, RoleCoder, profile.Role)

	// The active profile must be retrievable from the derived context.
	active := GetFromContext(newCtx)
	require.NotNil(t, active)
	assert.Same(t, profile, active)
}

func TestApplyToContext_ProfileNotFound(t *testing.T) {
	registry := NewProfileRegistry()

	ctx := context.Background()
	newCtx, profile, err := registry.ApplyToContext(ctx, "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProfileNotFound)
	assert.Nil(t, profile)
	// The original context must be returned untouched.
	assert.Equal(t, ctx, newCtx)
}

func TestApplyToContext_SwitchesProfile(t *testing.T) {
	registry := NewProfileRegistry()
	registry.Register(&AgentProfile{ID: RoleResearcher, Role: RoleResearcher})
	registry.Register(&AgentProfile{ID: RoleCoder, Role: RoleCoder})

	ctx, _, err := registry.ApplyToContext(context.Background(), RoleResearcher)
	require.NoError(t, err)
	ctx, _, err = registry.ApplyToContext(ctx, RoleCoder)
	require.NoError(t, err)

	active := GetFromContext(ctx)
	require.NotNil(t, active)
	assert.Equal(t, RoleCoder, active.Role)
}

func TestGetFromContext_NoProfile(t *testing.T) {
	assert.Nil(t, GetFromContext(context.Background()))
}

// ── DefaultProfiles ─────────────────────────

func TestDefaultProfiles(t *testing.T) {
	profiles := DefaultProfiles()
	require.Len(t, profiles, 4)

	for _, role := range []string{RolePlanner, RoleResearcher, RoleCoder, RoleReviewer} {
		p, ok := profiles[role]
		require.True(t, ok, "missing default profile: %s", role)
		assert.Equal(t, role, p.ID)
		assert.Equal(t, role, p.Role)
		assert.NotEmpty(t, p.Instructions, "instructions empty for: %s", role)
		assert.NotEmpty(t, p.Tools, "tools empty for: %s", role)
	}
}

// ── ProfileError ────────────────────────────

func TestProfileError_Message(t *testing.T) {
	t.Run("with profile id", func(t *testing.T) {
		err := &ProfileError{ProfileID: "coder"}
		assert.Equal(t, "profile not found: coder", err.Error())
	})

	t.Run("with custom message", func(t *testing.T) {
		err := &ProfileError{Message: "custom failure"}
		assert.Equal(t, "custom failure", err.Error())
	})

	t.Run("empty fields fall back to generic message", func(t *testing.T) {
		err := &ProfileError{}
		assert.Equal(t, "profile not found", err.Error())
	})

	t.Run("sentinel error is never empty", func(t *testing.T) {
		assert.NotEmpty(t, ErrProfileNotFound.Error())
	})
}
