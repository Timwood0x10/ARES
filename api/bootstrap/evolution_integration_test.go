package bootstrap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// newTestEvolutionService constructs a real runtimeEvoService backed by
// ProvideNewEvolution(nil, nil) — a minimal but fully wired evolution system.
func newTestEvolutionService(t *testing.T) (*runtimeEvoService, *ares_bootstrap.NewEvolutionComponents) {
	t.Helper()
	comps, err := ares_bootstrap.ProvideNewEvolution(nil, nil, nil)
	if err != nil {
		t.Fatalf("ProvideNewEvolution: %v", err)
	}
	return &runtimeEvoService{components: comps}, comps
}

func TestRuntimeEvoService_Status_Initial(t *testing.T) {
	t.Parallel()

	svc, comps := newTestEvolutionService(t)
	_ = comps

	status, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.PatchesApplied != 0 {
		t.Fatalf("expected 0 patches applied initially, got %d", status.PatchesApplied)
	}
	if status.EvidenceEntries != 0 {
		t.Fatalf("expected 0 evidence entries initially, got %d", status.EvidenceEntries)
	}
	if len(status.Genomes) == 0 {
		t.Fatal("expected at least the knowledge genome to be registered")
	}
}

func TestRuntimeEvoService_QueryEvidence_Empty(t *testing.T) {
	t.Parallel()

	svc, _ := newTestEvolutionService(t)

	results, err := svc.QueryEvidence(context.Background(), core.EvidenceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("QueryEvidence: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 evidence entries, got %d", len(results))
	}
}

func TestRuntimeEvoService_QueryEvidence_WithDirectAppend(t *testing.T) {
	t.Parallel()

	svc, comps := newTestEvolutionService(t)

	// Append evidence directly to the internal store, then verify it's queryable via the public API.
	payload, _ := json.Marshal(map[string]float64{"score": 0.85})
	entries := []evidence.Evidence{
		{ID: "ev-1", Source: "flight", Kind: evidence.KindExecutionTrace, Payload: payload, Timestamp: time.Now()},
		{ID: "ev-2", Source: "arena", Kind: evidence.KindFailure, Payload: payload, Timestamp: time.Now()},
		{ID: "ev-3", Source: "arena", Kind: evidence.KindFailure, Payload: payload, Timestamp: time.Now()},
	}
	for _, e := range entries {
		if err := comps.EvidenceStore.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Query all.
	all, err := svc.QueryEvidence(context.Background(), core.EvidenceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("QueryEvidence all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 total entries, got %d", len(all))
	}

	// Query filtered by source.
	arenaOnly, err := svc.QueryEvidence(context.Background(), core.EvidenceFilter{Source: "arena", Limit: 100})
	if err != nil {
		t.Fatalf("QueryEvidence arena: %v", err)
	}
	if len(arenaOnly) != 2 {
		t.Fatalf("expected 2 arena entries, got %d", len(arenaOnly))
	}
	for _, e := range arenaOnly {
		if e.Source != "arena" {
			t.Fatalf("expected all entries to have source=arena, got %q", e.Source)
		}
	}

	// Query filtered by kind.
	traces, err := svc.QueryEvidence(context.Background(), core.EvidenceFilter{Kind: core.EvidenceExecutionTrace, Limit: 100})
	if err != nil {
		t.Fatalf("QueryEvidence traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 execution trace, got %d", len(traces))
	}
	if traces[0].ID != "ev-1" {
		t.Fatalf("expected ev-1, got %q", traces[0].ID)
	}
}

func TestRuntimeEvoService_RegisterComponent_Success(t *testing.T) {
	t.Parallel()

	svc, _ := newTestEvolutionService(t)

	comp := &testComponent{name: "test.custom"}
	if err := svc.RegisterComponent(context.Background(), comp); err != nil {
		t.Fatalf("RegisterComponent: %v", err)
	}
}

func TestRuntimeEvoService_RegisterComponent_Duplicate(t *testing.T) {
	t.Parallel()

	svc, _ := newTestEvolutionService(t)

	comp := &testComponent{name: "knowledge.planner"} // Already registered by bootstrap.
	if err := svc.RegisterComponent(context.Background(), comp); err == nil {
		t.Fatal("expected error when registering duplicate component name")
	}
}

func TestRuntimeEvoService_RegisterComponent_ThenApplyPatch(t *testing.T) {
	t.Parallel()

	svc, comps := newTestEvolutionService(t)

	comp := &testComponent{name: "test.echo"}
	if err := svc.RegisterComponent(context.Background(), comp); err != nil {
		t.Fatalf("RegisterComponent: %v", err)
	}

	// Verify the component is reachable via the internal patch registry.
	// RegisterComponent registers by Name(), so patches must Target the same Name.
	p := patch.RuntimePatch{
		Type:   patch.PatchInsertNode,
		Target: "test.echo",
		Value:  "hello",
		Source: "test",
	}
	if err := comps.PatchReg.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !comp.applyCalled {
		t.Fatal("expected Apply to be called on the registered component")
	}
}

// testComponent is a minimal RuntimeComponent for testing registration.
type testComponent struct {
	name        string
	applyCalled bool
}

func (c *testComponent) Name() string { return c.name }

func (c *testComponent) Snapshot(_ context.Context) (any, error) { return nil, nil }

func (c *testComponent) Apply(_ context.Context, p core.RuntimePatch) (*core.RuntimePatch, error) {
	c.applyCalled = true
	return &core.RuntimePatch{
		Type:   p.Type,
		Target: p.Target,
		Reason: "echo applied",
	}, nil
}

func (c *testComponent) CanApply(_ context.Context, _ core.RuntimePatch) error { return nil }
