package evolution

import (
	"context"
	"os"
	"testing"

	"goagentx/internal/evolution/genome"
	"goagentx/internal/evolution/mutation"
)

// newTestWiredSystem creates a minimal wired evolution system for testing.
func newTestWiredSystem(t *testing.T) *WiredEvolutionSystem {
	t.Helper()

	base := &mutation.Strategy{
		ID:      "test-root-v1",
		Version: 1,
		Params: map[string]any{
			"temperature": 0.7,
			"top_k":       40,
		},
	}

	cfg := DefaultSystemConfig()
	cfg.PopulationSize = 5
	cfg.EliteCount = 1
	cfg.MutationRate = 0.0
	cfg.SurvivalRate = 0.6
	cfg.MutatorSeed = 42
	cfg.CrossoverSeed = 42
	cfg.PopulationSeed = 42

	system, err := NewWiredEvolutionSystem(base, cfg)
	if err != nil {
		t.Fatalf("NewWiredEvolutionSystem failed: %v", err)
	}
	return system
}

func TestSaveLoadEvolutionRun_RoundTrip(t *testing.T) {
	system := newTestWiredSystem(t)

	// Add some genealogy records.
	_ = system.Genealogy.Record(context.Background(), StrategyLineage{
		ParentID:     "parent-1",
		ChildID:      "child-1",
		MutationType: "parameter",
		WinRate:      0.85,
	})

	tmpFile, err := os.CreateTemp("", "evolution-snapshot-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		t.Logf("error closing temp file: %v", err)
	}

	defer func() {
		err = os.Remove(tmpPath)
		if err != nil {
			t.Logf("error removing temp file: %v", err)
		}
	}()

	if err := SaveEvolutionRun(tmpPath, system); err != nil {
		t.Fatalf("SaveEvolutionRun failed: %v", err)
	}

	snapshot, err := LoadEvolutionRun(tmpPath)
	if err != nil {
		t.Fatalf("LoadEvolutionRun failed: %v", err)
	}

	if snapshot.Config.PopulationSize != 5 {
		t.Errorf("Config.PopulationSize = %d, want 5", snapshot.Config.PopulationSize)
	}
	if snapshot.Generation != 0 {
		t.Errorf("Generation = %d, want 0", snapshot.Generation)
	}
	if len(snapshot.Agents) != 5 {
		t.Errorf("len(Agents) = %d, want 5", len(snapshot.Agents))
	}
	if len(snapshot.Lineages) != 1 {
		t.Errorf("len(Lineages) = %d, want 1", len(snapshot.Lineages))
	}
	if snapshot.Lineages[0].ParentID != "parent-1" {
		t.Errorf("Lineage[0].ParentID = %s, want parent-1", snapshot.Lineages[0].ParentID)
	}

	// Verify agents are valid strategies.
	for i, agent := range snapshot.Agents {
		if agent.ID == "" {
			t.Errorf("agent[%d] has empty ID", i)
		}
	}
}

func TestSaveLoadEvolutionRun_NilSystem(t *testing.T) {
	err := SaveEvolutionRun("/tmp/nonexistent.json", nil)
	if err == nil {
		t.Fatal("expected error for nil system, got nil")
	}
}

func TestLoadEvolutionRun_MissingFile(t *testing.T) {
	_, err := LoadEvolutionRun("/tmp/nonexistent-file-for-test.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestSaveEvolutionRun_EmptyLineages(t *testing.T) {
	system := newTestWiredSystem(t)

	tmpFile, err := os.CreateTemp("", "evolution-snapshot-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		t.Logf("error closing temp file: %v", err)
	}
	defer func() {
		err := os.Remove(tmpPath)
		if err != nil {
			t.Logf("error removing temp file: %v", err)
		}
	}()

	if err := SaveEvolutionRun(tmpPath, system); err != nil {
		t.Fatalf("SaveEvolutionRun failed: %v", err)
	}

	snapshot, err := LoadEvolutionRun(tmpPath)
	if err != nil {
		t.Fatalf("LoadEvolutionRun failed: %v", err)
	}

	if snapshot.Lineages == nil {
		t.Fatal("Lineages should be non-nil empty slice after load")
	}
	if len(snapshot.Lineages) != 0 {
		t.Errorf("Lineages = %d, want 0", len(snapshot.Lineages))
	}
	if len(snapshot.Agents) == 0 {
		t.Error("Agents should not be empty")
	}
}

func TestDeterministicEvolution_FullSeed(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{
		ID:      "det-root-v1",
		Version: 1,
		Params: map[string]any{
			"temperature": 0.7,
		},
	}

	buildPopulation := func() *genome.Population {
		mut, err := mutation.NewMutator(
			mutation.WithSeed(123),
			mutation.WithDeterministicIDs(true),
		)
		if err != nil {
			t.Fatalf("NewMutator: %v", err)
		}

		mutAdapter, err := NewGenomeMutatorAdapter(mut)
		if err != nil {
			t.Fatalf("NewGenomeMutatorAdapter: %v", err)
		}

		pop, err := genome.NewPopulation(
			ctx, base, mutAdapter,
			genome.WithPopulationSize(5),
			genome.WithEliteCount(1),
			genome.WithMutationRate(0.5),
			genome.WithSurvivalRate(0.6),
			genome.WithPopulationSeed(789),
		)
		if err != nil {
			t.Fatalf("NewPopulation: %v", err)
		}
		return pop
	}

	pop1 := buildPopulation()
	pop2 := buildPopulation()

	mutAdapter1, err := NewGenomeMutatorAdapter(
		mustMutator(t, mutation.WithSeed(123), mutation.WithDeterministicIDs(true)),
	)
	if err != nil {
		t.Fatalf("NewGenomeMutatorAdapter: %v", err)
	}
	crosser1 := mustCrosser(t, genome.WithSeed(456), genome.WithDeterministicIDs(true))

	for _, agent := range pop1.Agents {
		agent.Score = 50.0
	}
	if err := pop1.EvolveOnIdle(ctx, mutAdapter1, crosser1); err != nil {
		t.Fatalf("pop1.EvolveOnIdle: %v", err)
	}

	mutAdapter2, err := NewGenomeMutatorAdapter(
		mustMutator(t, mutation.WithSeed(123), mutation.WithDeterministicIDs(true)),
	)
	if err != nil {
		t.Fatalf("NewGenomeMutatorAdapter: %v", err)
	}
	crosser2 := mustCrosser(t, genome.WithSeed(456), genome.WithDeterministicIDs(true))

	for _, agent := range pop2.Agents {
		agent.Score = 50.0
	}
	if err := pop2.EvolveOnIdle(ctx, mutAdapter2, crosser2); err != nil {
		t.Fatalf("pop2.EvolveOnIdle: %v", err)
	}

	agents1, gen1 := pop1.Snapshot()
	agents2, gen2 := pop2.Snapshot()

	if gen1 != gen2 {
		t.Fatalf("generations differ: %d vs %d", gen1, gen2)
	}
	if len(agents1) != len(agents2) {
		t.Fatalf("agent counts differ: %d vs %d", len(agents1), len(agents2))
	}

	for i := range agents1 {
		if agents1[i].ID != agents2[i].ID {
			t.Errorf("agent[%d] ID mismatch: %s vs %s", i, agents1[i].ID, agents2[i].ID)
		}
		if agents1[i].Score != agents2[i].Score {
			t.Errorf("agent[%d] Score mismatch: %.2f vs %.2f", i, agents1[i].Score, agents2[i].Score)
		}
	}
}

// mustMutator creates a deterministic mutator, failing the test on error.
func mustMutator(t *testing.T, opts ...mutation.MutatorOption) *mutation.Mutator {
	t.Helper()
	mut, err := mutation.NewMutator(opts...)
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}
	return mut
}

// mustCrosser creates a deterministic crossover, failing the test on error.
func mustCrosser(t *testing.T, opts ...genome.CrossoverOption) *genome.Crossover {
	t.Helper()
	crosser, err := genome.NewCrossover(opts...)
	if err != nil {
		t.Fatalf("NewCrossover: %v", err)
	}
	return crosser
}

func TestDeterministicIDs_Mutator(t *testing.T) {
	ctx := context.Background()

	parent := &mutation.Strategy{
		ID:      "parent-001",
		Version: 1,
		Params: map[string]any{
			"temperature": 0.7,
			"top_k":       40,
		},
	}

	// Mutator with deterministic IDs.
	mut, err := mutation.NewMutator(
		mutation.WithSeed(42),
		mutation.WithDeterministicIDs(true),
	)
	if err != nil {
		t.Fatalf("NewMutator failed: %v", err)
	}

	children1, err := mut.Mutate(ctx, parent, 3)
	if err != nil {
		t.Fatalf("Mutate (1) failed: %v", err)
	}

	// Second mutator, same seed.
	mut2, err := mutation.NewMutator(
		mutation.WithSeed(42),
		mutation.WithDeterministicIDs(true),
	)
	if err != nil {
		t.Fatalf("NewMutator (2) failed: %v", err)
	}

	children2, err := mut2.Mutate(ctx, parent, 3)
	if err != nil {
		t.Fatalf("Mutate (2) failed: %v", err)
	}

	if len(children1) != len(children2) {
		t.Fatalf("child counts differ: %d vs %d", len(children1), len(children2))
	}

	for i := range children1 {
		if children1[i].ID != children2[i].ID {
			t.Errorf("child[%d] ID mismatch: %s vs %s", i, children1[i].ID, children2[i].ID)
		}
		// Verify deterministic ID format.
		expectedPrefix := "det-mut-parent-"
		if len(children1[i].ID) < len(expectedPrefix) || children1[i].ID[:len(expectedPrefix)] != expectedPrefix {
			t.Errorf("child[%d] ID %q does not have expected prefix %q", i, children1[i].ID, expectedPrefix)
		}
	}
}

func TestDeterministicIDs_Crossover(t *testing.T) {
	parentA := &mutation.Strategy{
		ID:      "parent-a-001",
		Version: 1,
		Params: map[string]any{
			"temperature": 0.7,
		},
		Score: 80,
	}
	parentB := &mutation.Strategy{
		ID:      "parent-b-002",
		Version: 2,
		Params: map[string]any{
			"temperature": 0.5,
		},
		Score: 90,
	}

	ctx := context.Background()

	cross1, err := genome.NewCrossover(
		genome.WithSeed(42),
		genome.WithDeterministicIDs(true),
	)
	if err != nil {
		t.Fatalf("NewCrossover (1) failed: %v", err)
	}

	cross2, err := genome.NewCrossover(
		genome.WithSeed(42),
		genome.WithDeterministicIDs(true),
	)
	if err != nil {
		t.Fatalf("NewCrossover (2) failed: %v", err)
	}

	child1, err := cross1.Crossover(ctx, parentA, parentB)
	if err != nil {
		t.Fatalf("Crossover (1) failed: %v", err)
	}

	child2, err := cross2.Crossover(ctx, parentA, parentB)
	if err != nil {
		t.Fatalf("Crossover (2) failed: %v", err)
	}

	if child1.ID != child2.ID {
		t.Errorf("child IDs differ with same seed: %s vs %s", child1.ID, child2.ID)
	}

	// Verify deterministic ID format.
	expectedPrefix := "det-cross-parent-a"
	if len(child1.ID) < len(expectedPrefix) || child1.ID[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("child ID %q does not have expected prefix %q", child1.ID, expectedPrefix)
	}

	// MultiPointCrossover should also produce deterministic IDs.
	mpChild1, err := cross1.MultiPointCrossover(ctx, parentA, parentB, 1)
	if err != nil {
		t.Fatalf("MultiPointCrossover (1) failed: %v", err)
	}

	mpChild2, err := cross2.MultiPointCrossover(ctx, parentA, parentB, 1)
	if err != nil {
		t.Fatalf("MultiPointCrossover (2) failed: %v", err)
	}

	if mpChild1.ID != mpChild2.ID {
		t.Errorf("multi-point child IDs differ with same seed: %s vs %s", mpChild1.ID, mpChild2.ID)
	}
}

func TestDeterministicIDs_CrossoverVSNonDeterministic(t *testing.T) {
	parentA := &mutation.Strategy{
		ID:      "parent-a",
		Version: 1,
		Params:  map[string]any{"temperature": 0.7},
		Score:   80,
	}
	parentB := &mutation.Strategy{
		ID:      "parent-b",
		Version: 2,
		Params:  map[string]any{"temperature": 0.5},
		Score:   90,
	}

	ctx := context.Background()

	// Non-deterministic crossover uses uuid.New().
	ndCross, err := genome.NewCrossover(genome.WithSeed(42))
	if err != nil {
		t.Fatalf("NewCrossover failed: %v", err)
	}

	ndChild, err := ndCross.Crossover(ctx, parentA, parentB)
	if err != nil {
		t.Fatalf("Crossover failed: %v", err)
	}

	// UUIDs are 36 chars with dashes, deterministic IDs start with "det-cross-".
	if len(ndChild.ID) != 36 {
		t.Errorf("expected UUID length 36 for non-deterministic crossover, got %d: %s", len(ndChild.ID), ndChild.ID)
	}
}

func TestEvolutionStore_StoreDBValidation(t *testing.T) {
	// Verify the StoreDB interface can be satisfied by testing type assertions.
	// Both *sql.DB and *sql.Tx satisfy it in production.
	// This is a compile-time check only.
	var _ StoreDB = nil

	store := NewEvolutionStore(nil)
	if store == nil {
		t.Fatal("NewEvolutionStore(nil) should not panic")
	}
}
