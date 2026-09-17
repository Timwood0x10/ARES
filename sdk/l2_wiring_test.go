package sdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureL2_WiresSharedExecutionCore locks the B3 stage-1 contract: the
// SDK runtime builds the SAME agentruntime.Execution core serve/start use —
// session registry, L2 router, submitter — and spawns exactly one L2 fabric
// peer carrying the full L2 capability set. Repeated calls return the same
// core (idempotent once).
func TestEnsureL2_WiresSharedExecutionCore(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()

	exec1 := rt.ensureL2()
	require.NotNil(t, exec1, "L2 core must wire when an LLM is configured")
	require.NotNil(t, exec1.Submitter, "submission path must be wired")
	require.NotNil(t, exec1.Router, "L2 router must be the execution body")
	require.NotNil(t, exec1.Sessions, "session registry must back the core")
	assert.Same(t, exec1, rt.ensureL2(), "ensureL2 must be idempotent")
	require.NotNil(t, rt.l2Binder, "tool binder must be retained for late re-syncs")

	// The L2 peer is spawned with root/plan/answer + one capability per
	// registered tool (syscall tools bridged before the binder snapshot).
	peer, err := rt.agentsFabric.Get(l2PeerID)
	require.NoError(t, err, "L2 peer must exist in the agent fabric")
	got := peer.Capabilities
	assert.Contains(t, got, "ares/root")
	assert.Contains(t, got, "ares/plan")
	assert.Contains(t, got, "ares/answer")
	// Syscall tools are registered by ensureScheduler (which ensureL2 runs
	// first), so the planner must see them through tool/* capabilities.
	assert.Contains(t, got, "tool/spawn_agent", "syscall tools must reach the L2 planner")
}

// TestEnsureL2_NoLLM_NoCore locks the no-LLM contract: without an LLM the
// SDK builds no L2 core (nil, not a half-wired struct). The Runtime itself
// stays fully functional — Close must still tear down the scheduler that
// ensureScheduler started before the guard fired.
func TestEnsureL2_NoLLM_NoCore(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	orig := rt.llmSvc
	rt.llmSvc = nil

	assert.Nil(t, rt.ensureL2(), "no LLM must mean no L2 core")
	assert.Nil(t, rt.l2Exec, "l2Exec must stay nil (no half-wired core)")
	assert.Nil(t, rt.l2Binder, "l2Binder must stay nil (no half-wired core)")

	// Restore so Close can run its full teardown (it closes llmSvc).
	rt.llmSvc = orig
	rt.Close()
}
