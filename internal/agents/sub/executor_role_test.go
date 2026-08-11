package sub

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestActiveRoleInstructions_NoProfile verifies that a context without an
// active agent profile yields no role instructions — the regression guard for
// the planner-fallback leak: when the leader finds no real role match, no
// profile is applied and sub prompts must stay unchanged.
func TestActiveRoleInstructions_NoProfile(t *testing.T) {
	assert.Empty(t, activeRoleInstructions(context.Background()))
}

// TestActiveRoleInstructions_WithProfile verifies that an explicit role switch
// (profile present in context) returns that role's instructions for prepending.
func TestActiveRoleInstructions_WithProfile(t *testing.T) {
	registry := agents.NewProfileRegistry()
	registry.Register(&agents.AgentProfile{
		ID:           "coder",
		Role:         agents.RoleCoder,
		Instructions: "You are a software engineer. Write tests.",
		Tools:        []string{"write_file", "run_tests"},
	})

	ctx, profile, err := registry.ApplyToContext(context.Background(), "coder")
	require.NoError(t, err)
	require.NotNil(t, profile)

	assert.Equal(t, "You are a software engineer. Write tests.", activeRoleInstructions(ctx))
}

// TestActiveRoleInstructions_EmptyInstructions verifies that a role with empty
// instructions produces an empty prepend (no stray separator is injected).
func TestActiveRoleInstructions_EmptyInstructions(t *testing.T) {
	registry := agents.NewProfileRegistry()
	registry.Register(&agents.AgentProfile{ID: "coder", Role: agents.RoleCoder}) // no Instructions

	ctx, _, err := registry.ApplyToContext(context.Background(), "coder")
	require.NoError(t, err)
	assert.Empty(t, activeRoleInstructions(ctx))
}
