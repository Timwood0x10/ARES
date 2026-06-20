package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// DreamCycleConfig holds configuration for the dream cycle orchestrator.
type DreamCycleConfig struct {
	// Enabled is the master switch for dream cycle execution.
	Enabled bool

	// MinTasksBeforeEvolve is the minimum number of completed tasks before first evolution.
	MinTasksBeforeEvolve int

	// MinScoreDrop is the score drop threshold to trigger evolution (e.g., 0.15 = 15% drop).
	MinScoreDrop float64

	// MaxMutations is the maximum number of candidate strategies generated per cycle.
	MaxMutations int

	// MinWinRate is the minimum win rate required to accept a mutation.
	MinWinRate float64

	// Cooldown is the minimum time between consecutive dream cycles.
	Cooldown time.Duration
}

// DefaultDreamCycleConfig returns sensible defaults for dream cycle configuration.
func DefaultDreamCycleConfig() DreamCycleConfig {
	return DreamCycleConfig{
		Enabled:              false,
		MinTasksBeforeEvolve: 10,
		MinScoreDrop:         0.15,
		MaxMutations:         3,
		MinWinRate:           0.55,
		Cooldown:             5 * time.Minute,
	}
}

// DreamCycleOption configures a DreamCycle instance.
type DreamCycleOption func(*DreamCycle) error

// WithDreamCycleConfig applies a full DreamCycleConfig to the DreamCycle.
//
// Args:
//
//	cfg - the configuration to apply.
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleConfig(cfg DreamCycleConfig) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.config = cfg
		return nil
	}
}

// DreamCycle orchestrates the full autonomous evolution loop.
// It connects: Callback trigger -> Flight->Exp Adapter -> Scheduler ->
// Mutator -> Arena Regression -> Genealogy recording.
type DreamCycle struct {
	scheduler     *EvolutionScheduler
	mutator       MutatorInterface
	tester        TesterInterface
	genealogy     GenealogyRecorder
	strategyStore StrategyStore
	config        DreamCycleConfig
	mu            sync.Mutex // Protects taskCount and lastCycle from concurrent access.
	taskCount     int64
	lastCycle     time.Time
}

// NewDreamCycle creates a new dream cycle orchestrator with required dependencies.
//
// All dependencies must be non-nil except genealogy which is optional (lineage
// recording will be skipped if nil).
//
// Args:
//
//	scheduler - the evolution scheduler that triggers this cycle.
//	mutator - the strategy mutator for generating candidate variants.
//	tester - the arena regression tester for evaluating candidates.
//	genealogy - optional recorder for strategy lineage (may be nil).
//	opts - optional configuration functions.
//
// Returns:
//
//	*DreamCycle - the configured dream cycle instance.
//	error - non-nil if required dependencies are missing.
func NewDreamCycle(
	scheduler *EvolutionScheduler,
	mutator MutatorInterface,
	tester TesterInterface,
	genealogy GenealogyRecorder,
	opts ...DreamCycleOption,
) (*DreamCycle, error) {
	if scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	if mutator == nil {
		return nil, fmt.Errorf("mutator is required")
	}
	if tester == nil {
		return nil, fmt.Errorf("tester is required")
	}

	dc := &DreamCycle{
		scheduler: scheduler,
		mutator:   mutator,
		tester:    tester,
		genealogy: genealogy,
		config:    DefaultDreamCycleConfig(),
	}

	for _, opt := range opts {
		if err := opt(dc); err != nil {
			return nil, fmt.Errorf("dream cycle option: %w", err)
		}
	}

	return dc, nil
}

// Run executes one full dream cycle when triggered by the scheduler.
// This is the main orchestration method that coordinates all evolution components:
//
//  1. Collect recent task score trends (from experience system or flight recorder).
//  2. Decide whether evolution is needed (delegated to shouldEvolve heuristic).
//  3. If not needed, return nil quickly (fast path).
//  4. If needed:
//     a. Get current active strategy.
//     b. Call Mutator.Mutate() to generate N candidate variants.
//     c. For each candidate, call Tester.Run() for arena regression testing.
//     d. Select highest-scoring candidate with WinRate > threshold.
//     e. If winner exists, record Genealogy and return winning strategy.
//     f. If no winner, record failure experience and return nil (no change).
//
// Args:
//
//	ctx - operation context for cancellation and timeout.
//	data - callback data from the triggering event.
//
// Returns:
//
//	error - non-nil if a critical error occurs during orchestration.
func (dc *DreamCycle) Run(ctx context.Context, data CallbackData) error {
	// Increment task counter unconditionally for threshold tracking.
	dc.mu.Lock()
	dc.taskCount++
	taskCount := dc.taskCount
	lastCycle := dc.lastCycle
	dc.mu.Unlock()

	if !dc.config.Enabled {
		slog.DebugContext(ctx, "[DreamCycle] Disabled, skipping cycle")
		return nil
	}

	// Check cooldown between cycles.
	if !lastCycle.IsZero() && time.Since(lastCycle) < dc.config.Cooldown {
		slog.DebugContext(ctx, "[DreamCycle] Cooldown active, skipping",
			"last_cycle", lastCycle.Format(time.RFC3339),
			"cooldown", dc.config.Cooldown)
		return nil
	}

	if taskCount < int64(dc.config.MinTasksBeforeEvolve) {
		slog.DebugContext(ctx, "[DreamCycle] Not enough tasks yet",
			"task_count", taskCount,
			"min_required", dc.config.MinTasksBeforeEvolve)
		return nil
	}

	// Delegate evolution decision to scheduler's shouldEvolve logic.
	if !dc.scheduler.shouldEvolve(ctx, data) {
		return nil
	}

	slog.InfoContext(ctx, "[DreamCycle] Starting evolution cycle",
		"agent_id", data.AgentID,
		"task_count", taskCount)

	// Step 1: Get current active strategy as parent for mutation.
	parent, err := dc.getCurrentStrategy(ctx)
	if err != nil {
		slog.WarnContext(ctx, "[DreamCycle] Failed to get active strategy", "error", err)
		return nil
	}
	if parent.ID == "" {
		slog.WarnContext(ctx, "[DreamCycle] No active strategy available, skipping")
		return nil
	}

	// Step 2: Generate candidate mutations.
	candidates, err := dc.mutator.Mutate(ctx, parent, dc.config.MaxMutations)
	if err != nil {
		return fmt.Errorf("mutate strategy: %w", err)
	}
	if len(candidates) == 0 {
		slog.InfoContext(ctx, "[DreamCycle] No candidates generated")
		return nil
	}

	// Step 3: Test each candidate in arena and find best winner.
	winner, err := dc.findWinner(ctx, candidates, parent)
	if err != nil {
		return fmt.Errorf("arena regression: %w", err)
	}
	if winner == nil {
		slog.InfoContext(ctx, "[DreamCycle] No candidate passed win rate threshold",
			"min_win_rate", dc.config.MinWinRate)
		dc.recordFailure(ctx, parent)
		return nil
	}

	// Step 4: Record lineage if genealogy recorder is available.
	if dc.genealogy != nil {
		lineage := StrategyLineage{
			ParentID:         parent.ID,
			ChildID:          winner.strategy.ID,
			MutationType:     "dream_cycle",
			WinRate:          winner.winRate,
			ScoreImprovement: winner.scoreImprovement,
			Timestamp:        time.Now().Unix(),
		}
		if err := dc.genealogy.Record(ctx, lineage); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Failed to record lineage",
				"error", err)
			// Non-fatal: continue without lineage record.
		}
	}

	dc.lastCycle = time.Now()
	slog.InfoContext(ctx, "[DreamCycle] Evolution cycle complete",
		"winner_id", winner.strategy.ID,
		"win_rate", winner.winRate,
		"score_improvement", winner.scoreImprovement)

	return nil
}

// candidateResult holds an evaluated candidate strategy with its test results.
type candidateResult struct {
	strategy         Strategy
	winRate          float64
	scoreImprovement float64
}

// WithStrategyStore sets the strategy store for persisting evolved strategies.
//
// Args:
//
//	store - the strategy store implementation (may be nil to disable persistence).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithStrategyStore(store StrategyStore) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.strategyStore = store
		return nil
	}
}

// getCurrentStrategy returns the currently deployed strategy from the strategy store.
// Falls back to a default root strategy if none has been stored yet.
//
// Args:
//
//	ctx - operation context for store lookup.
//
// Returns:
//
//	Strategy - the active strategy, or a default on first run.
//	error - non-nil if store lookup fails.
func (dc *DreamCycle) getCurrentStrategy(ctx context.Context) (Strategy, error) {
	if dc.strategyStore == nil {
		slog.WarnContext(ctx, "[DreamCycle] No strategy store configured; using default")
		return defaultRootStrategy(), nil
	}

	stored, err := dc.strategyStore.GetActive(ctx)
	if err != nil {
		return Strategy{}, fmt.Errorf("get active strategy: %w", err)
	}

	if stored == nil {
		slog.InfoContext(ctx, "[DreamCycle] No stored strategy found; initializing with default")
		return defaultRootStrategy(), nil
	}

	return *stored, nil
}

// defaultRootStrategy returns a sensible root strategy for first-time initialization.
func defaultRootStrategy() Strategy {
	return Strategy{
		ID:      "root-strategy-v1",
		Name:    "DefaultStrategy",
		Version: 1,
		Params: map[string]any{
			"temperature":  0.7,
			"max_tokens":   4096,
			"retry_count":  3,
			"timeout_secs": 120,
		},
		StrategyMutationType: "",
		Score:                -1,
		CreatedAt:            time.Now(),
	}
}

// findWinner tests all candidates in arena and returns the best one above threshold.
func (dc *DreamCycle) findWinner(
	ctx context.Context,
	candidates []Strategy,
	baseline Strategy,
) (*candidateResult, error) {
	var best *candidateResult

	for _, cand := range candidates {
		result, err := dc.tester.Run(ctx, RegressionConfig{
			Candidate:      cand,
			Baseline:       baseline,
			TaskSampleSize: 50,
		})
		if err != nil {
			slog.WarnContext(ctx, "[DreamCycle] Candidate test failed",
				"candidate_id", cand.ID,
				"error", err)
			continue
		}

		// Skip candidates below minimum win rate threshold.
		if result.WinRate < dc.config.MinWinRate {
			slog.DebugContext(ctx, "[DreamCycle] Candidate below threshold",
				"candidate_id", cand.ID,
				"win_rate", result.WinRate,
				"threshold", dc.config.MinWinRate)
			continue
		}

		cr := &candidateResult{
			strategy:         cand,
			winRate:          result.WinRate,
			scoreImprovement: result.CandidateScore - result.BaselineScore,
		}

		if best == nil || cr.scoreImprovement > best.scoreImprovement {
			best = cr
		}
	}

	return best, nil
}

// recordFailure logs a failed evolution cycle for future analysis.
func (dc *DreamCycle) recordFailure(ctx context.Context, parent Strategy) {
	slog.InfoContext(ctx, "[DreamCycle] Evolution cycle produced no acceptable candidate",
		"parent_id", parent.ID,
		"max_mutations", dc.config.MaxMutations,
		"min_win_rate", dc.config.MinWinRate)
}

// SetEnabled enables or disables the dream cycle at runtime.
//
// Args:
//
//	enabled - true to enable, false to disable.
func (dc *DreamCycle) SetEnabled(enabled bool) {
	dc.config.Enabled = enabled
}

// IsEnabled returns whether the dream cycle is currently enabled.
//
// Returns:
//
//	bool - true if enabled, false otherwise.
func (dc *DreamCycle) IsEnabled() bool {
	return dc.config.Enabled
}

// TaskCount returns the number of tasks processed since creation.
// Thread-safe: uses mutex to protect concurrent access.
//
// Returns:
//
//	int64 - the accumulated task count.
func (dc *DreamCycle) TaskCount() int64 {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.taskCount
}
