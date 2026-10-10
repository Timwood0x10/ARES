package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/introspect"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
)

// TestMemoryPanelSourceNilAndWired pins the introspect Memory wiring seam:
// a nil / non-statser manager yields a nil source (panel renders disabled),
// and a real memoryManager produces a Wired frame with live config values.
func TestMemoryPanelSourceNilAndWired(t *testing.T) {
	require.Nil(t, memoryPanelSource(nil), "nil manager must yield a nil panel source")

	cfg := memory.DefaultMemoryConfig()
	cfg.SessionMaxHistory = 33
	cfg.DistillationThreshold = 2
	mgr, mgrErr := memory.NewMemoryManager(cfg)
	require.NoError(t, mgrErr)

	src := memoryPanelSource(mgr)
	require.NotNil(t, src, "real memory manager must expose a panel source")
	frame := src()
	require.True(t, frame.Wired)
	require.Equal(t, 33, frame.SessionMaxHistory)
	require.Equal(t, 2, frame.DistillationThreshold)
	require.Equal(t, cfg.MaxHistory, frame.MaxHistory)
	require.False(t, frame.DistillationEngine, "engine-less manager reports unarmed")

	// The collector must land the frame on Snapshot.Memory.
	collector := introspect.NewCollector(introspect.Sources{Memory: src})
	snap := collector.Collect()
	require.NotNil(t, snap.Memory)
	require.True(t, snap.Memory.Wired)
}
