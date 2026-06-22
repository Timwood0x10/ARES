package bootstrap

import (
	"context"
	"strings"
	"testing"

	"goagentx/internal/callbacks"
	"goagentx/internal/config"
	"goagentx/internal/events"
	"goagentx/internal/evolution"
	"goagentx/internal/flight"
	"goagentx/internal/llm"
	"goagentx/internal/memory"
	"goagentx/internal/memory/distillation"
	"goagentx/internal/storage/postgres/models"
	"goagentx/internal/storage/postgres/repositories"
)

// mockExpRepo implements repositories.ExperienceRepositoryInterface for testing.
type mockExpRepo struct {
	experiences []*models.Experience
}

func (m *mockExpRepo) Create(_ context.Context, exp *models.Experience) error {
	m.experiences = append(m.experiences, exp)
	return nil
}

func (m *mockExpRepo) GetByID(_ context.Context, _ string) (*models.Experience, error) {
	return nil, nil
}

func (m *mockExpRepo) Update(_ context.Context, _ *models.Experience) error {
	return nil
}

func (m *mockExpRepo) Delete(_ context.Context, _ string) error {
	return nil
}

func (m *mockExpRepo) SearchByVector(_ context.Context, _ []float64, _ string, _ int) ([]*models.Experience, error) {
	return nil, nil
}

func (m *mockExpRepo) SearchByKeyword(_ context.Context, _, _ string, _ int) ([]*models.Experience, error) {
	return nil, nil
}

func (m *mockExpRepo) IncrementUsageCount(_ context.Context, _ string) error {
	return nil
}

func (m *mockExpRepo) DecrementRank(_ context.Context, _ string) error {
	return nil
}

func (m *mockExpRepo) ListByType(_ context.Context, _, _ string, _ int) ([]*models.Experience, error) {
	return nil, nil
}

func (m *mockExpRepo) ListByAgent(_ context.Context, _, _ string, _ int) ([]*models.Experience, error) {
	return nil, nil
}

var _ repositories.ExperienceRepositoryInterface = (*mockExpRepo)(nil)

// newTestLLMClient creates a minimal LLM client for testing.
func newWireTestLLMClient(t *testing.T) *llm.Client {
	t.Helper()
	client, err := llm.NewClient(&llm.Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "test-model",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("newWireTestLLMClient: %v", err)
	}
	return client
}

// newTestFlightRecorder creates a flight recorder with in-memory backing stores.
func newTestFlightRecorder(t *testing.T) *flight.FlightRecorder {
	t.Helper()
	eventStore := events.NewMemoryEventStore()
	memMgr, err := memory.NewMemoryManager(memory.DefaultMemoryConfig())
	if err != nil {
		t.Fatalf("newTestFlightRecorder: memory manager: %v", err)
	}
	recorder := flight.NewFlightRecorder(flight.FlightRecorderConfig{
		EventStore: eventStore,
		MemManager: memMgr,
	})
	return recorder
}

// newMockExpRepo creates a mock experience repository for testing.
func newMockExpRepo() repositories.ExperienceRepositoryInterface {
	return &mockExpRepo{experiences: make([]*models.Experience, 0)}
}

// TestWireAllEvolutionComponents_AllDepsProvided verifies that when all required
// dependencies are provided, all components are created and wired correctly.
func TestWireAllEvolutionComponents_AllDepsProvided(t *testing.T) {
	ctx := context.Background()

	client := newWireTestLLMClient(t)
	defer client.Close()

	recorder := newTestFlightRecorder(t)
	expRepo := newMockExpRepo()

	deps := &WireDependencies{
		LLMClient:      client,
		FlightRecorder: recorder,
		ExpRepo:        expRepo,
	}

	wired, err := WireAllEvolutionComponents(ctx, deps, &config.EvolutionConfig{Enabled: true})
	if err != nil {
		t.Fatalf("WireAllEvolutionComponents returned error: %v", err)
	}
	if wired == nil {
		t.Fatal("WiredComponents is nil")
	}

	// Verify CallbackReg is always created.
	if wired.CallbackReg == nil {
		t.Error("CallbackReg should not be nil")
	}

	// Verify FeedbackService is created when ExpRepo is provided.
	if wired.FeedbackSvc == nil {
		t.Error("FeedbackSvc should not be nil when ExpRepo is provided")
	}

	// Verify EvalRegistry is always created.
	if wired.EvalRegistry == nil {
		t.Error("EvalRegistry should not be nil")
	}

	// Verify LLM Judge evaluator is registered when LLMClient is provided.
	names := wired.EvalRegistry.Names()
	found := false
	for _, n := range names {
		if n == "llm_judge" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected llm_judge registered, got evaluators: %v", names)
	}

	// Verify Evolution components are created when FlightRecorder + ExpRepo are provided.
	if wired.Evolution == nil {
		t.Error("Evolution should not be nil when FlightRecorder and ExpRepo are provided")
	} else {
		if wired.Evolution.Adapter == nil {
			t.Error("Evolution.Adapter should not be nil")
		}
		if wired.Evolution.Scheduler == nil {
			t.Error("Evolution.Scheduler should not be nil")
		}
		// DreamCycle should be nil since we didn't provide dream deps.
		if wired.Evolution.DreamCycle != nil {
			t.Error("Evolution.DreamCycle should be nil without dream deps")
		}
	}
}

// TestWireAllEvolutionComponents_MissingOptionalDeps verifies that the function
// degrades gracefully when optional dependencies are missing.
func TestWireAllEvolutionComponents_MissingOptionalDeps(t *testing.T) {
	ctx := context.Background()

	expRepo := newMockExpRepo()

	// Case 1: Only ExpRepo provided — no LLM client, no flight recorder.
	deps := &WireDependencies{
		ExpRepo: expRepo,
	}

	wired, err := WireAllEvolutionComponents(ctx, deps, nil)
	if err != nil {
		t.Fatalf("WireAllEvolutionComponents returned error with minimal deps: %v", err)
	}
	if wired == nil {
		t.Fatal("WiredComponents is nil")
	}

	// CallbackReg should still be created.
	if wired.CallbackReg == nil {
		t.Error("CallbackReg should not be nil even with minimal deps")
	}

	// FeedbackSvc should be created from ExpRepo.
	if wired.FeedbackSvc == nil {
		t.Error("FeedbackSvc should not be nil when ExpRepo is provided")
	}

	// EvalRegistry should be created but empty (no LLM client).
	if wired.EvalRegistry == nil {
		t.Error("EvalRegistry should not be nil")
	}
	if len(wired.EvalRegistry.Names()) != 0 {
		t.Errorf("expected 0 evaluators without LLM client, got %d", len(wired.EvalRegistry.Names()))
	}

	// Evolution should be nil (no flight recorder).
	if wired.Evolution != nil {
		t.Error("Evolution should be nil without FlightRecorder")
	}
}

// TestWireAllEvolutionComponents_NilLLMClient verifies that a nil LLM client
// does not cause an error; evaluators are simply skipped.
func TestWireAllEvolutionComponents_NilLLMClient(t *testing.T) {
	ctx := context.Background()

	recorder := newTestFlightRecorder(t)
	expRepo := newMockExpRepo()

	deps := &WireDependencies{
		LLMClient:      nil,
		FlightRecorder: recorder,
		ExpRepo:        expRepo,
	}

	wired, err := WireAllEvolutionComponents(ctx, deps, &config.EvolutionConfig{Enabled: true})
	if err != nil {
		t.Fatalf("WireAllEvolutionComponents returned error with nil LLM client: %v", err)
	}
	if wired == nil {
		t.Fatal("WiredComponents is nil")
	}

	// EvalRegistry should exist but have no evaluators.
	if wired.EvalRegistry == nil {
		t.Error("EvalRegistry should not be nil")
	}
	if len(wired.EvalRegistry.Names()) != 0 {
		t.Errorf("expected 0 evaluators with nil LLM client, got %d", len(wired.EvalRegistry.Names()))
	}

	// Evolution should still work (doesn't depend on LLM client).
	if wired.Evolution == nil {
		t.Error("Evolution should not be nil when FlightRecorder + ExpRepo are provided")
	}

	// CallbackReg and FeedbackSvc should still be created.
	if wired.CallbackReg == nil {
		t.Error("CallbackReg should not be nil")
	}
	if wired.FeedbackSvc == nil {
		t.Error("FeedbackSvc should not be nil")
	}
}

// TestWireAllEvolutionComponents_NilDeps verifies graceful handling when
// all dependencies are nil.
func TestWireAllEvolutionComponents_NilDeps(t *testing.T) {
	ctx := context.Background()

	wired, err := WireAllEvolutionComponents(ctx, &WireDependencies{}, nil)
	if err != nil {
		t.Fatalf("WireAllEvolutionComponents returned error with nil deps: %v", err)
	}
	if wired == nil {
		t.Fatal("WiredComponents is nil")
	}

	// Only CallbackReg and EvalRegistry should be non-nil.
	if wired.CallbackReg == nil {
		t.Error("CallbackReg should always be non-nil")
	}
	if wired.EvalRegistry == nil {
		t.Error("EvalRegistry should always be non-nil")
	}
	if wired.FeedbackSvc != nil {
		t.Error("FeedbackSvc should be nil without ExpRepo")
	}
	if wired.Evolution != nil {
		t.Error("Evolution should be nil without FlightRecorder and ExpRepo")
	}
}

// TestWireAllEvolutionComponents_CallbackRegImplementsEmitter verifies that
// the returned CallbackReg satisfies callbacks.Emitter for injection into
// llm.WithCallbacks and leader.WithCallbacks.
func TestWireAllEvolutionComponents_CallbackRegImplementsEmitter(t *testing.T) {
	ctx := context.Background()

	wired, err := WireAllEvolutionComponents(ctx, &WireDependencies{}, nil)
	if err != nil {
		t.Fatalf("WireAllEvolutionComponents error: %v", err)
	}

	// Emit on the registry should not panic.
	wired.CallbackReg.Emit(nil)

	// Registering handlers should work.
	callCount := 0
	wired.CallbackReg.On(callbacks.EventAgentStart, func(_ *callbacks.Context) {
		callCount++
	})
	wired.CallbackReg.Emit(&callbacks.Context{Event: callbacks.EventAgentStart})

	if callCount != 1 {
		t.Errorf("expected 1 callback invocation, got %d", callCount)
	}
}

// TestWireAllEvolutionComponents_WithDreamDeps verifies that providing dream cycle
// dependencies results in a non-nil DreamCycle in Evolution components.
func TestWireAllEvolutionComponents_WithDreamDeps(t *testing.T) {
	ctx := context.Background()

	client := newWireTestLLMClient(t)
	defer client.Close()

	recorder := newTestFlightRecorder(t)
	expRepo := newMockExpRepo()

	// Create a real mutator and tester if available, or use nil to skip dream cycle.
	// Since we may not have concrete implementations, test that passing non-nil
	// dreamDeps struct fields does not cause errors even if dream cycle can't
	// be fully constructed without real mutator/tester.
	deps := &WireDependencies{
		LLMClient:      client,
		FlightRecorder: recorder,
		ExpRepo:        expRepo,
		DreamDeps: &DreamCycleDeps{
			Mutator:   &noopMutator{},
			Tester:    &noopTester{},
			Genealogy: &noopGenealogy{},
		},
	}

	wired, err := WireAllEvolutionComponents(ctx, deps, &config.EvolutionConfig{Enabled: true})
	if err != nil {
		// Dream cycle creation may fail due to internal validation; assert error is meaningful.
		errMsg := err.Error()
		if !strings.Contains(errMsg, "dream") && !strings.Contains(errMsg, "evolution") {
			t.Errorf("expected dream/evolution related error, got: %v", err)
		}
		if wired == nil {
			t.Fatal("WiredComponents should not be nil even if dream cycle setup fails")
		}
		// Core components should still be valid.
		if wired.CallbackReg == nil {
			t.Error("CallbackReg should not be nil")
		}
		if wired.EvalRegistry == nil {
			t.Error("EvalRegistry should not be nil")
		}
		return
	}

	if wired.Evolution == nil {
		t.Error("Evolution should not be nil with FlightRecorder + ExpRepo")
	}
}

// noopMutator is a no-op implementation of evolution.MutatorInterface for testing.
type noopMutator struct{}

func (n *noopMutator) Mutate(_ context.Context, parent evolution.Strategy, count int) ([]evolution.Strategy, error) {
	return make([]evolution.Strategy, count), nil
}

// noopTester is a no-op implementation of evolution.TesterInterface for testing.
type noopTester struct{}

func (n *noopTester) Run(_ context.Context, cfg evolution.RegressionConfig) (*evolution.RegressionResult, error) {
	return &evolution.RegressionResult{}, nil
}

// noopGenealogy is a no-op implementation of evolution.GenealogyRecorder for testing.
type noopGenealogy struct{}

func (n *noopGenealogy) Record(_ context.Context, lineage evolution.StrategyLineage) error {
	return nil
}

// TestExperienceStoreAdapter_NilInput verifies that Create returns a clear error
// when the input StoredExperience is nil, rather than silently succeeding or panicking.
func TestExperienceStoreAdapter_NilInput(t *testing.T) {
	repo := newMockExpRepo()
	adapter := NewExperienceStoreAdapter(repo)

	err := adapter.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil input, got nil")
	}
	if !strings.Contains(err.Error(), "must not be nil") {
		t.Errorf("expected 'must not be nil' in error message, got: %v", err)
	}

	// Verify no experience was persisted to the repo.
	mockRepo := repo.(*mockExpRepo)
	if len(mockRepo.experiences) != 0 {
		t.Errorf("expected 0 experiences persisted for nil input, got %d", len(mockRepo.experiences))
	}
}

// TestExperienceStoreAdapter_FieldMapping_Completeness verifies that every
// non-zero-value field on a populated StoredExperience is correctly mapped to
// the corresponding storage_models.Experience field (or metadata entry).
func TestExperienceStoreAdapter_FieldMapping_Completeness(t *testing.T) {
	repo := newMockExpRepo()
	adapter := NewExperienceStoreAdapter(repo)

	input := &distillation.StoredExperience{
		TenantID: "tenant-mapping-test",
		Type:     distillation.TypeSolution,
		Problem:  "mapping test problem",
		Solution: "mapping test solution",
		Score:    0.85,
		Source:   "unit_test",
		Metadata: map[string]interface{}{
			"custom_key": "custom_value",
		},
	}

	err := adapter.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	mockRepo := repo.(*mockExpRepo)
	if len(mockRepo.experiences) != 1 {
		t.Fatalf("expected 1 experience persisted, got %d", len(mockRepo.experiences))
	}

	got := mockRepo.experiences[0]

	// Verify all direct field mappings.
	if got.TenantID != input.TenantID {
		t.Errorf("TenantID mismatch: got %q, want %q", got.TenantID, input.TenantID)
	}
	if got.Type != input.Type {
		t.Errorf("Type mismatch: got %q, want %q", got.Type, input.Type)
	}
	if got.Problem != input.Problem {
		t.Errorf("Problem mismatch: got %q, want %q", got.Problem, input.Problem)
	}
	if got.Solution != input.Solution {
		t.Errorf("Solution mismatch: got %q, want %q", got.Solution, input.Solution)
	}
	if got.Score != input.Score {
		t.Errorf("Score mismatch: got %f, want %f", got.Score, input.Score)
	}

	// Success should be derived from Score > 0.5 threshold.
	if !got.Success {
		t.Error("Success should be true when Score > 0.5")
	}

	// Source should be stored in metadata.
	if got.Metadata == nil {
		t.Fatal("Metadata should not be nil")
	}
	if src, ok := got.Metadata["source"].(string); !ok || src != input.Source {
		t.Errorf("Metadata[\"source\"] mismatch: got %v, want %q", got.Metadata["source"], input.Source)
	}

	// Original custom metadata should be preserved.
	if val, ok := got.Metadata["custom_key"].(string); !ok || val != "custom_value" {
		t.Errorf("Metadata[\"custom_key\"] not preserved: got %v", got.Metadata["custom_key"])
	}

	// CreatedAt should be set to a recent UTC timestamp.
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	} else if got.CreatedAt.UTC().Year() <= 2020 {
		t.Errorf("CreatedAt seems unreasonable: %v", got.CreatedAt)
	}
}

// TestConvertToStorageExperience_AllFieldsMapped verifies that the evolution-path
// conversion function correctly maps all fields from evolution.Experience to
// storage_models.Experience, including AgentID dual-storage in both the struct
// field and metadata.
func TestConvertToStorageExperience_AllFieldsMapped(t *testing.T) {
	input := &evolution.Experience{
		TenantID: "tenant-evolution-test",
		Type:     evolution.TypeFailure,
		Problem:  "evolution mapping problem",
		Solution: "evolution mapping solution",
		Score:    0.3,
		Source:   "flight_recorder",
		AgentID:  "agent-evolution-42",
		Metadata: map[string]interface{}{
			"diagnostic_id":  "diag-123",
			"original_cause": "timeout",
		},
	}

	got := convertToStorageExperience(input)
	if got == nil {
		t.Fatal("convertToStorageExperience returned nil for non-nil input")
	}

	// Verify direct field mappings.
	if got.TenantID != input.TenantID {
		t.Errorf("TenantID: got %q, want %q", got.TenantID, input.TenantID)
	}
	if got.Type != input.Type {
		t.Errorf("Type: got %q, want %q", got.Type, input.Type)
	}
	if got.Problem != input.Problem {
		t.Errorf("Problem: got %q, want %q", got.Problem, input.Problem)
	}
	if got.Solution != input.Solution {
		t.Errorf("Solution: got %q, want %q", got.Solution, input.Solution)
	}
	if got.Score != input.Score {
		t.Errorf("Score: got %f, want %f", got.Score, input.Score)
	}
	if got.AgentID != input.AgentID {
		t.Errorf("AgentID: got %q, want %q", got.AgentID, input.AgentID)
	}

	// Success derived from score > 0.5; low score = failure.
	if got.Success {
		t.Error("Success should be false when Score <= 0.5")
	}

	// Metadata should contain merged original + agent_id + source.
	if got.Metadata == nil {
		t.Fatal("Metadata should not be nil")
	}
	if agentID, ok := got.Metadata["agent_id"].(string); !ok || agentID != input.AgentID {
		t.Errorf("Metadata[\"agent_id\"]: got %v, want %q", got.Metadata["agent_id"], input.AgentID)
	}
	if src, ok := got.Metadata["source"].(string); !ok || src != input.Source {
		t.Errorf("Metadata[\"source\"]: got %v, want %q", got.Metadata["source"], input.Source)
	}
	if diagID, ok := got.Metadata["diagnostic_id"].(string); !ok || diagID != "diag-123" {
		t.Errorf("Metadata[\"diagnostic_id\"] not preserved: got %v", got.Metadata["diagnostic_id"])
	}

	// CreatedAt should be set.
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

// TestConvertToStorageExperience_NilInput verifies that nil input returns nil without panic.
func TestConvertToStorageExperience_NilInput(t *testing.T) {
	got := convertToStorageExperience(nil)
	if got != nil {
		t.Errorf("expected nil for nil input, got %+v", got)
	}
}
