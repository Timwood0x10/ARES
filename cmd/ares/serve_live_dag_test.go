package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// TestBuildLiveAgentDAG_FromPeers pins the live-topology contract: one node
// per configured peer, AgentType = primary capability.
func TestBuildLiveAgentDAG_FromPeers(t *testing.T) {
	cfg := ares_config.NewMinimalConfig("http://localhost:11434", "", "")
	cfg.Agents.Peers = []ares_config.PeerAgentConfig{
		{ID: "researcher", Capabilities: []string{"research", "review"}},
		{ID: "writer", Capabilities: []string{"write"}},
	}

	dag, err := buildLiveAgentDAG(cfg)
	require.NoError(t, err)
	require.NotNil(t, dag)

	steps := dag.Steps()
	require.Len(t, steps, 2)
	byID := map[string]*engine.Step{}
	for _, s := range steps {
		byID[s.ID] = s
	}
	require.Contains(t, byID, "researcher")
	assert.Equal(t, "research", byID["researcher"].AgentType,
		"AgentType must be the primary (first) capability")
	require.Contains(t, byID, "writer")
	assert.Equal(t, "write", byID["writer"].AgentType)
}

// TestBuildLiveAgentDAG_CarriesLegacyDependencies pins that pre-C1 sub-agent
// dependency declarations survive the normalization into real DAG edges —
// recovery/workflow patches then act on the topology the operator declared.
func TestBuildLiveAgentDAG_CarriesLegacyDependencies(t *testing.T) {
	cfg := ares_config.NewMinimalConfig("http://localhost:11434", "", "")
	cfg.Agents.Sub = []ares_config.SubAgentConfig{
		{ID: "planner", Type: "plan"},
		{ID: "coder", Type: "code", Dependencies: []string{"planner"}},
	}

	dag, err := buildLiveAgentDAG(cfg)
	require.NoError(t, err)
	require.NotNil(t, dag)

	var coder *engine.Step
	for _, s := range dag.Steps() {
		if s.ID == "coder" {
			coder = s
		}
	}
	require.NotNil(t, coder)
	assert.Equal(t, []string{"planner"}, coder.DependsOn,
		"legacy agents.sub Dependencies must become DAG edges")
}

// TestBuildLiveAgentDAG_EmptyPopulationReturnsNil pins the placeholder
// contract: with no peers there is nothing live to inject — nil keeps the
// bootstrap placeholder instead of an empty graph.
func TestBuildLiveAgentDAG_EmptyPopulationReturnsNil(t *testing.T) {
	cfg := ares_config.NewMinimalConfig("http://localhost:11434", "", "")
	// NewMinimalConfig seeds a default peer population; an operator clearing
	// both lists must yield no live DAG at all.
	cfg.Agents.Peers = nil
	cfg.Agents.Sub = nil
	dag, err := buildLiveAgentDAG(cfg)
	require.ErrorIs(t, err, errNoLiveAgentDAG,
		"empty population must yield the sentinel, not a nil DAG")
	assert.Nil(t, dag)
}

// TestUpdateLiveDAG_WiredFromServeShape drives the serve-side injection chain
// end to end at the unit level: build a DAG the way buildLiveAgentDAG does,
// inject it via UpdateLiveDAG, and verify the recovery executor now mutates
// THE LIVE DAG (a strategy patch lands on its steps).
func TestUpdateLiveDAG_WiredFromServeShape(t *testing.T) {
	cfg := ares_config.NewMinimalConfig("http://localhost:11434", "", "")
	cfg.Agents.Peers = []ares_config.PeerAgentConfig{
		{ID: "worker-a", Capabilities: []string{"code"}},
	}
	liveDAG, err := buildLiveAgentDAG(cfg)
	require.NoError(t, err)
	require.NotNil(t, liveDAG)

	newEvol, err := ares_bootstrap.ProvideNewEvolution(nil, nil, nil, evidence.NewMemoryStore())
	require.NoError(t, err)
	require.NoError(t, newEvol.UpdateLiveDAG(liveDAG))

	// A recovery-strategy patch must land on the LIVE dag's steps.
	err = newEvol.PatchReg.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchChangeRecoveryStrategy,
		Target: "recovery.strategy",
		Value:  "fail_fast",
	})
	require.NoError(t, err)

	step := liveDAG.Steps()[0]
	require.NotNil(t, step.RecoveryPolicy, "live DAG step must gain the patched policy")
	assert.Equal(t, engine.RecoveryFailFast, step.RecoveryPolicy.Strategy)
}
