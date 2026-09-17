package kernel

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestBuildCandidates_HybridStaticWinsOwnCapability locks the hybrid merge
// contract (SDK mode): a static executor overlapping the task's capability
// suppresses overlapping fabric candidates — registered agents keep their
// pre-L2 behavior even after the L2 router peer joins the pool — while a
// fabric candidate serving a capability no static executor claims stays in
// the offered list.
func TestBuildCandidates_HybridStaticWinsOwnCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "hyb-peer",
		Capabilities: []string{"hyb-plan", "tool/web-search"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.WithAgentFabric(agents).WithStaticPoolHybrid()
	sched.RegisterExecutor("static-plan", &smokeExecutor{id: "static-plan", typ: models.AgentType("hyb-plan")})

	// A task whose capability the static executor owns: the fabric peer
	// advertises the same capability but must lose to the static executor.
	if err := fabric.Create(&taskfabric.Task{
		ID:          "hyb-t1",
		Capability:  "hyb-plan",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	seen := hybIDs(sched.buildCandidates("hyb-t1"))
	if !hybContains(seen, "static-plan") {
		t.Fatalf("static executor must stay a candidate, got %v", seen)
	}
	if hybContains(seen, "hyb-peer") {
		t.Fatalf("fabric peer must lose to the static executor on its own capability, got %v", seen)
	}

	// A capability no static executor claims: the fabric peer must stay.
	if err := fabric.Create(&taskfabric.Task{
		ID:          "hyb-t2",
		Capability:  "tool/web-search",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	seen = hybIDs(sched.buildCandidates("hyb-t2"))
	if !hybContains(seen, "hyb-peer") {
		t.Fatalf("fabric peer must remain a candidate for unowned capabilities, got %v", seen)
	}
}

func hybIDs(cands []taskfabric.Candidate) []string {
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.AgentID)
	}
	return ids
}

func hybContains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
