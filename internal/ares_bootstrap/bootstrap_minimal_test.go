package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/require"
)

// TestBootstrapMinimalConfig verifies the "only LLM url + api key" path is
// fully runnable: a config built from just the endpoint assembles every
// subsystem (memory, event store, LLM, agents wiring prerequisites) without
// requiring a YAML file or an external database. Evolution/AKG stay off
// (default), storage falls back to in-memory.
func TestBootstrapMinimalConfig(t *testing.T) {
	cfg := ares_config.NewMinimalConfig("https://api.example.com/v1", "sk-test", "")
	require.True(t, cfg.Memory.IsEnabled(), "memory must be enabled for the leader")
	require.NotEmpty(t, cfg.Agents.Sub, "default agent team must be assembled")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	comp, err := Bootstrap(ctx, cfg, &BootstrapDeps{
		EventStore: ares_events.NewMemoryEventStore(),
	})
	require.NoError(t, err, "minimal config must bootstrap without error")
	require.NotNil(t, comp)
	require.NotNil(t, comp.Memory, "memory component must be wired")
	require.NotNil(t, comp.LLM, "LLM component must be wired")

	// Default-off subsystems stay off: no evolution, no distillation, no AKG.
	require.Nil(t, comp.Evolution, "evolution must stay off in minimal mode")
	require.Nil(t, comp.Distillation, "distillation must stay off in minimal mode")

	// A snapshot must be available even though System Runtime wiring may be
	// partial.
	_ = comp.Snapshot()
}
