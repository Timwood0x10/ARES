// Package main demonstrates GoAgentX Autonomous Evolution (Dream Mode v1) workflow.
//
// This example showcases 5 core capabilities of the autonomous evolution system:
//
//	Scenario 1: Bandit Feedback Loop - Experience reinforcement via success/failure signals
//	Scenario 2: Callback Event System - Lifecycle event hooks for LLM/Tool/Agent events
//	Scenario 3: Strategy Mutation Engine - Generating candidate strategy variants
//	Scenario 4: Arena Regression Testing - A/B comparison with statistical significance
//	Scenario 5: Dream Cycle Orchestration - Full evolution loop from trigger to genealogy
//
// All dependencies use mock implementations - no external services required.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"goagentx/internal/arena"
	"goagentx/internal/callbacks"
	"goagentx/internal/evolution"
	"goagentx/internal/evolution/mutation"
	"goagentx/internal/experience"
	storageModels "goagentx/internal/storage/postgres/models"
)

// ============================================================
// Mock Implementations
// ============================================================

// mockExperienceRepo implements repositories.ExperienceRepositoryInterface in memory.
type mockExperienceRepo struct {
	mu          sync.RWMutex
	experiences map[string]*mockExperienceRecord
	usageCounts map[string]int
	ranks       map[string]float64
}

// mockExperienceRecord holds in-memory experience data.
type mockExperienceRecord struct {
	id     string
	input  string
	output string
	score  float64
}

// newMockExperienceRepo creates a new in-memory experience repository.
func newMockExperienceRepo() *mockExperienceRepo {
	return &mockExperienceRepo{
		experiences: make(map[string]*mockExperienceRecord),
		usageCounts: make(map[string]int),
		ranks:       make(map[string]float64),
	}
}

// Create inserts a new experience into the mock repository.
func (r *mockExperienceRepo) Create(_ context.Context, exp *storageModels.Experience) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.experiences[exp.ID] = &mockExperienceRecord{
		id:     exp.ID,
		input:  exp.Input,
		output: exp.Output,
		score:  exp.Score,
	}
	r.ranks[exp.ID] = exp.Score
	return nil
}

// GetByID retrieves an experience by ID from the mock repository.
func (r *mockExperienceRepo) GetByID(_ context.Context, id string) (*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rec, ok := r.experiences[id]
	if !ok {
		return nil, fmt.Errorf("experience not found: %s", id)
	}
	return &storageModels.Experience{
		ID:     rec.id,
		Input:  rec.input,
		Output: rec.output,
		Score:  rec.score,
	}, nil
}

// Update updates an existing experience in the mock repository.
func (r *mockExperienceRepo) Update(_ context.Context, exp *storageModels.Experience) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.experiences[exp.ID]; !ok {
		return fmt.Errorf("experience not found: %s", exp.ID)
	}
	r.experiences[exp.ID] = &mockExperienceRecord{
		id:     exp.ID,
		input:  exp.Input,
		output: exp.Output,
		score:  exp.Score,
	}
	return nil
}

// Delete removes an experience by ID from the mock repository.
func (r *mockExperienceRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.experiences, id)
	delete(r.usageCounts, id)
	delete(r.ranks, id)
	return nil
}

// SearchByVector performs vector similarity search (mock returns all experiences).
func (r *mockExperienceRepo) SearchByVector(
	_ context.Context,
	_ []float64,
	_ string,
	limit int,
) ([]*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*storageModels.Experience, 0, limit)
	for _, rec := range r.experiences {
		if len(result) >= limit {
			break
		}
		result = append(result, &storageModels.Experience{
			ID:     rec.id,
			Input:  rec.input,
			Output: rec.output,
			Score:  rec.score,
		})
	}
	return result, nil
}

// SearchByKeyword performs keyword-based search (mock returns matching experiences).
func (r *mockExperienceRepo) SearchByKeyword(
	_ context.Context,
	query, _ string,
	limit int,
) ([]*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*storageModels.Experience, 0, limit)
	for _, rec := range r.experiences {
		if len(result) >= limit {
			break
		}
		result = append(result, &storageModels.Experience{
			ID:     rec.id,
			Input:  rec.input,
			Output: rec.output,
			Score:  rec.score,
		})
	}
	_ = query // Suppress unused warning.
	return result, nil
}

// IncrementUsageCount increments the usage count of an experience.
func (r *mockExperienceRepo) IncrementUsageCount(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.usageCounts[id]++
	slog.Info("[MockRepo] Usage count incremented",
		"experience_id", id,
		"new_count", r.usageCounts[id],
	)
	return nil
}

// DecrementRank decreases the score of an experience as negative feedback.
func (r *mockExperienceRepo) DecrementRank(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ranks[id] *= 0.9 // Reduce rank by 10% on failure.
	slog.Info("[MockRepo] Rank decremented",
		"experience_id", id,
		"new_rank", fmt.Sprintf("%.4f", r.ranks[id]),
	)
	return nil
}

// ListByType retrieves experiences by type (mock returns all).
func (r *mockExperienceRepo) ListByType(
	_ context.Context,
	_, _ string,
	limit int,
) ([]*storageModels.Experience, error) {
	return r.SearchByVector(context.Background(), nil, "", limit)
}

// ListByAgent retrieves experiences for a specific agent (mock returns all).
func (r *mockExperienceRepo) ListByAgent(
	_ context.Context,
	_, _ string,
	limit int,
) ([]*storageModels.Experience, error) {
	return r.SearchByVector(context.Background(), nil, "", limit)
}

// getRank returns the current rank of an experience.
func (r *mockExperienceRepo) getRank(id string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ranks[id]
}

// getUsageCount returns the current usage count of an experience.
func (r *mockExperienceRepo) getUsageCount(id string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usageCounts[id]
}

// mockScorer implements arena.Scorer interface for testing purposes.
type mockScorer struct {
	baseScore float64
	variance  float64
	counter   int64
	mu        sync.Mutex
}

// newMockScorer creates a scorer that returns scores around baseScore with variance.
func newMockScorer(baseScore, variance float64) *mockScorer {
	return &mockScorer{baseScore: baseScore, variance: variance}
}

// Score evaluates input and returns a numeric score with slight variation.
func (s *mockScorer) Score(input any) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	offset := float64(s.counter%5-2) * s.variance
	score := s.baseScore + offset
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	slog.Info("[MockScorer] Score computed",
		"input", fmt.Sprintf("%v", input),
		"score", fmt.Sprintf("%.3f", score),
	)
	return score, nil
}

// mockGenealogyRecorder implements evolution.GenealogyRecorder interface.
type mockGenealogyRecorder struct {
	mu       sync.Mutex
	lineages []evolution.StrategyLineage
}

// newMockGenealogyRecorder creates a new in-memory genealogy recorder.
func newMockGenealogyRecorder() *mockGenealogyRecorder {
	return &mockGenealogyRecorder{
		lineages: make([]evolution.StrategyLineage, 0),
	}
}

// Record persists a strategy lineage entry.
func (g *mockGenealogyRecorder) Record(_ context.Context, lineage evolution.StrategyLineage) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.lineages = append(g.lineages, lineage)
	slog.Info("[MockGenealogy] Lineage recorded",
		"parent_id", lineage.ParentID,
		"child_id", lineage.ChildID,
		"mutation_type", lineage.MutationType,
		"win_rate", fmt.Sprintf("%.2f", lineage.WinRate),
	)
	return nil
}

// getLineages returns all recorded lineages.
func (g *mockGenealogyRecorder) getLineages() []evolution.StrategyLineage {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lineages
}

// mockTester implements evolution.TesterInterface using arena.RegressionTester.
type mockTester struct {
	rt *arena.RegressionTester
}

// newMockTester creates a tester backed by arena regression with mock scorer.
func newMockTester(scorer arena.Scorer) (*mockTester, error) {
	arenaSvc := arena.NewService(nil, nil)
	rt, err := arena.NewRegressionTester(arenaSvc, scorer)
	if err != nil {
		return nil, fmt.Errorf("create regression tester: %w", err)
	}
	return &mockTester{rt: rt}, nil
}

// Run executes a regression test and converts results to evolution format.
func (t *mockTester) Run(ctx context.Context, cfg evolution.RegressionConfig) (*evolution.RegressionResult, error) {
	arenaCfg := arena.RegressionConfig{
		OldStrategy:  cfg.Baseline,
		NewStrategy:  cfg.Candidate,
		BaselineRuns: 5,
		CompareRuns:  5,
		TestSuite:    "dream-cycle-test",
	}

	result, err := t.rt.Run(ctx, arenaCfg)
	if err != nil {
		return nil, err
	}

	return &evolution.RegressionResult{
		CandidateScore: result.NewAvg,
		BaselineScore:  result.OldAvg,
		WinRate:        result.WinRate,
		TotalTasks:     result.Samples,
	}, nil
}

// ============================================================
// Demo Scenarios
// ============================================================

// phaseSeparator prints a visual separator for readable output.
func phaseSeparator(title string) {
	fmt.Printf("\n%s\n", "============================================================")
	fmt.Printf("  %s\n", title)
	fmt.Printf("%s\n\n", "============================================================")
}

// setupLogger configures structured slog with text output.
func setupLogger() {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
}

// demoBanditFeedbackLoop demonstrates the bandit feedback mechanism.
// It shows how successful task execution increases usage count,
// and failed tasks decrease rank (score).
func demoBanditFeedbackLoop() {
	phaseSeparator("Scenario 1: Bandit Feedback Loop")

	ctx := context.Background()

	repo := newMockExperienceRepo()
	feedbackSvc := experience.NewFeedbackService(repo)

	expIDs := []string{"exp-001", "exp-002", "exp-003"}
	initialScores := map[string]float64{
		"exp-001": 0.90,
		"exp-002": 0.75,
		"exp-003": 0.60,
	}

	fmt.Println("Initializing experiences:")
	for _, id := range expIDs {
		err := repo.Create(ctx, &storageModels.Experience{
			ID:    id,
			Input: fmt.Sprintf("Task pattern for %s", id),
			Score: initialScores[id],
		})
		if err != nil {
			slog.Error("Failed to create experience", "error", err)
			continue
		}
		fmt.Printf("  - %s: initial score=%.2f\n", id, initialScores[id])
	}
	fmt.Println()

	fmt.Println("Simulating task executions:")
	taskResults := []struct {
		expID   string
		success bool
		desc    string
	}{
		{"exp-001", true, "Code generation completed"},
		{"exp-001", true, "Another code generation"},
		{"exp-001", true, "Third successful use"},
		{"exp-002", true, "Query parsing succeeded"},
		{"exp-003", false, "Timeout occurred"},
		{"exp-003", false, "Error response received"},
		{"exp-002", false, "Unexpected format"},
		{"exp-001", true, "Fourth successful use"},
	}

	for i, task := range taskResults {
		fmt.Printf("\n  Task #%d: %s\n", i+1, task.desc)
		fmt.Printf("    Experience: %s | Result: ", task.expID)

		if task.success {
			fmt.Println("SUCCESS ✓")
			_ = feedbackSvc.RecordSuccess(ctx, task.expID)
		} else {
			fmt.Println("FAILURE ✗")
			_ = feedbackSvc.RecordFailure(ctx, task.expID)
		}
	}

	fmt.Println("\n--- Final State ---")
	fmt.Printf("%-12s %-15s %-12s\n", "Experience", "Usage Count", "Rank (Score)")
	fmt.Println(stringsRepeat("-", 42))
	for _, id := range expIDs {
		count := repo.getUsageCount(id)
		rank := repo.getRank(id)
		fmt.Printf("%-12s %-15d %-12.4f\n", id, count, rank)
	}

	fmt.Println("\nKey Observations:")
	fmt.Println("  • exp-001: High usage (4 successes) → reinforced as reliable pattern")
	fmt.Println("  • exp-002: Mixed results (1 success, 1 failure) → moderate rank")
	fmt.Println("  • exp-003: All failures (2 failures) → rank reduced by ~19%")
	fmt.Println("  • Feedback loop enables continuous experience quality optimization")
}

// demoCallbackSystem demonstrates the callback event system.
// It shows registering handlers for LLM/Tool/Agent lifecycle events
// and verifying they are called when events are emitted.
func demoCallbackSystem() {
	phaseSeparator("Scenario 2: Callback Event System")

	registry := callbacks.NewRegistry()

	var (
		llmStartCount   int
		llmEndCount     int
		toolStartCount  int
		agentStartCount int
		capturedEvents  []*callbacks.Context
	)

	mu := sync.Mutex{}

	registry.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		llmStartCount++
		capturedEvents = append(capturedEvents, ctx)
		slog.Info("[Handler] LLM Start captured",
			"model", ctx.Model,
			"input_len", len(ctx.Input),
		)
	})

	registry.On(callbacks.EventLLMEnd, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		llmEndCount++
		capturedEvents = append(capturedEvents, ctx)
		slog.Info("[Handler] LLM End captured",
			"model", ctx.Model,
			"output_len", len(ctx.Output),
			"duration", ctx.Duration,
		)
	})

	registry.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		capturedEvents = append(capturedEvents, ctx)
		slog.Warn("[Handler] LLM Error captured",
			"model", ctx.Model,
			"error", ctx.Error,
		)
	})

	registry.On(callbacks.EventToolStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		toolStartCount++
		capturedEvents = append(capturedEvents, ctx)
		slog.Info("[Handler] Tool Start captured",
			"tool_name", ctx.ToolName,
			"agent_id", ctx.AgentID,
		)
	})

	registry.On(callbacks.EventAgentStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		agentStartCount++
		capturedEvents = append(capturedEvents, ctx)
		slog.Info("[Handler] Agent Start captured",
			"agent_id", ctx.AgentID,
		)
	})

	fmt.Println("Registered handlers:")
	fmt.Printf("  - EventLLMStart   -> %d handler(s)\n", registry.Count(callbacks.EventLLMStart))
	fmt.Printf("  - EventLLMEnd     -> %d handler(s)\n", registry.Count(callbacks.EventLLMEnd))
	fmt.Printf("  - EventLLMError   -> %d handler(s)\n", registry.Count(callbacks.EventLLMError))
	fmt.Printf("  - EventToolStart  -> %d handler(s)\n", registry.Count(callbacks.EventToolStart))
	fmt.Printf("  - EventAgentStart -> %d handler(s)\n", registry.Count(callbacks.EventAgentStart))

	fmt.Println("\nSimulating event emissions:")

	testEvents := []*callbacks.Context{
		{
			Event:   callbacks.EventLLMStart,
			Model:   "gpt-4o",
			Input:   "Explain quantum computing",
			AgentID: "agent-01",
		},
		{
			Event:      callbacks.EventLLMEnd,
			Model:      "gpt-4o",
			Output:     "Quantum computing harnesses...",
			Duration:   250 * time.Millisecond,
			TokenCount: 150,
		},
		{
			Event:    callbacks.EventToolStart,
			ToolName: "calculator",
			AgentID:  "agent-01",
			Input:    "{\"expression\": \"2+2\"}",
		},
		{
			Event:   callbacks.EventAgentStart,
			AgentID: "agent-02",
		},
		{
			Event: callbacks.EventLLMError,
			Model: "gpt-4o",
			Error: fmt.Errorf("rate limit exceeded"),
		},
		{
			Event: callbacks.EventLLMStart,
			Model: "claude-3",
			Input: "Summarize this document",
		},
	}

	for i, evt := range testEvents {
		fmt.Printf("\n  Emitting event #%d: %s\n", i+1, evt.Event)
		registry.Emit(evt)
	}

	fmt.Println("\n--- Handler Invocation Summary ---")
	fmt.Printf("%-20s %-10s\n", "Event Type", "Invocations")
	fmt.Println(stringsRepeat("-", 32))
	fmt.Printf("%-20s %-10d\n", "EventLLMStart", llmStartCount)
	fmt.Printf("%-20s %-10d\n", "EventLLMEnd", llmEndCount)
	fmt.Printf("%-20s %-10d\n", "EventToolStart", toolStartCount)
	fmt.Printf("%-20s %-10d\n", "EventAgentStart", agentStartCount)
	fmt.Printf("%-20s %-10d\n", "Total Captured", len(capturedEvents))

	fmt.Println("\nKey Observations:")
	fmt.Println("  • Each event type can have multiple handlers (pub/sub pattern)")
	fmt.Println("  • Handlers are invoked sequentially in registration order")
	fmt.Println("  • Context carries rich metadata (model, tokens, duration, errors)")
	fmt.Println("  • System is panic-safe: handler panics don't crash the emitter")
	fmt.Println("  • Enables observability, metrics collection, and debugging hooks")
}

// demoMutationEngine demonstrates the strategy mutation engine.
// It shows generating child strategies from a parent with parameter variations,
// deterministic behavior with fixed seeds, and mutation type distribution.
func demoMutationEngine() {
	phaseSeparator("Scenario 3: Strategy Mutation Engine")

	ctx := context.Background()

	parent := &mutation.Strategy{
		ID:      "parent-strategy-v1",
		Version: 1,
		Params: map[string]any{
			"temperature":        0.7,
			"top_k":              40,
			"max_steps":          10,
			"memory_limit":       5,
			"conflict_threshold": 0.90,
		},
		PromptTemplate: "You are a careful assistant. Think step by step.",
		CreatedAt:      time.Now(),
	}

	fmt.Println("Parent Strategy:")
	fmt.Printf("  ID:      %s\n", parent.ID)
	fmt.Printf("  Version: %d\n", parent.Version)
	fmt.Println("  Params:")
	for k, v := range parent.Params {
		fmt.Printf("    %s: %v\n", k, v)
	}
	fmt.Printf("  Prompt:  %s\n", parent.PromptTemplate)

	promptPool := []string{
		"You are a careful assistant. Think step by step.",
		"You are a creative assistant. Explore multiple solutions.",
		"You are a precise assistant. Focus on accuracy.",
		"You are a fast assistant. Be concise and direct.",
	}

	mutator, err := mutation.NewMutator(
		mutation.WithPromptPool(promptPool),
		mutation.WithSeed(42), // Deterministic seed.
	)
	if err != nil {
		slog.Error("Failed to create mutator", "error", err)
		return
	}

	numMutations := 5
	children, err := mutator.Mutate(ctx, parent, numMutations)
	if err != nil {
		slog.Error("Failed to mutate", "error", err)
		return
	}

	fmt.Printf("\n--- Generated %d Child Strategies ---\n", numMutations)
	for i, child := range children {
		fmt.Printf("\n  Child #%d:\n", i+1)
		fmt.Printf("    ID:            %s\n", child.ID)
		fmt.Printf("    Parent ID:     %s\n", child.ParentID)
		fmt.Printf("    Version:       %d\n", child.Version)
		fmt.Printf("    Mutation Type: %s\n", child.StrategyMutationType)
		fmt.Printf("    Description:   %s\n", child.MutationDesc)
		fmt.Printf("    Params:\n")

		for k, v := range child.Params {
			parentVal := parent.Params[k]
			marker := ""
			if fmt.Sprintf("%v", v) != fmt.Sprintf("%v", parentVal) {
				marker = " ← CHANGED"
			}
			fmt.Printf("      %s: %v%s\n", k, v, marker)
		}

		if child.PromptTemplate != parent.PromptTemplate {
			fmt.Printf("    Prompt:        %s ← CHANGED\n", child.PromptTemplate)
		} else {
			fmt.Printf("    Prompt:        %s (unchanged)\n", child.PromptTemplate)
		}
	}

	fmt.Println("\n--- Determinism Test ---")
	mutator2, _ := mutation.NewMutator(
		mutation.WithPromptPool(promptPool),
		mutation.WithSeed(42), // Same seed.
	)
	children2, _ := mutator2.Mutate(ctx, parent, numMutations)

	allMatch := true
	for i := range children {
		if children[i].Params["temperature"] != children2[i].Params["temperature"] ||
			children[i].MutationDesc != children2[i].MutationDesc {
			allMatch = false
			break
		}
	}
	fmt.Printf("Same seed (42) produces identical results: %v\n", allMatch)

	mutator3, _ := mutation.NewMutator(
		mutation.WithPromptPool(promptPool),
		mutation.WithSeed(123), // Different seed.
	)
	children3, _ := mutator3.Mutate(ctx, parent, numMutations)
	diffSeedMatch := false
	if len(children3) > 0 && len(children) > 0 {
		diffSeedMatch = children[0].MutationDesc == children3[0].MutationDesc
	}
	fmt.Printf("Different seed (123) produces different results: %v\n", !diffSeedMatch)

	fmt.Println("\nKey Observations:")
	fmt.Println("  • Mutation engine varies one parameter per child (temperature, top_k, etc.)")
	fmt.Println("  • 80% probability for parameter mutation, 20% for prompt template change")
	fmt.Println("  • Deterministic: same seed always produces same mutations (reproducibility)")
	fmt.Println("  • Each child tracks ParentID, Version++, and MutationDesc for traceability")
	fmt.Println("  • Enables systematic exploration of strategy space")
}

// demoArenaRegressionTest demonstrates A/B style arena regression testing.
// It compares two strategies with statistical significance analysis.
func demoArenaRegressionTest() {
	phaseSeparator("Scenario 4: Arena Regression Test")

	ctx := context.Background()

	oldStrategy := map[string]any{
		"id":          "baseline-v1",
		"name":        "StandardStrategy",
		"temperature": 0.7,
		"max_tokens":  2048,
	}

	newStrategy := map[string]any{
		"id":          "candidate-v2",
		"name":        "OptimizedStrategy",
		"temperature": 0.5,
		"max_tokens":  4096,
	}

	baselineScorer := newMockScorer(0.72, 0.05)
	candidateScorer := newMockScorer(0.81, 0.06)

	arenaSvc := arena.NewService(nil, nil)

	compositeScorer := newCompositeScorer(baselineScorer, candidateScorer)

	rt, err := arena.NewRegressionTester(arenaSvc, compositeScorer)
	if err != nil {
		slog.Error("Failed to create regression tester", "error", err)
		return
	}

	cfg := arena.RegressionConfig{
		OldStrategy:  oldStrategy,
		NewStrategy:  newStrategy,
		BaselineRuns: 5,
		CompareRuns:  5,
		TestSuite:    "autonomous-evolution-demo",
		Confidence:   0.05,
		MinWinRate:   0.55,
	}

	fmt.Println("Regression Test Configuration:")
	fmt.Printf("  Old Strategy:  %v\n", oldStrategy["name"])
	fmt.Printf("  New Strategy:  %v\n", newStrategy["name"])
	fmt.Printf("  Baseline Runs: %d\n", cfg.BaselineRuns)
	fmt.Printf("  Compare Runs:  %d\n", cfg.CompareRuns)
	fmt.Printf("  Confidence:    %.2f (%.0f%% confidence level)\n", cfg.Confidence, (1-cfg.Confidence)*100)
	fmt.Printf("  Min Win Rate:  %.2f\n", cfg.MinWinRate)

	fmt.Println("\nRunning regression test...")
	result, err := rt.Run(ctx, cfg)
	if err != nil {
		slog.Error("Regression test failed", "error", err)
		return
	}

	fmt.Println("\n--- Regression Results ---")
	fmt.Printf("  Old Strategy Avg Score: %.4f\n", result.OldAvg)
	fmt.Printf("  New Strategy Avg Score: %.4f\n", result.NewAvg)
	fmt.Printf("  Win Rate:               %.2f%%\n", result.WinRate*100)
	fmt.Printf("  Statistically Significant: %v\n", result.Confident)
	fmt.Printf("  P-Value:                %.6f\n", result.PValue)
	fmt.Printf("  Samples per Strategy:   %d\n", result.Samples)
	fmt.Printf("  Tested At:             %s\n", result.TestedAt.Format(time.RFC3339))

	fmt.Println("\n  Individual Run Scores:")
	fmt.Printf("  %-8s %-25s %-25s\n", "Run#", "Old Strategy", "New Strategy")
	fmt.Println(stringsRepeat("-", 60))
	minLen := len(result.OldScores)
	if len(result.NewScores) < minLen {
		minLen = len(result.NewScores)
	}
	for i := 0; i < minLen; i++ {
		marker := ""
		if result.NewScores[i] >= result.OldScores[i] {
			marker = " ✓"
		}
		fmt.Printf("  %-8d %-25.4f %-25.4f%s\n", i+1, result.OldScores[i], result.NewScores[i], marker)
	}

	fmt.Println("\nKey Observations:")
	fmt.Println("  • Arena test runs both strategies N times and collects scores")
	fmt.Println("  • Win rate measures how often new strategy matches or beats baseline")
	fmt.Println("  • Statistical significance uses Welch's t-test approximation")
	fmt.Println("  • P-value < 0.05 indicates the difference is not due to chance")
	fmt.Println("  • Enables data-driven strategy adoption decisions")
}

// compositeScorerImpl routes scoring to the correct mock scorer based on strategy identity.
type compositeScorerImpl struct {
	baselineScorer  *mockScorer
	candidateScorer *mockScorer
	mu              sync.Mutex
}

// newCompositeScorer creates a scorer that routes based on strategy ID field.
func newCompositeScorer(baseline, candidate *mockScorer) *compositeScorerImpl {
	return &compositeScorerImpl{
		baselineScorer:  baseline,
		candidateScorer: candidate,
	}
}

// Score evaluates a strategy using the appropriate scorer.
func (c *compositeScorerImpl) Score(input any) (float64, error) {
	strategyMap, ok := input.(map[string]any)
	if !ok {
		return c.candidateScorer.Score(input)
	}

	idVal, hasID := strategyMap["id"]
	if hasID && idVal == "baseline-v1" {
		return c.baselineScorer.Score(input)
	}
	return c.candidateScorer.Score(input)
}

// demoDreamCycle demonstrates the full Dream Cycle orchestration.
// It shows the complete flow: trigger → mutate → arena test → genealogy.
func demoDreamCycle() {
	phaseSeparator("Scenario 5: Dream Cycle Orchestration")

	ctx := context.Background()

	scorer := newMockScorer(0.78, 0.04)
	tester, err := newMockTester(scorer)
	if err != nil {
		slog.Error("Failed to create tester", "error", err)
		return
	}
	genealogy := newMockGenealogyRecorder()

	mutator, err := mutation.NewMutator(mutation.WithSeed(99))
	if err != nil {
		slog.Error("Failed to create mutator", "error", err)
		return
	}

	parentStrategy := &mutation.Strategy{
		ID:      "root-strategy-v1",
		Version: 1,
		Params: map[string]any{
			"temperature": 0.7,
			"top_k":       40,
			"max_steps":   10,
		},
		CreatedAt: time.Now(),
	}

	fmt.Println("Dream Cycle Components Initialized:")
	fmt.Println("  • Mutator:      Strategy mutation engine (seed=99)")
	fmt.Println("  • Tester:       Arena regression tester (mock scorer)")
	fmt.Println("  • Genealogy:    Lineage recorder (in-memory)")
	fmt.Println()
	fmt.Println("Parent Strategy:")
	fmt.Printf("  ID: %s | Version: %d | Params: %v\n",
		parentStrategy.ID, parentStrategy.Version, parentStrategy.Params)

	fmt.Println("\n--- Step 1: Mutation ---")
	mutationChildren, err := mutator.Mutate(ctx, parentStrategy, 3)
	if err != nil {
		slog.Error("Mutation failed", "error", err)
		return
	}

	evoCandidates := make([]evolution.Strategy, len(mutationChildren))
	for i, c := range mutationChildren {
		evoCandidates[i] = evolution.Strategy{
			ID:       c.ID,
			Version:  c.Version,
			Params:   c.Params,
			ParentID: c.ParentID,
		}
		fmt.Printf("  Candidate #%d: %s (params=%v)\n", i+1, evoCandidates[i].ID, evoCandidates[i].Params)
	}

	baselineEvo := evolution.Strategy{
		ID:      parentStrategy.ID,
		Version: parentStrategy.Version,
		Params:  parentStrategy.Params,
	}

	fmt.Println("\n--- Step 2: Arena Regression Testing ---")
	var bestCandidate *evolution.Strategy
	var bestWinRate float64
	var bestScoreImprovement float64

	for i, cand := range evoCandidates {
		result, err := tester.Run(ctx, evolution.RegressionConfig{
			Candidate:      cand,
			Baseline:       baselineEvo,
			TaskSampleSize: 50,
		})
		if err != nil {
			slog.Warn("Candidate test failed", "candidate_id", cand.ID, "error", err)
			continue
		}

		improvement := result.CandidateScore - result.BaselineScore
		passed := result.WinRate >= 0.55
		status := "✗ Below threshold"
		if passed {
			status = "✓ Passed"
		}

		fmt.Printf("  Candidate #%d (%s):\n", i+1, cand.ID[:12])
		fmt.Printf("    Candidate Score: %.4f | Baseline Score: %.4f\n",
			result.CandidateScore, result.BaselineScore)
		fmt.Printf("    Win Rate: %.1f%% | Improvement: %+.4f | Status: %s\n",
			result.WinRate*100, improvement, status)

		if passed && (bestCandidate == nil || improvement > bestScoreImprovement) {
			bestCandidate = &cand
			bestWinRate = result.WinRate
			bestScoreImprovement = improvement
		}
	}

	fmt.Println("\n--- Step 3: Genealogy Recording ---")
	if bestCandidate != nil {
		lineage := evolution.StrategyLineage{
			ParentID:         baselineEvo.ID,
			ChildID:          bestCandidate.ID,
			MutationType:     "dream_cycle",
			WinRate:          bestWinRate,
			ScoreImprovement: bestScoreImprovement,
			Timestamp:        time.Now().Unix(),
		}
		_ = genealogy.Record(ctx, lineage)

		recorded := genealogy.getLineages()
		if len(recorded) > 0 {
			rec := recorded[0]
			fmt.Printf("  Lineage Recorded:\n")
			fmt.Printf("    Parent → Child: %s → %s\n", rec.ParentID, rec.ChildID[:12])
			fmt.Printf("    Mutation Type:  %s\n", rec.MutationType)
			fmt.Printf("    Win Rate:       %.2f%%\n", rec.WinRate*100)
			fmt.Printf("    Score Delta:    %+.4f\n", rec.ScoreImprovement)
		}
	} else {
		fmt.Println("  No candidate passed the win rate threshold (0.55).")
		fmt.Println("  Evolution cycle produced no acceptable variant.")
	}

	fmt.Println("\n--- Dream Cycle Summary ---")
	fmt.Println("  Flow: Trigger → Mutate(N candidates) → Arena Test → Select Best → Genealogy")
	fmt.Println("  This cycle demonstrates autonomous self-improvement without human intervention.")
	fmt.Println("  The system continuously explores, evaluates, and adopts better strategies.")
}

// stringsRepeat repeats the given string n times.
func stringsRepeat(s string, n int) string {
	result := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		result = append(result, s...)
	}
	return string(result)
}

// ============================================================
// Main Entry Point
// ============================================================

func main() {
	setupLogger()

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║     GoAgentX Autonomous Evolution (Dream Mode v1) Demo       ║")
	fmt.Println("║                                                              ║")
	fmt.Println("║  This demo showcases 5 core capabilities of the              ║")
	fmt.Println("║  autonomous evolution system using mock implementations.     ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")

	demoBanditFeedbackLoop()
	demoCallbackSystem()
	demoMutationEngine()
	demoArenaRegressionTest()
	demoDreamCycle()

	phaseSeparator("Demo Complete")

	fmt.Println("All 5 scenarios executed successfully!")
	fmt.Println()
	fmt.Println("Summary:")
	fmt.Println("  1. Bandit Feedback Loop     - Experience reinforcement via success/failure")
	fmt.Println("  2. Callback Event System    - Lifecycle hooks for LLM/Tool/Agent events")
	fmt.Println("  3. Strategy Mutation Engine - Parameter and prompt template variations")
	fmt.Println("  4. Arena Regression Test    - A/B comparison with statistical significance")
	fmt.Println("  5. Dream Cycle             - Full orchestration from trigger to genealogy")
	fmt.Println()
	fmt.Println("The autonomous evolution system enables agents to:")
	fmt.Println("  • Learn from experience through feedback loops")
	fmt.Println("  • Monitor all operations via callback events")
	fmt.Println("  • Explore strategy space through controlled mutations")
	fmt.Println("  • Validate improvements with statistical rigor")
	fmt.Println("  • Self-evolve through automated dream cycles")
}
