package evolution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// EvolutionMode selects the strategy evolution algorithm used by DreamCycle.
type EvolutionMode int

const (
	// ModeEvolutionStrategy uses the current (1+λ) evolution strategy:
	// mutate parent → test candidates → deploy best.
	ModeEvolutionStrategy EvolutionMode = iota

	// ModeGeneticAlgorithm uses the full genetic algorithm pipeline:
	// population → selection → crossover → mutation → score → next generation.
	// Requires genome.Population to be initialized via GA config fields.
	ModeGeneticAlgorithm
)

// ErrAllCandidatesRejected is returned by findWinner when no candidate
// passes the win-rate threshold during quick-reject or full evaluation.
// Callers should treat this as "no winner" rather than a hard error.
var ErrAllCandidatesRejected = errors.New("dream cycle: all candidates rejected")

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

	// EvolutionMode selects the evolution algorithm: ModeEvolutionStrategy or ModeGeneticAlgorithm.
	// Default: ModeEvolutionStrategy (backward compatible).
	EvolutionMode EvolutionMode

	// GA config (only used when EvolutionMode == ModeGeneticAlgorithm):

	// PopulationSize is the number of individuals in the GA population.
	// Default: 20.
	PopulationSize int

	// EliteCount is the number of top individuals preserved each generation.
	// Default: 3.
	EliteCount int

	// MutationRate is the probability of mutating each offspring [0, 1].
	// Default: 0.2.
	MutationRate float64

	// SurvivalRate is the fraction of population that survives each generation [0, 1].
	// Default: 0.6.
	SurvivalRate float64

	// SelectionStrategy selects the parent selection algorithm.
	// Supported: "tournament", "rank", "roulette", "sus", "truncation", "" (random).
	// Default: "tournament".
	SelectionStrategy string

	// TournamentSize is the number of competitors per tournament (only for tournament selection).
	// Default: 3.
	TournamentSize int

	// MaxGenerations is the maximum number of GA generations to run.
	// 0 means unlimited (run until manually stopped).
	MaxGenerations int

	// TargetFitness stops evolution when the best score reaches this threshold.
	// 0 means no target (run until MaxGenerations).
	TargetFitness float64

	// CrossoverType selects the parameter recombination strategy for GA evolution.
	// Supported: "uniform", "two_point", "segment". Default: "uniform".
	CrossoverType string

	// SteadyState enables steady-state GA mode: each generation replaces only
	// a fraction of the population (SteadyStateReplaceRate) instead of full
	// generational replacement. Default: false (full generational GA).
	SteadyState bool

	// SteadyStateReplaceRate is the fraction of the population replaced each
	// generation in steady-state mode [0, 1]. Default: 0.3.
	// Only used when SteadyState is true.
	SteadyStateReplaceRate float64
}

// parseCrossoverType converts a string to the corresponding genome.CrossoverType.
// Returns CrossoverUniform for unknown values (safe default).
func parseCrossoverType(s string) genome.CrossoverType {
	switch s {
	case "two_point":
		return genome.CrossoverTwoPoint
	case "segment":
		return genome.CrossoverSegment
	default:
		return genome.CrossoverUniform
	}
}

// DefaultDreamCycleConfig returns sensible defaults for dream cycle configuration.
// ES mode is the default for backward compatibility.
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

		// GA defaults (used when EvolutionMode == ModeGeneticAlgorithm)
		EvolutionMode:          ModeEvolutionStrategy,
		PopulationSize:         20,
		EliteCount:             3,
		MutationRate:           0.2,
		SurvivalRate:           0.6,
		SelectionStrategy:      "tournament",
		TournamentSize:         3,
		MaxGenerations:         0, // unlimited
		TargetFitness:          0, // no target
		CrossoverType:          "uniform",
		SteadyState:            false,
		SteadyStateReplaceRate: 0.3,
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
// In GA mode, it uses genome.Population for full genetic algorithm cycles.
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
	crosser         *genome.Crossover
	config          DreamCycleConfig
	mu              sync.Mutex
	runMu           sync.Mutex // serializes Run() to prevent double-evolution (EV-01)
	// generating reports whether an evolution cycle body is currently
	// executing. Read via GenerationActive by the live-chaos guard (#12:
	// GA quiet window) — chaos must not inject while GA is mid-generation.
	generating atomic.Bool
	taskCount  int64
	lastCycle  time.Time
}

// NewDreamCycle creates a new dream cycle orchestrator with required dependencies.
//
// All dependencies must be non-nil except genealogy which is optional (lineage
// recording will be skipped if nil).
//
// When EvolutionMode is ModeGeneticAlgorithm, a genome.Population is initialized
// automatically from the GA configuration fields in DreamCycleConfig.
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
//	error - non-nil if required dependencies are missing or GA initialization fails.
func NewDreamCycle(
	scheduler *EvolutionScheduler,
	mutator MutatorInterface,
	tester TesterInterface,
	genealogy GenealogyRecorder,
	opts ...DreamCycleOption,
) (*DreamCycle, error) {
	if mutator == nil {
		return nil, errors.New("mutator is required")
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

	// Initialize GA population and crosser if in GA mode.
	if dc.config.EvolutionMode == ModeGeneticAlgorithm {
		if err := dc.initGAPopulation(context.Background()); err != nil {
			return nil, fmt.Errorf("init GA population: %w", err)
		}
		crosser, err := genome.NewCrossover(genome.WithCrossoverType(parseCrossoverType(dc.config.CrossoverType)))
		if err != nil {
			return nil, fmt.Errorf("new crossover: %w", err)
		}
		dc.crosser = crosser
	}

	return dc, nil
}

// Run executes one full dream cycle when triggered by the scheduler.
//
// In ES mode (default): mutate parent → test candidates → deploy best.
// In GA mode: score population → evolve (selection/crossover/mutation) → deploy best.
//
// This is the main orchestration method that coordinates all evolution components.
//
// Args:
//
//	ctx - operation context for cancellation and timeout.
//	data - callback data from the triggering event.
//
// Returns:
//
//	error - non-nil if a critical error occurs during orchestration.
//
//nolint:gocyclo // Complex evolutionary cycle orchestration with multiple phases
func (dc *DreamCycle) Run(ctx context.Context, data CallbackData) error {
	// Serialize Run to prevent double-evolution (EV-01).
	dc.runMu.Lock()
	defer dc.runMu.Unlock()

	// Mark the generation window for the live-chaos pause gate.
	dc.generating.Store(true)
	defer dc.generating.Store(false)

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

	// Route to GA or ES path based on EvolutionMode.
	if dc.config.EvolutionMode == ModeGeneticAlgorithm {
		return dc.runGAEvolution(ctx, cycleCtx, data)
	}

	return dc.runESEvolution(ctx, cycleCtx, data, taskCount)
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

// GenerationActive reports whether an evolution cycle is currently executing.
// The live-chaos loop polls this to honor the GA quiet window (pause injections
// while a generation is in flight).
func (dc *DreamCycle) GenerationActive() bool {
	return dc != nil && dc.generating.Load()
}
