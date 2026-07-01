package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
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

	// TaskSampleSize is the number of scoring runs per strategy for the final evaluation.
	// Default 50. With adaptive batching, actual calls may be less.
	TaskSampleSize int

	// QuickRejectRuns is the number of runs for the first-pass screening.
	// Candidates below MinWinRate after this many runs are discarded without full eval.
	// Default 5. Set to 0 to skip quick rejection.
	QuickRejectRuns int
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
		TaskSampleSize:       50,
		QuickRejectRuns:      5,
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
	scheduler       *EvolutionScheduler
	mutator         MutatorInterface
	tester          TesterInterface
	genealogy       GenealogyRecorder
	strategyStore   StrategyStore
	guardrails      *EvolutionGuardrails
	shadowEvaluator *ShadowEvaluator
	stateManager    *ActiveStrategyManager
	metrics         MetricsRecorder
	hintProvider    mutation.HintProvider
	population      *genome.Population
	config          DreamCycleConfig
	mu              sync.Mutex
	taskCount       int64
	lastCycle       time.Time
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
	if mutator == nil {
		return nil, fmt.Errorf("mutator is required")
	}
	// scheduler and tester may be nil at construction time and wired later
	// via direct field assignment (e.g., in NewWiredEvolutionSystem).
	// Run() checks them at invocation time before use.

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
	// Enforce a max duration for the entire cycle to prevent hangs.
	cycleCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Increment task counter unconditionally for threshold tracking.
	// Also read Enabled under lock to prevent data races.
	dc.mu.Lock()
	dc.taskCount++
	taskCount := dc.taskCount
	lastCycle := dc.lastCycle
	enabled := dc.config.Enabled
	dc.mu.Unlock()

	if !enabled {
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

	// Runtime guard: scheduler and tester may be nil when wired lazily.
	if dc.scheduler == nil {
		slog.WarnContext(ctx, "[DreamCycle] Scheduler not wired yet, skipping cycle")
		return nil
	}
	if dc.tester == nil {
		slog.WarnContext(ctx, "[DreamCycle] Tester not wired yet, skipping cycle")
		return nil
	}

	if taskCount < int64(dc.config.MinTasksBeforeEvolve) {
		slog.DebugContext(ctx, "[DreamCycle] Not enough tasks yet",
			"task_count", taskCount,
			"min_required", dc.config.MinTasksBeforeEvolve)
		return nil
	}

	// Delegate evolution decision to scheduler's exported ShouldEvolve method.
	if !dc.scheduler.ShouldEvolve(ctx, data) {
		return nil
	}

	popGen := 0
	popSize := 0
	if dc.population != nil {
		popGen = dc.population.CurrentGeneration()
		popSize = len(dc.population.Agents)
	}
	slog.InfoContext(ctx, "[DreamCycle] Starting evolution cycle",
		"agent_id", data.AgentID,
		"task_count", taskCount,
		"trigger", dc.scheduler.TriggerMode().String(),
		"generation", popGen,
		"population_size", popSize)

	// Pre-evolution guardrail check.
	// Pass taskCount as totalPop so the unevaluated ratio check is meaningful.
	// Generation is not tracked yet; currentBest is sourced from active strategy.
	var currentBest float64
	if dc.stateManager != nil {
		if cur := dc.stateManager.Current(); cur != nil {
			currentBest = cur.Score
		}
	}
	if dc.guardrails != nil {
		gen := 0
		unevaluatedCount := 0
		if dc.population != nil {
			gen = dc.population.CurrentGeneration()
			agents, _ := dc.population.Snapshot()
			for _, a := range agents {
				if !genome.IsScoreEvaluated(a.Score) {
					unevaluatedCount++
				}
			}
		}
		preResult := dc.guardrails.PreEvolveCheck(ctx, currentBest, gen, int(taskCount), unevaluatedCount)
		if preResult.ShouldStop {
			slog.WarnContext(ctx, "[DreamCycle] Pre-evolution guardrails prevent cycle",
				"events", len(preResult.Events))
			return nil
		}
	}

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
	candidates, err := dc.mutator.Mutate(cycleCtx, parent, dc.config.MaxMutations)
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
			ParentScore:      parent.Score,
			ChildScore:       winner.scoreImprovement + parent.Score,
			Timestamp:        time.Now().Unix(),
		}
		if err := dc.genealogy.Record(ctx, lineage); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Failed to record lineage",
				"error", err)
		}
	}

	// Step 5: Post-evolution guardrail check.
	if dc.guardrails != nil {
		gen := 0
		var lineageShares map[string]int
		if dc.population != nil {
			gen = dc.population.CurrentGeneration()
			if agents, _ := dc.population.Snapshot(); len(agents) > 0 {
				lineageShares = computeLineageShares(agents)
			}
		}
		postResult := dc.guardrails.PostEvolveCheck(ctx, winner.winRate, gen, lineageShares)
		if postResult.ShouldStop {
			slog.WarnContext(ctx, "[DreamCycle] Post-evolution guardrails block deploy",
				"winner_id", winner.strategy.ID,
				"win_rate", winner.winRate,
				"score", winner.winRate,
				"events", len(postResult.Events),
				"generation", gen)
			return nil
		}
	}

	// Step 6: Shadow evaluation before deployment.
	if dc.shadowEvaluator != nil {
		mtnWinner := winnerToMutationStrategy(winner)
		if mtnWinner == nil {
			slog.ErrorContext(ctx, "[DreamCycle] winnerToMutationStrategy returned nil, skipping shadow")
			return nil
		}
		// Set active strategy for comparison and start shadow evaluation.
		parentMutation := evolutionToMutationStrategy(parent)
		dc.shadowEvaluator.SetActiveStrategy(&parentMutation)
		dc.shadowEvaluator.StartShadow(mtnWinner)

		// Use independent scorer if available, otherwise fall back to manual scores.
		if dc.shadowEvaluator.HasIndependentScorer() {
			dc.shadowEvaluator.Evaluate(ctx)
		} else {
			dc.shadowEvaluator.RecordResult(parent.Score, winner.scoreImprovement+parent.Score)
		}

		shouldDeploy, report := dc.shadowEvaluator.ShouldDeploy()
		if !shouldDeploy {
			slog.InfoContext(ctx, "[DreamCycle] Shadow evaluation rejects deployment",
				"candidate_id", winner.strategy.ID,
				"active_id", parent.ID,
				"win_rate", report.WinRate,
				"threshold", dc.shadowEvaluator.minWinRate,
				"reason", report.Recommendation)
			if dc.metrics != nil {
				dc.metrics.RecordEvolutionShadow("rejected")
			}
			return nil
		}
		slog.InfoContext(ctx, "[DreamCycle] Shadow evaluation approves deployment",
			"candidate_id", winner.strategy.ID,
			"active_id", parent.ID,
			"win_rate", report.WinRate,
			"threshold", dc.shadowEvaluator.minWinRate)
		if dc.metrics != nil {
			dc.metrics.RecordEvolutionShadow("promoted")
		}
	}

	// Step 7: Deploy via ActiveStrategyManager.
	if dc.stateManager != nil {
		mtnWinner := winnerToMutationStrategy(winner)
		if mtnWinner == nil {
			slog.ErrorContext(ctx, "[DreamCycle] winnerToMutationStrategy returned nil, skipping deploy")
			return nil
		}
		if err := ValidateStrategySize(mtnWinner); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Winning strategy exceeds size limits",
				"winner_id", winner.strategy.ID,
				"error", err)
			return nil
		}
		if err := dc.stateManager.Deploy(cycleCtx, mtnWinner); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Failed to deploy winning strategy",
				"winner_id", winner.strategy.ID,
				"error", err)
			return nil
		}
		slog.InfoContext(ctx, "[DreamCycle] Winning strategy deployed",
			"winner_id", winner.strategy.ID,
			"win_rate", winner.winRate)
		if dc.metrics != nil {
			dc.metrics.RecordEvolutionDeploy("success")
			dc.metrics.SetEvolutionScore(winner.strategy.ID, winner.winRate)
		}

		// Record successful outcome for hint provider learning.
		if dc.hintProvider != nil {
			outcome := mutation.StrategyOutcome{
				StrategyID:   winner.strategy.ID,
				TaskType:     data.AgentID,
				Success:      true,
				Score:        winner.winRate,
				MutationType: "dream_cycle",
				Timestamp:    time.Now(),
			}
			if err := dc.hintProvider.RecordStrategyOutcome(cycleCtx, outcome); err != nil {
				slog.WarnContext(ctx, "[DreamCycle] Failed to record strategy outcome",
					"error", err)
			}
		}
	}

	dc.mu.Lock()
	dc.lastCycle = time.Now()
	dc.mu.Unlock()

	slog.InfoContext(ctx, "[DreamCycle] Evolution cycle complete",
		"winner_id", winner.strategy.ID,
		"win_rate", winner.winRate,
		"score_improvement", winner.scoreImprovement,
		"trigger", dc.scheduler.TriggerMode().String(),
		"generation", 0,
		"population_size", 0)

	return nil
}

// candidateResult holds an evaluated candidate strategy with its test results.
type candidateResult struct {
	strategy         Strategy
	winRate          float64
	scoreImprovement float64
}

// WithDreamCycleGuardrails attaches a guardrail checker to the dream cycle.
//
// Args:
//
//	guardrails - the evolution guardrails instance (may be nil to disable).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleGuardrails(guardrails *EvolutionGuardrails) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.guardrails = guardrails
		return nil
	}
}

// WithDreamCycleShadowEvaluator attaches a shadow evaluator for safe deployment.
//
// Args:
//
//	se - the shadow evaluator instance (may be nil to disable).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleShadowEvaluator(se *ShadowEvaluator) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.shadowEvaluator = se
		return nil
	}
}

// WithDreamCycleMetrics attaches a metrics recorder for evolution event counters.
//
// Args:
//
//	metrics - the metrics recorder (may be nil to disable).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleMetrics(metrics MetricsRecorder) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.metrics = metrics
		return nil
	}
}

// WithDreamCycleHintProvider attaches a hint provider for recording strategy
// outcomes after each evolution cycle. The hint provider learns from real
// execution outcomes and provides better hints for future mutations.
//
// Args:
//
//	provider - the hint provider (may be nil to disable).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleHintProvider(provider mutation.HintProvider) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.hintProvider = provider
		return nil
	}
}

// WithDreamCycleTester attaches a regression tester for candidate evaluation.
//
// Args:
//
//	tester - the arena regression tester (may be nil to disable).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleTester(tester TesterInterface) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.tester = tester
		return nil
	}
}

// WithDreamCycleStrategyManager attaches a strategy manager for deployment.
//
// Args:
//
//	mgr - the active strategy manager (may be nil to disable).
//
// Returns:
//
//	DreamCycleOption - the option function.
func WithDreamCycleStrategyManager(mgr *ActiveStrategyManager) DreamCycleOption {
	return func(dc *DreamCycle) error {
		dc.stateManager = mgr
		return nil
	}
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
//
// Uses a two-stage approach:
//  1. Quick reject: all candidates are screened in parallel with N=QuickRejectRuns (default 5).
//     Those below MinWinRate are discarded.
//  2. Full eval: survivors are evaluated in parallel with N=TaskSampleSize (default 50)
//     and adaptive batching. The best is returned.
func (dc *DreamCycle) findWinner(
	ctx context.Context,
	candidates []Strategy,
	baseline Strategy,
) (*candidateResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("dream cycle: no candidates to evaluate")
	}

	// Stage 1: Quick reject — screen all candidates in parallel with small N.
	quickRejectN := dc.config.QuickRejectRuns
	survivors := candidates

	if quickRejectN > 0 {
		type quickResult struct {
			candidate Strategy
			winRate   float64
		}

		quickResults := make([]*quickResult, len(candidates))
		g, gCtx := errgroup.WithContext(ctx)

		for i, cand := range candidates {
			i, cand := i, cand
			g.Go(func() error {
				result, err := dc.tester.Run(gCtx, RegressionConfig{
					Candidate:         cand,
					Baseline:          baseline,
					TaskSampleSize:    quickRejectN,
					AdaptiveBatchSize: quickRejectN, // single batch, no adaptive benefit
				})
				if err != nil {
					slog.WarnContext(ctx, "[DreamCycle] Quick reject failed",
						"candidate_id", cand.ID, "error", err)
					return nil // skip on error
				}
				quickResults[i] = &quickResult{candidate: cand, winRate: result.WinRate}
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return nil, err
		}

		survivors = nil
		for _, qr := range quickResults {
			if qr == nil {
				continue
			}
			if qr.winRate >= dc.config.MinWinRate {
				survivors = append(survivors, qr.candidate)
			} else {
				slog.DebugContext(ctx, "[DreamCycle] Candidate rejected in quick pass",
					"candidate_id", qr.candidate.ID,
					"win_rate", qr.winRate,
					"threshold", dc.config.MinWinRate)
			}
		}

		rejected := len(candidates) - len(survivors)
		if rejected > 0 {
			slog.InfoContext(ctx, "[DreamCycle] Quick reject filtered candidates",
				"total", len(candidates),
				"survivors", len(survivors),
				"rejected", rejected)
		}
	}

	if len(survivors) == 0 {
		return nil, fmt.Errorf("dream cycle: all candidates rejected in quick pass")
	}

	// Stage 2: Full evaluation — run survivors in parallel with full N.
	type evalResult struct {
		candidateResult
		err error
	}

	evalResults := make([]*evalResult, len(survivors))
	g, gCtx := errgroup.WithContext(ctx)

	for i, cand := range survivors {
		i, cand := i, cand
		g.Go(func() error {
			result, err := dc.tester.Run(gCtx, RegressionConfig{
				Candidate:         cand,
				Baseline:          baseline,
				TaskSampleSize:    dc.config.TaskSampleSize,
				AdaptiveBatchSize: 5,
			})
			if err != nil {
				evalResults[i] = &evalResult{err: err}
				return nil // skip on error
			}
			evalResults[i] = &evalResult{
				candidateResult: candidateResult{
					strategy:         cand,
					winRate:          result.WinRate,
					scoreImprovement: result.CandidateScore - result.BaselineScore,
				},
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Pick the best among survivors above threshold.
	var best *candidateResult
	for _, er := range evalResults {
		if er == nil || er.err != nil {
			continue
		}
		if er.winRate < dc.config.MinWinRate {
			continue
		}
		if best == nil || er.scoreImprovement > best.scoreImprovement {
			cr := er.candidateResult
			best = &cr
		}
	}

	return best, nil
}

// recordFailure logs a failed evolution cycle for future analysis and records
// the failure outcome for hint provider learning.
func (dc *DreamCycle) recordFailure(ctx context.Context, parent Strategy) {
	slog.InfoContext(ctx, "[DreamCycle] Evolution cycle produced no acceptable candidate",
		"parent_id", parent.ID,
		"max_mutations", dc.config.MaxMutations,
		"min_win_rate", dc.config.MinWinRate)

	if dc.hintProvider != nil {
		outcome := mutation.StrategyOutcome{
			StrategyID:   parent.ID,
			Success:      false,
			Score:        parent.Score,
			MutationType: "dream_cycle",
			Timestamp:    time.Now(),
		}
		if err := dc.hintProvider.RecordStrategyOutcome(ctx, outcome); err != nil {
			slog.WarnContext(ctx, "[DreamCycle] Failed to record failure outcome",
				"error", err)
		}
	}
}

// MetricsRecorder abstracts Prometheus metrics recording for evolution events.
// The observability.PrometheusMetrics type satisfies this interface.
type MetricsRecorder interface {
	RecordEvolutionDeploy(status string)
	RecordEvolutionShadow(result string)
	SetEvolutionScore(strategyID string, score float64)
}

// winnerToMutationStrategy converts a candidateResult to a mutation.Strategy
// pointer for deployment via ActiveStrategyManager.
//
// Args:
//
//	result - the evaluated candidate result.
//
// Returns:
//
//	*mutation.Strategy - pointer to the strategy ready for deployment, or nil.
func winnerToMutationStrategy(result *candidateResult) *mutation.Strategy {
	if result == nil {
		return nil
	}
	ms := evolutionToMutationStrategy(result.strategy)
	ms.Score = result.winRate
	return &ms
}

// SetEnabled enables or disables the dream cycle at runtime.
// Thread-safe: uses mutex to protect concurrent access to config.Enabled.
//
// Args:
//
//	enabled - true to enable, false to disable.
func (dc *DreamCycle) SetEnabled(enabled bool) {
	dc.mu.Lock()
	dc.config.Enabled = enabled
	dc.mu.Unlock()
}

// IsEnabled returns whether the dream cycle is currently enabled.
// Thread-safe: uses mutex to protect concurrent access to config.Enabled.
//
// Returns:
//
//	bool - true if enabled, false otherwise.
func (dc *DreamCycle) IsEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
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
