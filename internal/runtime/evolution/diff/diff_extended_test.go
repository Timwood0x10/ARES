package diff

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

type stubDiffer struct {
	name    string
	patches []patch.RuntimePatch
	err     error
}

func (s *stubDiffer) Name() string { return s.name }
func (s *stubDiffer) Diff(_ context.Context, _, _ any) ([]patch.RuntimePatch, error) {
	return s.patches, s.err
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubDiffer{name: "d1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Get("d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "d1" {
		t.Errorf("Name() = %q, want d1", got.Name())
	}
}

func TestRegistryRegisterNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("expected error for nil differ")
	}
}

func TestRegistryRegisterEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubDiffer{name: ""}); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubDiffer{name: "dup"})
	if err := r.Register(&stubDiffer{name: "dup"}); err == nil {
		t.Error("expected error for duplicate name")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("missing"); err == nil {
		t.Error("expected error for missing differ")
	}
}

func TestRegistryListMultiple(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubDiffer{name: "a"})
	_ = r.Register(&stubDiffer{name: "b"})
	names := r.List()
	if len(names) != 2 {
		t.Errorf("List() len = %d, want 2", len(names))
	}
}

func TestRegistryDiffAllSuccess(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubDiffer{
		name:    "test",
		patches: []patch.RuntimePatch{{Type: patch.PatchChangeBudget, Target: "t", Value: 1}},
	})
	patches, err := r.DiffAll(context.Background(), map[string]SnapshotPair{
		"test": {Old: 1, New: 2},
	})
	if err != nil {
		t.Fatalf("DiffAll: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("patches len = %d, want 1", len(patches))
	}
}

func TestRegistryDiffAllUnregistered(t *testing.T) {
	r := NewRegistry()
	_, err := r.DiffAll(context.Background(), map[string]SnapshotPair{
		"unknown": {},
	})
	if err == nil {
		t.Error("expected error for unregistered differ")
	}
}

func TestRegistryDiffAllDifferError(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubDiffer{name: "bad", err: errors.New("diff failed")})
	_, err := r.DiffAll(context.Background(), map[string]SnapshotPair{
		"bad": {},
	})
	if err == nil {
		t.Error("expected error from differ")
	}
}

// ── KnowledgeDiffer ─────────────────────────────────────────────────────────

func TestKnowledgeDifferIdentity(t *testing.T) {
	d := NewKnowledgeDiffer()
	if d.Name() != genome.KnowledgeGenomeName {
		t.Errorf("Name() = %q, want %q", d.Name(), genome.KnowledgeGenomeName)
	}
}

func TestKnowledgeDifferTypeErrors(t *testing.T) {
	d := NewKnowledgeDiffer()
	ctx := context.Background()
	if _, err := d.Diff(ctx, "bad", genome.KnowledgeGenomeConfig{}); err == nil {
		t.Error("expected error for bad old type")
	}
	if _, err := d.Diff(ctx, genome.KnowledgeGenomeConfig{}, 42); err == nil {
		t.Error("expected error for bad new type")
	}
}

func TestKnowledgeDifferNoChange(t *testing.T) {
	d := NewKnowledgeDiffer()
	cfg := genome.KnowledgeGenomeConfig{MaxResults: 10, ReducerStrategy: "prune", PlannerStrategy: "greedy", SummarizerType: "ext"}
	patches, err := d.Diff(context.Background(), cfg, cfg)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("patches len = %d, want 0", len(patches))
	}
}

func TestKnowledgeDifferAllChanged(t *testing.T) {
	d := NewKnowledgeDiffer()
	old := genome.KnowledgeGenomeConfig{MaxResults: 10, ReducerStrategy: "a", PlannerStrategy: "b", SummarizerType: "c"}
	newCfg := genome.KnowledgeGenomeConfig{MaxResults: 20, ReducerStrategy: "x", PlannerStrategy: "y", SummarizerType: "z"}
	patches, err := d.Diff(context.Background(), old, newCfg)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 4 {
		t.Errorf("patches len = %d, want 4", len(patches))
	}
}

func TestKnowledgeDifferPartial(t *testing.T) {
	d := NewKnowledgeDiffer()
	old := genome.KnowledgeGenomeConfig{MaxResults: 10}
	newCfg := genome.KnowledgeGenomeConfig{MaxResults: 20}
	patches, err := d.Diff(context.Background(), old, newCfg)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("patches len = %d, want 1", len(patches))
	}
	if patches[0].Target != "knowledge.planner.max_results" {
		t.Errorf("Target = %q", patches[0].Target)
	}
}

// ── MemoryDiffer ────────────────────────────────────────────────────────────

func TestMemoryDifferIdentity(t *testing.T) {
	d := NewMemoryDiffer()
	if d.Name() != genome.MemoryGenomeName {
		t.Errorf("Name() = %q, want %q", d.Name(), genome.MemoryGenomeName)
	}
}

func TestMemoryDifferTypeErrors(t *testing.T) {
	d := NewMemoryDiffer()
	ctx := context.Background()
	if _, err := d.Diff(ctx, 42, genome.MemoryGenomeConfig{}); err == nil {
		t.Error("expected error for bad old type")
	}
	if _, err := d.Diff(ctx, genome.MemoryGenomeConfig{}, "bad"); err == nil {
		t.Error("expected error for bad new type")
	}
}

func TestMemoryDifferNoChange(t *testing.T) {
	d := NewMemoryDiffer()
	cfg := genome.MemoryGenomeConfig{MaxHistory: 50, MaxSessions: 10, MaxDistilledTasks: 100, UseStructuredCleaning: true}
	patches, err := d.Diff(context.Background(), cfg, cfg)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("patches len = %d, want 0", len(patches))
	}
}

func TestMemoryDifferAllChanged(t *testing.T) {
	d := NewMemoryDiffer()
	old := genome.MemoryGenomeConfig{MaxHistory: 50, MaxSessions: 10, MaxDistilledTasks: 100, UseStructuredCleaning: false}
	newCfg := genome.MemoryGenomeConfig{MaxHistory: 80, MaxSessions: 20, MaxDistilledTasks: 200, UseStructuredCleaning: true}
	patches, err := d.Diff(context.Background(), old, newCfg)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 3 {
		t.Errorf("patches len = %d, want 3", len(patches))
	}
}

// ── RecoveryDiffer ──────────────────────────────────────────────────────────

func TestRecoveryDifferIdentity(t *testing.T) {
	d := NewRecoveryDiffer()
	if d.Name() != genome.RecoveryGenomeName {
		t.Errorf("Name() = %q, want %q", d.Name(), genome.RecoveryGenomeName)
	}
}

func TestRecoveryDifferTypeErrors(t *testing.T) {
	d := NewRecoveryDiffer()
	ctx := context.Background()
	if _, err := d.Diff(ctx, "bad", &engine.RecoveryPolicy{}); err == nil {
		t.Error("expected error for bad old type")
	}
	if _, err := d.Diff(ctx, &engine.RecoveryPolicy{}, 42); err == nil {
		t.Error("expected error for bad new type")
	}
}

func TestRecoveryDifferNoChange(t *testing.T) {
	d := NewRecoveryDiffer()
	p := &engine.RecoveryPolicy{Strategy: "retry", MaxAttempts: 3, ReplacementAgent: "a"}
	patches, err := d.Diff(context.Background(), p, p)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("patches len = %d, want 0", len(patches))
	}
}

func TestRecoveryDifferAllChanged(t *testing.T) {
	d := NewRecoveryDiffer()
	old := &engine.RecoveryPolicy{Strategy: "retry", MaxAttempts: 3, ReplacementAgent: "a"}
	newPolicy := &engine.RecoveryPolicy{Strategy: "fallback", MaxAttempts: 5, ReplacementAgent: "b"}
	patches, err := d.Diff(context.Background(), old, newPolicy)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 3 {
		t.Errorf("patches len = %d, want 3", len(patches))
	}
}

func TestRecoveryDifferPartial(t *testing.T) {
	d := NewRecoveryDiffer()
	old := &engine.RecoveryPolicy{Strategy: "retry", MaxAttempts: 3}
	newPolicy := &engine.RecoveryPolicy{Strategy: "retry", MaxAttempts: 5}
	patches, err := d.Diff(context.Background(), old, newPolicy)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("patches len = %d, want 1", len(patches))
	}
	if patches[0].Target != "recovery.max_attempts" {
		t.Errorf("Target = %q", patches[0].Target)
	}
}
