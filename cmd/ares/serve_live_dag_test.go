// Package main — F04 live DAG supply chain tests (Stage 8).
//
// Verifies buildLeaderLiveDAG constructs the leader's real workflow DAG from
// the configured sub-agents (input → sub steps → output) with correct
// dependency edges, and fails loud on unnamable sub-agents.
package main

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildLeaderLiveDAG_BuildsRealTopology verifies the live DAG mirrors the
// configured sub-agents: input + one step per sub-agent + output, with the
// output depending on every sub step (F04: real topology, not synthetic).
func TestBuildLeaderLiveDAG_BuildsRealTopology(t *testing.T) {
	cfg := &ares_config.Config{
		Agents: ares_config.AgentsConfig{
			Leader: ares_config.LeaderConfig{ID: "orchestrator"},
			Sub: []ares_config.SubAgentConfig{
				{ID: "researcher", Type: "research"},
				{ID: "writer", Type: "write"},
			},
		},
	}

	dag, err := buildLeaderLiveDAG(cfg)
	require.NoError(t, err, "live DAG must build from valid sub-agent config")
	require.NotNil(t, dag)

	steps := dag.Steps()
	stepIDs := make(map[string]bool, len(steps))
	for _, s := range steps {
		stepIDs[s.ID] = true
	}
	assert.True(t, stepIDs["input"], "input step must exist")
	assert.True(t, stepIDs["researcher"], "sub-agent step must exist")
	assert.True(t, stepIDs["writer"], "sub-agent step must exist")
	assert.True(t, stepIDs["output"], "output step must exist")

	// Output must depend on both sub steps (and input).
	outputDeps := dag.ReadDeps("output")
	assert.Contains(t, outputDeps, "researcher", "output must depend on researcher")
	assert.Contains(t, outputDeps, "writer", "output must depend on writer")
}

// TestBuildLeaderLiveDAG_NoSubAgents verifies a leader without sub-agents
// still yields a valid two-step DAG (input → output).
func TestBuildLeaderLiveDAG_NoSubAgents(t *testing.T) {
	cfg := &ares_config.Config{
		Agents: ares_config.AgentsConfig{
			Leader: ares_config.LeaderConfig{ID: "solo"},
		},
	}

	dag, err := buildLeaderLiveDAG(cfg)
	require.NoError(t, err, "live DAG must build without sub-agents")
	require.NotNil(t, dag)
	assert.Len(t, dag.Steps(), 2, "input + output only")
}

// TestBuildLeaderLiveDAG_UnnamedSubAgent_FailsLoud verifies a sub-agent with
// neither ID nor type is rejected instead of silently registering a broken
// DAG edge (no fabricated pass).
func TestBuildLeaderLiveDAG_UnnamedSubAgent_FailsLoud(t *testing.T) {
	cfg := &ares_config.Config{
		Agents: ares_config.AgentsConfig{
			Leader: ares_config.LeaderConfig{ID: "orchestrator"},
			Sub: []ares_config.SubAgentConfig{
				{ID: "   ", Type: "  "},
			},
		},
	}

	_, err := buildLeaderLiveDAG(cfg)
	require.Error(t, err, "unnamed sub-agent must be rejected")
	assert.Contains(t, err.Error(), "empty ID and empty type")
}
