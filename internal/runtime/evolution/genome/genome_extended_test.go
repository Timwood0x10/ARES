package genome

import (
	"context"
	"testing"
)

type stubGenome struct {
	name     string
	snapshot any
}

func (s *stubGenome) Name() string { return s.name }
func (s *stubGenome) Mutate(_ context.Context, n int) ([]Genome, error) {
	if n <= 0 {
		return nil, nil
	}
	result := make([]Genome, n)
	for i := range result {
		result[i] = &stubGenome{name: s.name + "_child"}
	}
	return result, nil
}
func (s *stubGenome) Snapshot(_ context.Context) (any, error) {
	return s.snapshot, nil
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubGenome{name: "g1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Get("g1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "g1" {
		t.Errorf("Name() = %q, want g1", got.Name())
	}
}

func TestRegistryRegisterNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("expected error for nil genome")
	}
}

func TestRegistryRegisterEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubGenome{name: ""}); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubGenome{name: "dup"})
	if err := r.Register(&stubGenome{name: "dup"}); err == nil {
		t.Error("expected error for duplicate")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("missing"); err == nil {
		t.Error("expected error for missing genome")
	}
}

func TestRegistryListAndUnregister(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubGenome{name: "g1"})
	_ = r.Register(&stubGenome{name: "g2"})

	if len(r.List()) != 2 {
		t.Errorf("List() len = %d, want 2", len(r.List()))
	}

	if err := r.Unregister("g1"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if len(r.List()) != 1 {
		t.Errorf("List() after unregister len = %d, want 1", len(r.List()))
	}

	if err := r.Unregister("missing"); err == nil {
		t.Error("expected error for unregistering missing")
	}
}

func TestStubGenomeMutate(t *testing.T) {
	g := &stubGenome{name: "test"}
	children, err := g.Mutate(context.Background(), 3)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if len(children) != 3 {
		t.Errorf("children len = %d, want 3", len(children))
	}

	result, err := g.Mutate(context.Background(), 0)
	if err != nil {
		t.Fatalf("Mutate(0): %v", err)
	}
	if result != nil {
		t.Errorf("Mutate(0) result = %v, want nil", result)
	}
}

func TestStubGenomeSnapshot(t *testing.T) {
	g := &stubGenome{name: "test", snapshot: "data"}
	snap, err := g.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap != "data" {
		t.Errorf("Snapshot = %v, want data", snap)
	}
}

func TestIsScoreEvaluatedBoundary(t *testing.T) {
	if IsScoreEvaluated(ScoreUnevaluated) {
		t.Error("ScoreUnevaluated should not be evaluated")
	}
	if !IsScoreEvaluated(0) {
		t.Error("0 should be evaluated")
	}
	if !IsScoreEvaluated(99.9) {
		t.Error("99.9 should be evaluated")
	}
}

func TestPromptGenomeNilScorerFitness(t *testing.T) {
	g := NewPromptGenome(nil, PromptGenomeConfig{})
	score, err := g.Fitness(context.Background())
	if err != nil {
		t.Fatalf("Fitness: %v", err)
	}
	if score != 0.5 {
		t.Errorf("Fitness = %v, want 0.5", score)
	}
}

func TestPromptGenomeNilMutatorError(t *testing.T) {
	g := NewPromptGenome(nil, PromptGenomeConfig{})
	_, err := g.Mutate(context.Background(), 1)
	if err == nil {
		t.Error("expected error for nil mutator")
	}
}

func TestPromptGenomeZeroMutate(t *testing.T) {
	g := NewPromptGenome(nil, PromptGenomeConfig{})
	result, err := g.Mutate(context.Background(), 0)
	if err != nil {
		t.Fatalf("Mutate(0): %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestPromptGenomeSnapshot(t *testing.T) {
	g := NewPromptGenome(nil, PromptGenomeConfig{})
	snap, err := g.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap == nil {
		t.Error("Snapshot returned nil")
	}
}

func TestPromptGenomeName(t *testing.T) {
	g := NewPromptGenome(nil, PromptGenomeConfig{})
	if g.Name() != "prompt" {
		t.Errorf("Name() = %q, want prompt", g.Name())
	}
}

func TestPromptGenomeCrossoverIncompatible(t *testing.T) {
	g := NewPromptGenome(nil, PromptGenomeConfig{})
	other := &stubGenome{name: "other"}
	_, err := g.Crossover(context.Background(), other)
	if err == nil {
		t.Error("expected error for incompatible genome type")
	}
}

// ── KnowledgeGenome ─────────────────────────────────────────────────────────

func TestDefaultKnowledgeGenomeConfig(t *testing.T) {
	cfg := DefaultKnowledgeGenomeConfig()
	if cfg.MaxResults != 100 {
		t.Errorf("MaxResults = %d, want 100", cfg.MaxResults)
	}
	if cfg.ReducerStrategy != defaultReducer {
		t.Errorf("ReducerStrategy = %q, want %q", cfg.ReducerStrategy, defaultReducer)
	}
	if cfg.PlannerStrategy != plannerBalanced {
		t.Errorf("PlannerStrategy = %q, want %q", cfg.PlannerStrategy, plannerBalanced)
	}
	if cfg.SummarizerType != "truncation" {
		t.Errorf("SummarizerType = %q, want truncation", cfg.SummarizerType)
	}
}

func TestKnowledgeGenomeNameAndConfig(t *testing.T) {
	cfg := KnowledgeGenomeConfig{MaxResults: 50, ReducerStrategy: "strict"}
	g := NewKnowledgeGenome(nil, cfg)
	if g.Name() != KnowledgeGenomeName {
		t.Errorf("Name() = %q, want %q", g.Name(), KnowledgeGenomeName)
	}
	got := g.Config()
	if got.MaxResults != 50 {
		t.Errorf("Config().MaxResults = %d, want 50", got.MaxResults)
	}
}

func TestKnowledgeGenomeMutateZero(t *testing.T) {
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{})
	result, err := g.Mutate(context.Background(), 0)
	if err != nil {
		t.Fatalf("Mutate(0): %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestKnowledgeGenomeMutateGenerates(t *testing.T) {
	g := NewKnowledgeGenome(nil, DefaultKnowledgeGenomeConfig())
	children, err := g.Mutate(context.Background(), 5)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if len(children) != 5 {
		t.Errorf("children len = %d, want 5", len(children))
	}
}

func TestKnowledgeGenomeSnapshot(t *testing.T) {
	cfg := KnowledgeGenomeConfig{MaxResults: 42}
	g := NewKnowledgeGenome(nil, cfg)
	snap, err := g.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	snapCfg, ok := snap.(KnowledgeGenomeConfig)
	if !ok {
		t.Fatalf("snapshot type = %T, want KnowledgeGenomeConfig", snap)
	}
	if snapCfg.MaxResults != 42 {
		t.Errorf("MaxResults = %d, want 42", snapCfg.MaxResults)
	}
}

func TestKnowledgeGenomeFitnessNilEvidence(t *testing.T) {
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{})
	score, err := g.Fitness(context.Background())
	if err != nil {
		t.Fatalf("Fitness: %v", err)
	}
	if score != 0.5 {
		t.Errorf("Fitness = %v, want 0.5 (nil evidence fallback)", score)
	}
}

func TestKnowledgeGenomeCrossoverIncompatible(t *testing.T) {
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{})
	_, err := g.Crossover(context.Background(), &stubGenome{name: "other"})
	if err == nil {
		t.Error("expected error for incompatible type")
	}
}

func TestKnowledgeGenomeCrossoverCompatible(t *testing.T) {
	g1 := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{MaxResults: 10})
	g2 := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{MaxResults: 20})
	child, err := g1.Crossover(context.Background(), g2)
	if err != nil {
		t.Fatalf("Crossover: %v", err)
	}
	if child == nil {
		t.Error("Crossover returned nil child")
	}
}

func TestKnowledgeGenomeMutateMaxResultsBounds(t *testing.T) {
	// Test that mutation keeps MaxResults in [5, 200].
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{MaxResults: 100})
	for i := 0; i < 50; i++ {
		g.mutateMaxResults()
		if g.config.MaxResults < 5 || g.config.MaxResults > 200 {
			t.Fatalf("MaxResults = %d out of bounds [5, 200] after mutation %d", g.config.MaxResults, i)
		}
	}
}

func TestKnowledgeGenomeMutateReducerStrategy(t *testing.T) {
	valid := map[string]bool{defaultReducer: true, "strict": true, "relaxed": true}
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{})
	for i := 0; i < 20; i++ {
		g.mutateReducerStrategy()
		if !valid[g.config.ReducerStrategy] {
			t.Errorf("ReducerStrategy = %q, not in valid set", g.config.ReducerStrategy)
		}
	}
}

func TestKnowledgeGenomeMutatePlannerStrategy(t *testing.T) {
	valid := map[string]bool{plannerArchFirst: true, plannerMemoryFirst: true, plannerBalanced: true}
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{})
	for i := 0; i < 20; i++ {
		g.mutatePlannerStrategy()
		if !valid[g.config.PlannerStrategy] {
			t.Errorf("PlannerStrategy = %q, not in valid set", g.config.PlannerStrategy)
		}
	}
}

func TestKnowledgeGenomeMutateSummarizerType(t *testing.T) {
	valid := map[string]bool{"truncation": true, "llm": true}
	g := NewKnowledgeGenome(nil, KnowledgeGenomeConfig{})
	for i := 0; i < 20; i++ {
		g.mutateSummarizerType()
		if !valid[g.config.SummarizerType] {
			t.Errorf("SummarizerType = %q, not in valid set", g.config.SummarizerType)
		}
	}
}

// ── MemoryGenome ────────────────────────────────────────────────────────────

func TestDefaultMemoryGenomeConfig(t *testing.T) {
	cfg := DefaultMemoryGenomeConfig()
	if cfg.MaxHistory != 10 {
		t.Errorf("MaxHistory = %d, want 10", cfg.MaxHistory)
	}
	if cfg.MaxSessions != 100 {
		t.Errorf("MaxSessions = %d, want 100", cfg.MaxSessions)
	}
	if cfg.MaxDistilledTasks != 5000 {
		t.Errorf("MaxDistilledTasks = %d, want 5000", cfg.MaxDistilledTasks)
	}
	if cfg.UseStructuredCleaning {
		t.Error("UseStructuredCleaning should be false")
	}
}

func TestMemoryGenomeNameAndConfig(t *testing.T) {
	cfg := MemoryGenomeConfig{MaxHistory: 20, MaxSessions: 200}
	g := NewMemoryGenome(cfg)
	if g.Name() != MemoryGenomeName {
		t.Errorf("Name() = %q, want %q", g.Name(), MemoryGenomeName)
	}
	got := g.Config()
	if got.MaxHistory != 20 {
		t.Errorf("Config().MaxHistory = %d, want 20", got.MaxHistory)
	}
}

func TestMemoryGenomeMutateZero(t *testing.T) {
	g := NewMemoryGenome(MemoryGenomeConfig{})
	result, err := g.Mutate(context.Background(), 0)
	if err != nil {
		t.Fatalf("Mutate(0): %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestMemoryGenomeMutateGenerates(t *testing.T) {
	g := NewMemoryGenome(DefaultMemoryGenomeConfig())
	children, err := g.Mutate(context.Background(), 3)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if len(children) != 3 {
		t.Errorf("children len = %d, want 3", len(children))
	}
}

func TestMemoryGenomeSnapshot(t *testing.T) {
	cfg := MemoryGenomeConfig{MaxHistory: 42}
	g := NewMemoryGenome(cfg)
	snap, err := g.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	snapCfg, ok := snap.(MemoryGenomeConfig)
	if !ok {
		t.Fatalf("snapshot type = %T, want MemoryGenomeConfig", snap)
	}
	if snapCfg.MaxHistory != 42 {
		t.Errorf("MaxHistory = %d, want 42", snapCfg.MaxHistory)
	}
}

func TestMemoryGenomeFitnessNilEvidence(t *testing.T) {
	g := NewMemoryGenome(MemoryGenomeConfig{})
	score, err := g.Fitness(context.Background())
	if err != nil {
		t.Fatalf("Fitness: %v", err)
	}
	if score != 0.5 {
		t.Errorf("Fitness = %v, want 0.5", score)
	}
}

func TestMemoryGenomeCrossoverIncompatible(t *testing.T) {
	g := NewMemoryGenome(MemoryGenomeConfig{})
	_, err := g.Crossover(context.Background(), &stubGenome{name: "other"})
	if err == nil {
		t.Error("expected error for incompatible type")
	}
}

func TestMemoryGenomeCrossoverCompatible(t *testing.T) {
	g1 := NewMemoryGenome(MemoryGenomeConfig{MaxHistory: 10})
	g2 := NewMemoryGenome(MemoryGenomeConfig{MaxHistory: 20})
	child, err := g1.Crossover(context.Background(), g2)
	if err != nil {
		t.Fatalf("Crossover: %v", err)
	}
	if child == nil {
		t.Error("Crossover returned nil child")
	}
}

func TestMemoryGenomeMutateBounds(t *testing.T) {
	g := NewMemoryGenome(MemoryGenomeConfig{MaxHistory: 25, MaxSessions: 250, MaxDistilledTasks: 10000})
	for i := 0; i < 50; i++ {
		g.mutateMaxHistory()
		if g.config.MaxHistory < 3 || g.config.MaxHistory > 50 {
			t.Fatalf("MaxHistory = %d out of bounds [3, 50]", g.config.MaxHistory)
		}
		g.mutateMaxSessions()
		if g.config.MaxSessions < 20 || g.config.MaxSessions > 500 {
			t.Fatalf("MaxSessions = %d out of bounds [20, 500]", g.config.MaxSessions)
		}
		g.mutateMaxDistilledTasks()
		if g.config.MaxDistilledTasks < 500 || g.config.MaxDistilledTasks > 20000 {
			t.Fatalf("MaxDistilledTasks = %d out of bounds [500, 20000]", g.config.MaxDistilledTasks)
		}
	}
}

func TestMemoryGenomeMutateStructuredCleaningToggle(t *testing.T) {
	g := NewMemoryGenome(MemoryGenomeConfig{UseStructuredCleaning: false})
	g.mutateStructuredCleaning()
	if !g.config.UseStructuredCleaning {
		t.Error("UseStructuredCleaning should be toggled to true")
	}
	g.mutateStructuredCleaning()
	if g.config.UseStructuredCleaning {
		t.Error("UseStructuredCleaning should be toggled back to false")
	}
}
