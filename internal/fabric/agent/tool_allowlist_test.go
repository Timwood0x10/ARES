package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgentIsToolAllowed pins the per-agent allowlist contract:
// an agent with no allowlist permits all tools; an agent with a non-nil
// allowlist permits only listed tools.
func TestAgentIsToolAllowed(t *testing.T) {
	t.Parallel()

	t.Run("nil allowlist permits all", func(t *testing.T) {
		t.Parallel()
		a := &Agent{}
		require.True(t, a.IsToolAllowed("any_tool"))
		require.True(t, a.IsToolAllowed(""))
	})

	t.Run("non-nil allowlist restricts", func(t *testing.T) {
		t.Parallel()
		a := &Agent{
			toolAllowlist: map[string]bool{
				"file_read":  true,
				"file_write": true,
			},
		}
		require.True(t, a.IsToolAllowed("file_read"))
		require.True(t, a.IsToolAllowed("file_write"))
		require.False(t, a.IsToolAllowed("network_request"))
		require.False(t, a.IsToolAllowed(""))
	})
}

// TestSpawnToolAllowlist pins the Spawn → Agent wiring: a
// SpawnSpec with a non-empty ToolAllowlist produces an agent whose
// IsToolAllowed restricts tools to that set. A nil/empty allowlist
// produces an agent that permits all tools (backward compatible).
func TestSpawnToolAllowlist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFabric()

	t.Run("non-empty allowlist restricts spawned agent", func(t *testing.T) {
		t.Parallel()
		a, err := f.Spawn(ctx, SpawnSpec{
			Identity:      "restricted",
			Capabilities:  []string{"ares/plan"},
			ToolAllowlist: []string{"file_read", "memory_recall"},
		})
		require.NoError(t, err)
		require.True(t, a.IsToolAllowed("file_read"))
		require.True(t, a.IsToolAllowed("memory_recall"))
		require.False(t, a.IsToolAllowed("network_request"))
		require.False(t, a.IsToolAllowed("spawn_agent"))
	})

	t.Run("nil allowlist permits all (backward compatible)", func(t *testing.T) {
		t.Parallel()
		a, err := f.Spawn(ctx, SpawnSpec{
			Identity:     "unrestricted",
			Capabilities: []string{"ares/plan"},
		})
		require.NoError(t, err)
		require.True(t, a.IsToolAllowed("file_read"))
		require.True(t, a.IsToolAllowed("network_request"))
		require.True(t, a.IsToolAllowed("anything"))
	})

	t.Run("empty allowlist permits all", func(t *testing.T) {
		t.Parallel()
		a, err := f.Spawn(ctx, SpawnSpec{
			Identity:      "empty-allow",
			Capabilities:  []string{"ares/plan"},
			ToolAllowlist: []string{},
		})
		require.NoError(t, err)
		require.True(t, a.IsToolAllowed("file_read"))
		require.True(t, a.IsToolAllowed("network_request"))
	})
}
