package evolution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"goagentx/internal/callbacks"
)

// --- Mock implementations for DreamCycle tests ---

// mockMutator implements MutatorInterface for testing.
type mockMutator struct {
	mutateFn func(ctx context.Context, parent Strategy, n int) ([]Strategy, error)
}

func (m *mockMutator) Mutate(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
	if m.mutateFn != nil {
		return m.mutateFn(ctx, parent, n)
	}
	return []Strategy{
		{ID: "candidate-1", Name: "MutatedV1", Version: 2, ParentID: parent.ID},
		{ID: "candidate-2", Name: "MutatedV2", Version: 2, ParentID: parent.ID},
	}, nil
}

// mockTester implements TesterInterface for testing.
type mockTester struct {
	results map[string]*RegressionResult // keyed by candidate ID
	err     error
}

func (m *mockTester) Run(ctx context.Context, cfg RegressionConfig) (*RegressionResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	if result, ok := m.results[cfg.Candidate.ID]; ok {
		return result, nil
	}
	// Default: candidate loses (low win rate).
	return &RegressionResult{
		CandidateScore: 0.4,
		BaselineScore:  0.6,
		WinRate:        0.3,
		TotalTasks:     50,
	}, nil
}

// mockGenealogy implements GenealogyRecorder for testing.
type mockGenealogy struct {
	mu       sync.Mutex
	recorded []StrategyLineage
	err      error
}

func (m *mockGenealogy) Record(ctx context.Context, lineage StrategyLineage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.recorded = append(m.recorded, lineage)
	return nil
}

func (m *mockGenealogy) RecordCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.recorded)
}

// --- Tests ---

// TestNewDreamCycle_ValidArgs tests constructor with valid arguments.
func TestNewDreamCycle_ValidArgs(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil)
	mutator := &mockMutator{}
	tester := &mockTester{}

	dc, err := NewDreamCycle(scheduler, mutator, tester, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if dc == nil {
		t.Fatal("expected non-nil dream cycle")
	}
	if dc.config.Enabled {
		t.Error("expected disabled by default")
	}
	if dc.config.MinTasksBeforeEvolve != 10 {
		t.Errorf("expected MinTasksBeforeEvolve=10, got %d", dc.config.MinTasksBeforeEvolve)
	}
	if dc.config.MaxMutations != 3 {
		t.Errorf("expected MaxMutations=3, got %d", dc.config.MaxMutations)
	}
}

// TestNewDreamCycle_NilScheduler tests that nil scheduler returns error.
func TestNewDreamCycle_NilScheduler(t *testing.T) {
	mutator := &mockMutator{}
	tester := &mockTester{}

	_, err := NewDreamCycle(nil, mutator, tester, nil)
	if err == nil {
		t.Fatal("expected error for nil scheduler")
	}
}

// TestNewDreamCycle_NilMutator tests that nil mutator returns error.
func TestNewDreamCycle_NilMutator(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil)
	tester := &mockTester{}

	_, err := NewDreamCycle(scheduler, nil, tester, nil)
	if err == nil {
		t.Fatal("expected error for nil mutator")
	}
}

// TestNewDreamCycle_NilTester tests that nil tester returns error.
func TestNewDreamCycle_NilTester(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil)
	mutator := &mockMutator{}

	_, err := NewDreamCycle(scheduler, mutator, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil tester")
	}
}

// TestRun_DisabledConfig tests that Run returns immediately when disabled.
func TestRun_DisabledConfig(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{}
	tester := &mockTester{}
	genealogy := &mockGenealogy{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(DreamCycleConfig{Enabled: false}),
	)

	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("expected no error when disabled, got %v", err)
	}
	// Task count is always incremented regardless of enabled state.
	if dc.taskCount != 1 {
		t.Errorf("expected task count 1 when disabled, got %d", dc.taskCount)
	}
}

// TestRun_FewTasks tests that evolution is skipped when task count is below threshold.
func TestRun_FewTasks(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{}
	tester := &mockTester{}
	genealogy := &mockGenealogy{}

	cfg := DefaultDreamCycleConfig()
	cfg.Enabled = true
	cfg.MinTasksBeforeEvolve = 5
	cfg.Cooldown = time.Nanosecond

	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(cfg),
	)

	// Run 4 times (below threshold of 5).
	for i := 0; i < 4; i++ {
		err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", i, err)
		}
	}

	if genealogy.RecordCount() != 0 {
		t.Errorf("expected 0 lineage records below threshold, got %d", genealogy.RecordCount())
	}
	if dc.taskCount != 4 {
		t.Errorf("expected task count 4, got %d", dc.taskCount)
	}
}

// TestRun_FullCycleHappyPath tests a complete dream cycle with winning candidate.
func TestRun_FullCycleHappyPath(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutateCalled := false
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
			mutateCalled = true
			return []Strategy{
				{ID: "winner-cand", Name: "WinnerV1", Version: 2, ParentID: parent.ID},
			}, nil
		},
	}
	tester := &mockTester{
		results: map[string]*RegressionResult{
			"winner-cand": {
				CandidateScore: 0.85,
				BaselineScore:  0.60,
				WinRate:        0.80,
				TotalTasks:     50,
			},
		},
	}
	genealogy := &mockGenealogy{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			MaxMutations:         3,
			MinWinRate:           0.55,
			Cooldown:             time.Nanosecond,
		}),
	)

	// Pre-warm to pass threshold.
	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)

	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mutateCalled {
		t.Error("expected mutator to be called")
	}
	if genealogy.RecordCount() != 1 {
		t.Errorf("expected 1 lineage record, got %d", genealogy.RecordCount())
	}
	if dc.lastCycle.IsZero() {
		t.Error("expected lastCycle to be set after successful run")
	}
}

// TestRun_AllCandidatesFail tests that no lineage is recorded when all candidates fail arena test.
func TestRun_AllCandidatesFail(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
			return []Strategy{
				{ID: "loser-1", Name: "LoserV1", Version: 2},
				{ID: "loser-2", Name: "LoserV2", Version: 2},
			}, nil
		},
	}
	// All candidates have low win rate (below threshold).
	tester := &mockTester{
		results: map[string]*RegressionResult{
			"loser-1": {CandidateScore: 0.3, BaselineScore: 0.6, WinRate: 0.20, TotalTasks: 50},
			"loser-2": {CandidateScore: 0.35, BaselineScore: 0.6, WinRate: 0.25, TotalTasks: 50},
		},
	}
	genealogy := &mockGenealogy{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			MinWinRate:           0.55,
			Cooldown:             time.Nanosecond,
		}),
	)

	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)

	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if genealogy.RecordCount() != 0 {
		t.Errorf("expected 0 lineage records when all candidates fail, got %d", genealogy.RecordCount())
	}
}

// TestRun_OneCandidateWin tests that one winning candidate records genealogy correctly.
func TestRun_OneCandidateWins(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
			return []Strategy{
				{ID: "cand-a", Name: "CandA", Version: 2},
				{ID: "cand-b", Name: "CandB", Version: 2},
			}, nil
		},
	}
	tester := &mockTester{
		results: map[string]*RegressionResult{
			"cand-a": {CandidateScore: 0.70, BaselineScore: 0.60, WinRate: 0.60, TotalTasks: 50},
			"cand-b": {CandidateScore: 0.90, BaselineScore: 0.60, WinRate: 0.75, TotalTasks: 50},
		},
	}
	genealogy := &mockGenealogy{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			MinWinRate:           0.55,
			Cooldown:             time.Nanosecond,
		}),
	)

	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)

	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if genealogy.RecordCount() != 1 {
		t.Fatalf("expected 1 lineage record, got %d", genealogy.RecordCount())
	}

	record := genealogy.recorded[0]
	if record.ChildID != "cand-b" {
		t.Errorf("expected winner cand-b, got %s", record.ChildID)
	}
	if record.WinRate != 0.75 {
		t.Errorf("expected win rate 0.75, got %.2f", record.WinRate)
	}
}

// TestRun_MutatorError tests that mutation errors are propagated.
func TestRun_MutatorError(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
			return nil, errors.New("mutation resource exhausted")
		},
	}
	tester := &mockTester{}
	genealogy := &mockGenealogy{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			Cooldown:             time.Nanosecond,
		}),
	)

	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)

	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected error from failed mutation")
	}
}

// TestRun_GenealogyNil tests that nil genealogy does not cause panic.
func TestRun_GenealogyNil(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
			return []Strategy{
				{ID: "win-no-gene", Name: "WinNoGene", Version: 2},
			}, nil
		},
	}
	tester := &mockTester{
		results: map[string]*RegressionResult{
			"win-no-gene": {CandidateScore: 0.80, BaselineScore: 0.60, WinRate: 0.70, TotalTasks: 50},
		},
	}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, nil, // nil genealogy
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			MinWinRate:           0.55,
			Cooldown:             time.Nanosecond,
		}),
	)

	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)

	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("unexpected error with nil genealogy: %v", err)
	}
}

// TestRun_CooldownActive tests that cooldown blocks consecutive cycles.
func TestRun_CooldownActive(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{}
	tester := &mockTester{
		results: map[string]*RegressionResult{
			"candidate-1": {CandidateScore: 0.80, BaselineScore: 0.60, WinRate: 0.70, TotalTasks: 50},
		},
	}
	genealogy := &mockGenealogy{}

	longCooldown := 1 * time.Hour
	dc, _ := NewDreamCycle(scheduler, mutator, tester, genealogy,
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			Cooldown:             longCooldown,
		}),
	)

	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)

	// First run should succeed and set lastCycle.
	err := dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}

	firstRecordCount := genealogy.RecordCount()

	// Second run should be blocked by cooldown.
	err = dc.Run(context.Background(), CallbackData{AgentID: "agent-2"})
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}

	if genealogy.RecordCount() != firstRecordCount {
		t.Errorf("expected no new records during cooldown, got total %d", genealogy.RecordCount())
	}
}

// TestShouldEvolve_NotEnoughTasks tests shouldEvolve with insufficient tasks.
func TestShouldEvolve_NotEnoughTasks(t *testing.T) {
	reg := newMockCallbackRegistrarForTest()
	adapter := newMockAdapterForScheduler()

	scheduler := NewEvolutionScheduler(reg, adapter,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)

	// With TriggerOnIdle and interval passed, should return true by default.
	result := scheduler.shouldEvolve(context.Background(), CallbackData{AgentID: "agent-1"})
	if !result {
		t.Error("expected shouldEvolve=true with idle trigger and interval passed")
	}
}

// TestShouldEvolve_TriggerOnDemand tests that demand trigger never auto-evolves.
func TestShouldEvolve_TriggerOnDemand(t *testing.T) {
	reg := newMockCallbackRegistrarForTest()
	adapter := newMockAdapterForScheduler()

	scheduler := NewEvolutionScheduler(reg, adapter,
		WithEnabled(true),
		WithTrigger(TriggerOnDemand),
		WithMinInterval(time.Nanosecond),
	)

	result := scheduler.shouldEvolve(context.Background(), CallbackData{AgentID: "agent-1"})
	if result {
		t.Error("expected shouldEvolve=false with TriggerOnDemand")
	}
}

// TestSetDreamCycle_Getter tests SetDreamCycle and DreamCycle methods.
func TestSetDreamCycle_Getter(t *testing.T) {
	reg := newMockCallbackRegistrarForTest()
	adapter := newMockAdapterForScheduler()

	scheduler := NewEvolutionScheduler(reg, adapter)

	if scheduler.DreamCycle() != nil {
		t.Error("expected nil dream cycle initially")
	}

	mutator := &mockMutator{}
	tester := &mockTester{}
	dc, _ := NewDreamCycle(scheduler, mutator, tester, nil)

	scheduler.SetDreamCycle(dc)

	if scheduler.DreamCycle() == nil {
		t.Error("expected non-nil dream cycle after SetDreamCycle")
	}
	if scheduler.DreamCycle() != dc {
		t.Error("dream cycle mismatch")
	}
}

// TestDefaultDreamCycleConfig tests default configuration values.
func TestDefaultDreamCycleConfig(t *testing.T) {
	cfg := DefaultDreamCycleConfig()

	if cfg.Enabled {
		t.Error("expected Enabled=false by default")
	}
	if cfg.MinTasksBeforeEvolve != 10 {
		t.Errorf("expected MinTasksBeforeEvolve=10, got %d", cfg.MinTasksBeforeEvolve)
	}
	if cfg.MinScoreDrop != 0.15 {
		t.Errorf("expected MinScoreDrop=0.15, got %f", cfg.MinScoreDrop)
	}
	if cfg.MaxMutations != 3 {
		t.Errorf("expected MaxMutations=3, got %d", cfg.MaxMutations)
	}
	if cfg.MinWinRate != 0.55 {
		t.Errorf("expected MinWinRate=0.55, got %f", cfg.MinWinRate)
	}
	if cfg.Cooldown != 5*time.Minute {
		t.Errorf("expected Cooldown=5m, got %v", cfg.Cooldown)
	}
}

// TestSetEnabled_IsEnabled_DreamCycle tests enable/disable on dream cycle.
func TestSetEnabled_IsEnabled_DreamCycle(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil)
	mutator := &mockMutator{}
	tester := &mockTester{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, nil)

	if dc.IsEnabled() {
		t.Error("expected disabled by default")
	}

	dc.SetEnabled(true)
	if !dc.IsEnabled() {
		t.Error("expected enabled after SetEnabled(true)")
	}

	dc.SetEnabled(false)
	if dc.IsEnabled() {
		t.Error("expected disabled after SetEnabled(false)")
	}
}

// TestTaskCount tests task counter behavior.
func TestTaskCount(t *testing.T) {
	scheduler := NewEvolutionScheduler(nil, nil)
	mutator := &mockMutator{}
	tester := &mockTester{}

	dc, _ := NewDreamCycle(scheduler, mutator, tester, nil,
		WithDreamCycleConfig(DreamCycleConfig{Enabled: false}),
	)

	if dc.TaskCount() != 0 {
		t.Errorf("expected initial task count 0, got %d", dc.TaskCount())
	}

	// Each Run call increments counter even when disabled.
	for i := 0; i < 5; i++ {
		_ = dc.Run(context.Background(), CallbackData{AgentID: "agent-1"})
	}

	if dc.TaskCount() != 5 {
		t.Errorf("expected task count 5, got %d", dc.TaskCount())
	}
}

// --- Helper: mock CallbackRegistrar for scheduler tests ---

type mockCallbackRegistrarForTest struct {
	handlers map[callbacks.Event][]callbacks.Handler
}

func newMockCallbackRegistrarForTest() *mockCallbackRegistrarForTest {
	return &mockCallbackRegistrarForTest{
		handlers: make(map[callbacks.Event][]callbacks.Handler),
	}
}

func (r *mockCallbackRegistrarForTest) On(event callbacks.Event, handler callbacks.Handler) {
	r.handlers[event] = append(r.handlers[event], handler)
}

func (r *mockCallbackRegistrarForTest) Count(event callbacks.Event) int {
	return len(r.handlers[event])
}
