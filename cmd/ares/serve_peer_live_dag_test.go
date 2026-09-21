package main

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// peerFixtureConfig builds the dogfood-shaped config: one yaml peer whose
// declared capability is NOT L2-routable — the exact shape that produced
// the unschedulable worker_01 task hot loop before the GA-2 fix.
func peerFixtureConfig() *ares_config.Config {
	cfg := &ares_config.Config{}
	cfg.Agents.Peers = []ares_config.PeerAgentConfig{
		{ID: "worker_01", Capabilities: []string{"worker"}},
	}
	return cfg
}

// TestProjectLiveAgentDAGYieldsNonExecutableCapabilities documents the GA-2
// defect mechanism: the live agent DAG maps each peer to a step whose
// AgentType is the peer's yaml capability, and the planprojection maps that
// straight onto the compiled task's Capability. A yaml capability like
// "worker" is not L2-routable, so such a task can never find a capable
// executor — which is WHY serve must not compile the agent topology into
// the production task fabric.
func TestProjectLiveAgentDAGYieldsNonExecutableCapabilities(t *testing.T) {
	dag, err := buildLiveAgentDAG(peerFixtureConfig())
	if err != nil {
		t.Fatalf("buildLiveAgentDAG: %v", err)
	}
	steps := dag.Steps()
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	ps := planprojection.ProjectStep(steps[0])
	if ps.ID != "worker_01" || ps.Capability != "worker" {
		t.Fatalf("projected = id:%q cap:%q, want worker_01/worker", ps.ID, ps.Capability)
	}
	if agentfabric.IsL2Capability(ps.Capability) {
		t.Fatalf("capability %q unexpectedly L2-routable — the defect premise no longer holds", ps.Capability)
	}
}

// TestWireLiveDAGAndCompileLeavesTaskFabricEmpty is the GA-2 fix contract:
// wiring the live agent DAG must register the topology for evolution and
// the runtime manager WITHOUT projecting it into the task fabric. Before
// the fix, CompileDAG(liveDAG) created a READY task per peer with a
// non-L2 capability that the scheduler retried forever.
func TestWireLiveDAGAndCompileLeavesTaskFabricEmpty(t *testing.T) {
	ctx := context.Background()
	cfg := peerFixtureConfig()

	fabric := taskfabric.NewFabric()
	sessions := &agentruntime.Sessions{
		Reg:     agentfabric.NewSessionRegistry(),
		Fabric:  fabric,
		Compile: planprojection.NewCompileCoordinator(fabric, nil),
	}
	kernel := &kernelHandle{
		fabric:       fabric,
		compileCoord: sessions.Compile,
	}
	mgr := runtime.New(nil, nil, nil)
	comp := &ares_bootstrap.Components{
		NewEvolution: &ares_bootstrap.NewEvolutionComponents{
			PatchReg: patch.NewRegistry(),
		},
	}

	wireLiveDAGAndCompile(ctx, cfg, comp, kernel, mgr)

	if ids := fabric.IDs(); len(ids) != 0 {
		t.Fatalf("task fabric IDs = %v, want empty — agent topology must not compile into work tasks", ids)
	}
	if _, ok := mgr.GetAgentDAG(runtime.AgentDAGLiveKey); !ok {
		t.Fatal("live agent DAG must be registered on the runtime manager for evolution patches to act on")
	}
	if comp.NewEvolution == nil {
		t.Fatal("NewEvolution must remain wired")
	}
}

// TestBuildLiveAgentDAGNoPeersSentinel pins the empty-population contract:
// no peers → errNoLiveAgentDAG, and the caller keeps the bootstrap
// placeholder instead of injecting an empty graph.
func TestBuildLiveAgentDAGNoPeersSentinel(t *testing.T) {
	cfg := &ares_config.Config{}
	_, err := buildLiveAgentDAG(cfg)
	if err == nil {
		t.Fatal("empty peer population must return errNoLiveAgentDAG")
	}
}
