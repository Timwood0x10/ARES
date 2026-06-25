// Package evolution provides wiring between the genome population system
// and the DreamCycle/EvolutionScheduler orchestration layer.
//
// This file bridges the type gap between genome.Population (which operates
// on *mutation.Strategy) and the evolution package (which uses evolution.Strategy).
// It provides adapters and factory functions for building a fully connected
// autonomous evolution system.
package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/scoring"
	"github.com/Timwood0x10/ares/internal/callbacks"
)

// GenomePopulationAdapter wraps a genome.Population to implement AdapterRunner.
// It allows the EvolutionScheduler to trigger genome-based evolution cycles
// when agents complete tasks.
//
// When a scorer is set, new offspring (IsScoreEvaluated() == false) are automatically scored
// after each evolution cycle, closing the scoring loop for the scheduler path.
type GenomePopulationAdapter struct {
	pop     *genome.Population
	mutator genome.MutatorInterface
	crosser genome.CrossoverInterface
	scorer  func(*mutation.Strategy) float64

	// Scoring infrastructure for cost-controlled evaluation (optional).
	// When set via WithAdapterTieredScoring, Run() uses TieredScorer pipeline
	// instead of the plain scorer path.
	tieredScorer *scoring.TieredScorer
	budget       *scoring.Budget
	scoreCache   *scoring.ScoreCache

	// Guardrails for pre/post evolution safety checks (optional).
	// When set via WithAdapterGuardrails, Run() runs safety checks before
	// and after each evolution cycle.
	guardrails *EvolutionGuardrails

	// Memory-aware scorer for evidence-based scoring adjustments (optional).
	// When set via WithAdapterMemoryAwareScoring, Run() wraps the tiered
	// scorer pipeline with memory-aware adjustments, preserving tiered
	// scoring stats and context propagation.
	memoryScorer *scoring.MemoryAwareScorer
}

// NewGenomePopulationAdapter creates an adapter around a genome population.
//
// Args:
//
//	pop - the managed population (must not be nil).
//	mutator - the genome-compatible mutator (must not be nil).
//	crosser - the genome-compatible crossover engine (must not be nil).
//
// Returns:
//
//	*GenomePopulationAdapter - the configured adapter.
//	error - non-nil if any required dependency is nil.
func NewGenomePopulationAdapter(
	pop *genome.Population,
	mutator genome.MutatorInterface,
	crosser genome.CrossoverInterface,
	opts ...GenomeAdapterOption,
) (*GenomePopulationAdapter, error) {
	if pop == nil {
		return nil, fmt.Errorf("population must not be nil")
	}
	if mutator == nil {
		return nil, fmt.Errorf("mutator must not be nil")
	}
	if crosser == nil {
		return nil, fmt.Errorf("crosser must not be nil")
	}
	adapter := &GenomePopulationAdapter{
		pop:     pop,
		mutator: mutator,
		crosser: crosser,
	}
	for _, opt := range opts {
		opt(adapter)
	}
	return adapter, nil
}

// GenomeAdapterOption configures a GenomePopulationAdapter.
type GenomeAdapterOption func(*GenomePopulationAdapter)

// WithAdapterScorer sets a scoring function that is called after each evolution
// cycle to assign scores to newly generated offspring (IsScoreEvaluated() == false).
// Without this, the scheduler path produces unevaluated agents that distort
// selection and diversity metrics.
//
// Args:
//
//	scorer - function that takes an internal strategy and returns its fitness score.
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterScorer(scorer func(*mutation.Strategy) float64) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.scorer = scorer
	}
}

// WithAdapterTieredScoring configures the adapter to use a TieredScorer pipeline
// instead of the plain scorer. This enables LLM budget control, score caching,
// and automatic fallback from LLM to heuristic scoring.
//
// Args:
//
//	ts - the configured tiered scorer (must not be nil).
//	budget - the budget tracker (must not be nil).
//	cache - the shared score cache (must not be nil).
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterTieredScoring(ts *scoring.TieredScorer, budget *scoring.Budget, cache *scoring.ScoreCache) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.tieredScorer = ts
		a.budget = budget
		a.scoreCache = cache
	}
}

// WithAdapterGuardrails sets the evolution guardrails for pre/post safety checks.
// When set, Run() calls PreEvolveCheck before evolution and PostEvolveCheck after.
// Without this, guardrails are disabled and behavior is unchanged.
//
// Args:
//
//	g - the configured guardrails instance (may be nil to disable).
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterGuardrails(g *EvolutionGuardrails) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.guardrails = g
	}
}

// WithAdapterMemoryAwareScoring configures the adapter to wrap the tiered
// scorer with memory-aware scoring adjustments. The MemoryAwareScorer adds
// evidence-based bonuses and cost/latency penalties to the fitness score.
//
// This must be used together with WithAdapterTieredScoring. The memory-aware
// scorer wraps the tiered pipeline, preserving all tiered scoring stats
// (cache hits, LLM calls, fallbacks) and proper context propagation.
//
// Args:
//
//	ms - the configured memory-aware scorer (must not be nil).
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterMemoryAwareScoring(ms *scoring.MemoryAwareScorer) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.memoryScorer = ms
	}
}

// Run executes one atomic genome evolution cycle (EvolveAfterScoring) when
// triggered by scheduler. The atomic API handles pre-scoring, evolution, and
// post-scoring in a single call, eliminating the risk of evolving unevaluated agents.
//
// Args:
//
//	ctx - operation context for cancellation.
//
// Returns:
//
//	error - non-nil if evolution fails.
func (a *GenomePopulationAdapter) Run(ctx context.Context) error {
	var scorer genome.ScorerFunc

	if a.tieredScorer != nil {
		// Use tiered scorer pipeline: cache → LLM(budget-gated) → heuristic.
		// Reset per-generation budget at start of each cycle.
		a.tieredScorer.ResetForGeneration()
		scorer = func(s *mutation.Strategy) float64 {
			// When memory-aware scorer is set, delegate through it to get
			// evidence-based bonuses and cost/latency penalties.
			if a.memoryScorer != nil {
				score, _, err := a.memoryScorer.Score(ctx, s)
				if err != nil {
					slog.WarnContext(ctx, "[GenomeAdapter] memory-aware scorer failed, using heuristic",
						"error", err, "strategy_id", s.ID)
					return 50.0
				}
				return score
			}
			score, _, err := a.tieredScorer.Score(ctx, s)
			if err != nil {
				slog.WarnContext(ctx, "[GenomeAdapter] tiered scorer failed, using baseline",
					"error", err, "strategy_id", s.ID)
				return 50.0 // fallback baseline on error
			}
			return score
		}
		// Log scoring stats after evolution.
		defer func() {
			stats := a.tieredScorer.Stats()
			used, max, cacheHits, fallbacks := a.budget.Usage()
			slog.InfoContext(ctx, "[GenomeAdapter] Tiered scoring stats",
				"llm_used", used, "llm_max", max,
				"cache_hits", cacheHits, "fallbacks", fallbacks,
				"tier_stats", stats,
			)
		}()
	} else {
		scorer = buildScorer(a.scorer)
	}

	// --- Pre-evolution guardrails checkpoint ---
	if a.guardrails != nil {
		preStats := a.pop.Stats()
		agents, _ := a.pop.Snapshot()
		unevaluated := countUnevaluated(agents)

		preResult := a.guardrails.PreEvolveCheck(ctx,
			preStats.BestScore,
			preStats.Generation,
			preStats.Size,
			unevaluated,
		)

		// Log all pre-check events
		for _, evt := range preResult.Events {
			slog.WarnContext(ctx, "[GenomeAdapter] Pre-evolve guardrail triggered",
				"rule", evt.Rule,
				"level", evt.Level,
				"message", evt.Message,
				"suggested_action", evt.SuggestedAction,
			)
		}

		if preResult.ShouldStop {
			return fmt.Errorf("[GenomeAdapter] pre-evolve guardrail check failed (generation %d): %d event(s), best_score=%.2f, unevaluated=%d/%d",
				preStats.Generation, len(preResult.Events), preStats.BestScore, unevaluated, preStats.Size)
		}
	}

	if err := a.pop.EvolveAfterScoring(ctx, scorer, a.mutator, a.crosser); err != nil {
		return fmt.Errorf("genome evolve on idle: %w", err)
	}

	// --- Post-evolution guardrails checkpoint ---
	if a.guardrails != nil {
		postStats := a.pop.Stats()
		agents, _ := a.pop.Snapshot()
		lineageShares := computeLineageShares(agents)

		postResult := a.guardrails.PostEvolveCheck(ctx,
			postStats.BestScore,
			postStats.Generation,
			lineageShares,
		)

		// Log all post-check events
		for _, evt := range postResult.Events {
			slog.WarnContext(ctx, "[GenomeAdapter] Post-evolve guardrail triggered",
				"rule", evt.Rule,
				"level", evt.Level,
				"message", evt.Message,
				"suggested_action", evt.SuggestedAction,
			)
		}

		if postResult.ShouldStop {
			// Evolution already completed; log warning but still return error.
			slog.WarnContext(ctx, "[GenomeAdapter] post-evolve guardrail signals stop, but evolution already completed",
				"generation", postStats.Generation,
				"event_count", len(postResult.Events),
			)
			return fmt.Errorf("[GenomeAdapter] post-evolve guardrail check failed after evolution completed (generation %d): %d event(s), best_score=%.2f",
				postStats.Generation, len(postResult.Events), postStats.BestScore)
		}
	}

	stats := a.pop.Stats()
	slog.InfoContext(ctx, "[GenomeAdapter] Evolution cycle completed",
		"generation", stats.Generation,
		"population_size", stats.Size,
		"best_score", stats.BestScore,
		"avg_score", stats.AvgScore,
	)
	return nil
}

// scorerWarningOnce ensures the missing-scorer warning is logged at most once
// per process lifetime, even when buildScorer is called repeatedly (e.g., once
// per evolution cycle in the scheduler loop).
var scorerWarningOnce sync.Once

// buildScorer constructs a ScorerFunc from the optional adapter-level scorer.
// When no scorer is available, returns a constant baseline scorer with a warning.
func buildScorer(scorer func(*mutation.Strategy) float64) genome.ScorerFunc {
	if scorer != nil {
		return scorer
	}
	scorerWarningOnce.Do(func() {
		slog.Warn("[GenomeAdapter] No scorer configured, using constant baseline (50.0). " +
			"Configure a real scorer for production use.")
	})
	// Note: TieredScorer is now available via SystemConfig options (MaxLLMCallsPerGeneration,
	// HeuristicScorer). When those are set, Run() uses the tiered pipeline instead of this
	// fallback path. The ConstantScorer default is retained for backward compatibility.
	return genome.ConstantScorer(50.0)
}

// countUnevaluated counts agents with Score == ScoreUnevaluated.
func countUnevaluated(agents []*mutation.Strategy) int {
	n := 0
	for _, a := range agents {
		if a.Score == genome.ScoreUnevaluated {
			n++
		}
	}
	return n
}

// computeLineageShares computes ParentID distribution from a population snapshot.
// Returns a map of parentID -> count. Root strategies (empty ParentID) are excluded.
func computeLineageShares(agents []*mutation.Strategy) map[string]int {
	shares := make(map[string]int)
	for _, a := range agents {
		if a.ParentID != "" {
			shares[a.ParentID]++
		}
	}
	return shares
}

// Population returns the underlying genome population for direct access.
//
// Returns:
//
//	*genome.Population - the managed population.
func (a *GenomePopulationAdapter) Population() *genome.Population {
	return a.pop
}

// GenomeMutatorAdapter wraps a genome.MutatorInterface-compatible mutator
// to implement genome.MutatorInterface. This enables genome.Population to
// use both the production mutator and the experience-guided mutator.
type GenomeMutatorAdapter struct {
	mutator genome.MutatorInterface
}

// NewGenomeMutatorAdapter creates a genome-compatible mutator adapter.
// The provided mutator must implement the genome.MutatorInterface (both
// *mutation.Mutator and *mutation.ExperienceGuidedMutator satisfy this).
//
// Args:
//
//	m - the mutator to wrap (must not be nil).
//
// Returns:
//
//	*GenomeMutatorAdapter - the adapter instance.
//	error - non-nil if mutator is nil.
func NewGenomeMutatorAdapter(m genome.MutatorInterface) (*GenomeMutatorAdapter, error) {
	if m == nil {
		return nil, fmt.Errorf("mutator must not be nil")
	}
	return &GenomeMutatorAdapter{mutator: m}, nil
}

// Mutate delegates to the wrapped mutator.
// The signature matches genome.MutatorInterface (uses *mutation.Strategy).
//
// Args:
//
//	ctx - operation context for cancellation.
//	parent - the parent strategy to mutate.
//	n - number of children to generate.
//
// Returns:
//
//	[]*mutation.Strategy - the generated child strategies.
//	error - delegation error from the wrapped mutator.
func (a *GenomeMutatorAdapter) Mutate(
	ctx context.Context,
	parent *mutation.Strategy,
	n int,
) ([]*mutation.Strategy, error) {
	children, err := a.mutator.Mutate(ctx, parent, n)
	if err != nil {
		return nil, fmt.Errorf("genome mutator adapter: %w", err)
	}
	return children, nil
}

// PopulationGenealogyRecorder records strategy lineage from genome evolution
// into the evolution package's genealogy system. It implements GenealogyRecorder
// by extracting lineage data from population state after each evolution cycle.
type PopulationGenealogyRecorder struct {
	mu          sync.RWMutex
	lineages    []StrategyLineage
	maxLineages int // Maximum number of lineage records; 0 = unlimited (default 10000).
}

// NewPopulationGenealogyRecorder creates a new genealogy recorder.
//
// Returns:
//
//	*PopulationGenealogyRecorder - the recorder instance.
func NewPopulationGenealogyRecorder() *PopulationGenealogyRecorder {
	return &PopulationGenealogyRecorder{
		lineages:    make([]StrategyLineage, 0),
		maxLineages: 10000,
	}
}

// Record persists a strategy lineage entry from genome evolution results.
// It extracts parent-child relationships from evolved population agents.
//
// Args:
//
//	ctx - operation context.
//	lineage - the lineage record to persist.
//
// Returns:
//
//	error - always nil for in-memory implementation.
func (r *PopulationGenealogyRecorder) Record(ctx context.Context, lineage StrategyLineage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lineages = append(r.lineages, lineage)

	// Trim oldest records if exceeding max capacity.
	if r.maxLineages > 0 && len(r.lineages) > r.maxLineages {
		trimCount := len(r.lineages) - r.maxLineages
		r.lineages = r.lineages[trimCount:]
	}

	slog.DebugContext(ctx, "[Genealogy] Lineage recorded",
		"parent_id", lineage.ParentID,
		"child_id", lineage.ChildID,
		"mutation_type", lineage.MutationType,
	)

	return nil
}

// Lineages returns all recorded lineage entries (thread-safe).
//
// Returns:
//
//	[]StrategyLineage - copy of recorded lineages.
func (r *PopulationGenealogyRecorder) Lineages() []StrategyLineage {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]StrategyLineage, len(r.lineages))
	copy(result, r.lineages)
	return result
}

// Count returns the number of recorded lineage entries.
//
// Returns:
//
//	int - number of lineages.
func (r *PopulationGenealogyRecorder) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.lineages)
}

// RecordPopulationLineage extracts parent-child relationships from a genome
// population after evolution and records them into the genealogy system.
// This bridges genome.Population's ParentID tracking with evolution.GenealogyRecorder.
//
// Args:
//
//	ctx - operation context.
//	pop - the post-evolution population to extract lineage from.
//	prevGeneration - the generation number before evolution (for filtering).
//
// Returns:
//
//	int - number of new lineage records created.
//	error - non-nil if recording fails.
func RecordPopulationLineage(
	ctx context.Context,
	pop *genome.Population,
	recorder GenealogyRecorder,
	prevGeneration int,
) (int, error) {
	if pop == nil || recorder == nil {
		return 0, nil
	}

	// Snapshot provides a thread-safe locked read of all agents and generation.
	agents, generation := pop.Snapshot()

	count := 0
	for _, agent := range agents {
		if agent.ParentID == "" {
			continue
		}
		if agent.Version <= 1 {
			continue
		}

		lineage := StrategyLineage{
			ParentID:     agent.ParentID,
			ChildID:      agent.ID,
			MutationType: agent.StrategyMutationType.String(),
			Timestamp:    agent.CreatedAt.Unix(),
		}

		if err := recorder.Record(ctx, lineage); err != nil {
			return count, fmt.Errorf("record lineage for agent %s: %w", agent.ID, err)
		}
		count++
	}

	if count > 0 {
		slog.InfoContext(ctx, "[Genealogy] Recorded population lineage",
			"new_records", count,
			"generation", generation,
		)
	}

	return count, nil
}

// WiredEvolutionSystem holds a fully wired autonomous evolution system.
// It contains all components pre-connected and ready for production use.
type WiredEvolutionSystem struct {
	Scheduler  *EvolutionScheduler
	DreamCycle *DreamCycle
	PopAdapter *GenomePopulationAdapter
	Population *genome.Population
	Genealogy  *PopulationGenealogyRecorder

	// StrategyStore persists deployed strategies (optional, may be nil).
	StrategyStore StrategyStore
}

// SystemConfig holds configuration for creating a wired evolution system.
type SystemConfig struct {
	// PopulationSize is the target population size for genome evolution.
	PopulationSize int `json:"population_size"`

	// EliteCount is the number of elite strategies to preserve per generation.
	EliteCount int `json:"elite_count"`

	// MutationRate is the probability of mutating each offspring.
	MutationRate float64 `json:"mutation_rate"`

	// SurvivalRate is the fraction of top performers to keep.
	SurvivalRate float64 `json:"survival_rate"`

	// Callbacks is the callback registrar for event subscription.
	Callbacks callbacks.CallbackRegistrar `json:"-"`

	// EnableDreamCycle enables the dream cycle orchestrator.
	EnableDreamCycle bool `json:"enable_dream_cycle"`

	// EnableScheduler enables the evolution scheduler.
	EnableScheduler bool `json:"enable_scheduler"`

	// MinTasksBeforeEvolve is the minimum tasks before first evolution.
	MinTasksBeforeEvolve int `json:"min_tasks_before_evolve"`

	// SchedulerTrigger is the trigger mode for the scheduler.
	SchedulerTrigger EvolutionTrigger `json:"scheduler_trigger"`

	// MutatorSeed is the random seed for the mutator (0 = non-deterministic).
	MutatorSeed int64 `json:"mutator_seed,omitempty"`

	// CrossoverSeed is the random seed for the crossover engine (0 = non-deterministic).
	CrossoverSeed int64 `json:"crossover_seed,omitempty"`

	// PopulationSeed is the random seed for the population (0 = non-deterministic).
	PopulationSeed int64 `json:"population_seed,omitempty"`

	// UseDeterministicIDs enables counter-based IDs instead of UUIDs.
	// Produces reproducible strategy IDs across runs with the same seeds.
	UseDeterministicIDs bool `json:"use_deterministic_ids,omitempty"`

	// StrategyStore persists deployed strategies (optional, may be nil).
	StrategyStore StrategyStore `json:"-"`

	// MinMutationRate is the floor for adaptive mutation rate clamping.
	MinMutationRate float64 `json:"min_mutation_rate"`

	// MaxMutationRate is the ceiling for adaptive mutation rate clamping.
	MaxMutationRate float64 `json:"max_mutation_rate"`

	// MaxStagnantGenerations is the stagnation threshold for bottom-performer reset.
	MaxStagnantGenerations int `json:"max_stagnant_generations"`

	// DiversityThreshold minimum average pairwise distance before adaptive
	// mutation becomes more aggressive.
	DiversityThreshold float64 `json:"diversity_threshold"`

	// BreedingPoolRatio limits breeding to the top fraction of survivors.
	BreedingPoolRatio float64 `json:"breeding_pool_ratio"`

	// PromptCrossoverMode controls how PromptTemplate is combined during crossover.
	// 0 = PromptInherit (higher-scoring parent), 1 = PromptHalfSplit (half-sentence),
	// 2 = PromptUniform (random parent pick). Default is 0.
	PromptCrossoverMode int `json:"prompt_crossover_mode"`

	// Scorer is an optional function that evaluates strategy fitness after each
	// evolution cycle. When set, newly generated offspring receive valid scores
	// instead of the Score=-1 default, closing the scoring loop for the
	// scheduler-triggered path. When nil, the caller must score externally.
	Scorer func(*mutation.Strategy) float64 `json:"-"`

	// MaxLLMCallsPerGeneration is the LLM call budget per evolution generation.
	// When 0 or unset, LLM tier is disabled and all scoring uses heuristic.
	MaxLLMCallsPerGeneration int `json:"max_llm_calls_per_generation,omitempty"`

	// ScoreCacheSize is the maximum entries in the strategy score cache.
	// When 0, cache is unlimited.
	ScoreCacheSize int `json:"score_cache_size,omitempty"`

	// HeuristicScorer is a fast, cheap scoring function used as fallback
	// when LLM budget is exhausted or unavailable. If nil, defaults to
	// ConstantScorer(50.0).
	HeuristicScorer func(*mutation.Strategy) float64 `json:"-"`

	// PromptTemplates is the pool of prompt templates for prompt mutation.
	// When non-empty, the mutator can generate MutationPrompt type mutations
	// that swap the strategy's prompt template with alternatives from this pool.
	// Empty (default) means prompt mutation is disabled.
	PromptTemplates []string `json:"prompt_templates,omitempty"`

	// EnableExperienceGuidedMutation enables experience-guided mutation when
	// true AND a GuidanceProvider is configured. When hints are available,
	// the mutator biases its decisions toward patterns that worked in the past.
	EnableExperienceGuidedMutation bool `json:"enable_experience_guided_mutation,omitempty"`

	// GuidanceProvider provides evolution hints for guided mutation.
	// When EnableExperienceGuidedMutation is true and this is non-nil, the
	// mutator is wrapped with an ExperienceGuidedMutator that biases mutation
	// decisions using past experience data.
	GuidanceProvider GuidanceProvider `json:"-"`

	// Guardrails provides pre/post evolution safety checks (optional).
	// When set, the adapter runs guardrail checks before and after each
	// evolution cycle. Nil (default) means guardrails are disabled.
	Guardrails *EvolutionGuardrails `json:"-"`

	// HistoryMaxSize limits the number of per-generation history entries
	// stored for trajectory reporting. When > 0, each evolution cycle
	// appends a GenerationHistoryEntry to the population history (default 0 = disabled).
	HistoryMaxSize int `json:"history_max_size"`

	// MemoryAwareScoringConfig configures memory-aware scoring that extends the
	// tiered scorer with evidence-based bonuses and cost/latency penalties.
	// When MemoryAwareScoringConfig.Enabled is true and a MemoryExperienceProvider is
	// set, the tiered scorer is wrapped in a MemoryAwareScorer.
	MemoryAwareScoringConfig scoring.MemoryAwareScoringConfig `json:"memory_aware_scoring,omitempty"`

	// MemoryExperienceProvider provides access to past experiences for memory-aware
	// scoring. When set and MemoryAwareScoringConfig.Enabled is true, the scorer
	// adjusts fitness scores based on historical evidence.
	MemoryExperienceProvider scoring.ExperienceProvider `json:"-"`
}

// DefaultSystemConfig returns sensible defaults for a wired evolution system.
//
// Returns:
//
//	SystemConfig - configuration with default values.
func DefaultSystemConfig() SystemConfig {
	return SystemConfig{
		PopulationSize:         20,
		EliteCount:             3,
		MutationRate:           0.2,
		SurvivalRate:           0.6,
		EnableDreamCycle:       false,
		EnableScheduler:        false,
		MinTasksBeforeEvolve:   10,
		SchedulerTrigger:       TriggerOnIdle,
		MinMutationRate:        0.05,
		MaxMutationRate:        0.5,
		MaxStagnantGenerations: 10,
		DiversityThreshold:     0.15,
		BreedingPoolRatio:      0.6,
	}
}

// NewWiredEvolutionSystem creates a fully connected evolution system.
// It creates and wires together all components:
//
//  1. mutation.Mutator (production mutator)
//  2. genome.MutatorAdapter (genome-compatible wrapper)
//  3. genome.Crossover (crossover engine)
//  4. genome.Population (managed population)
//  5. GenomePopulationAdapter (scheduler-compatible runner)
//  6. PopulationGenealogyRecorder (lineage tracking)
//  7. MutationAdapter (dream-cycle-compatible mutator)
//  8. DreamCycle (orchestrator, optional)
//  9. EvolutionScheduler (event-driven trigger, optional)
//
// Args:
//
//	baseStrategy - the root strategy to evolve from (must not be nil).
//	cfg - system configuration (use DefaultSystemConfig() for sensible defaults).
//
// Returns:
//
//	*WiredEvolutionSystem - the fully wired system ready for use.
//	error - non-nil if any component creation or wiring fails.
func NewWiredEvolutionSystem(
	baseStrategy *mutation.Strategy,
	cfg SystemConfig,
) (*WiredEvolutionSystem, error) {
	if baseStrategy == nil {
		return nil, fmt.Errorf("base strategy must not be nil")
	}

	// Step 1: Create production mutator with optional seed, deterministic IDs,
	// and prompt template pool for prompt mutation support.
	var mutatorOpts []mutation.MutatorOption
	if cfg.MutatorSeed != 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithSeed(cfg.MutatorSeed))
	}
	if cfg.UseDeterministicIDs {
		mutatorOpts = append(mutatorOpts, mutation.WithDeterministicIDs(true))
	}
	if len(cfg.PromptTemplates) > 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithPromptPool(cfg.PromptTemplates))
	}
	rawMutator, err := mutation.NewMutator(mutatorOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mutator: %w", err)
	}

	// Step 1b: Optionally wrap with experience-guided mutation.
	// When enabled and a guidance provider is configured, the mutator
	// biases its decisions toward patterns that worked in the past.
	var mutatorForGenome genome.MutatorInterface = rawMutator
	if cfg.EnableExperienceGuidedMutation && cfg.GuidanceProvider != nil {
		guidedMutator, err := mutation.NewExperienceGuidedMutator(
			rawMutator,
			newGuidanceAdapter(cfg.GuidanceProvider),
		)
		if err != nil {
			return nil, fmt.Errorf("create guided mutator: %w", err)
		}
		mutatorForGenome = guidedMutator

		slog.Info("[WiredSystem] Experience-guided mutation enabled",
			"hint_provider", fmt.Sprintf("%T", cfg.GuidanceProvider),
		)
	}

	// Step 2: Wrap for genome compatibility.
	genomeMutator, err := NewGenomeMutatorAdapter(mutatorForGenome)
	if err != nil {
		return nil, fmt.Errorf("create genome mutator adapter: %w", err)
	}
	var crosserOpts []genome.CrossoverOption
	if cfg.CrossoverSeed != 0 {
		crosserOpts = append(crosserOpts, genome.WithSeed(cfg.CrossoverSeed))
	}
	if cfg.UseDeterministicIDs {
		crosserOpts = append(crosserOpts, genome.WithDeterministicIDs(true))
	}
	switch cfg.PromptCrossoverMode {
	case 1:
		crosserOpts = append(crosserOpts, genome.WithPromptMode(genome.PromptHalfSplit))
	case 2:
		crosserOpts = append(crosserOpts, genome.WithPromptMode(genome.PromptUniform))
	}
	crosser, err := genome.NewCrossover(crosserOpts...)
	if err != nil {
		return nil, fmt.Errorf("create crossover: %w", err)
	}

	// Step 4: Create genome population with optional seed and adaptive config.
	popOpts := []genome.PopulationOption{
		genome.WithPopulationSize(cfg.PopulationSize),
		genome.WithEliteCount(cfg.EliteCount),
		genome.WithMutationRate(cfg.MutationRate),
		genome.WithSurvivalRate(cfg.SurvivalRate),
		genome.WithMinMutationRate(cfg.MinMutationRate),
		genome.WithMaxMutationRate(cfg.MaxMutationRate),
		genome.WithMaxStagnantGenerations(cfg.MaxStagnantGenerations),
		genome.WithDiversityThreshold(cfg.DiversityThreshold),
		genome.WithBreedingPoolRatio(cfg.BreedingPoolRatio),
	}
	if cfg.HistoryMaxSize > 0 {
		popOpts = append(popOpts, genome.WithHistoryEnabled(cfg.HistoryMaxSize))
	}
	if cfg.PopulationSeed != 0 {
		popOpts = append(popOpts, genome.WithPopulationSeed(cfg.PopulationSeed))
	}
	pop, err := genome.NewPopulation(
		context.Background(),
		baseStrategy,
		genomeMutator,
		popOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("create population: %w", err)
	}

	// Step 5: Build adapter options (scorer + optional tiered scoring pipeline).
	var adapterOpts []GenomeAdapterOption
	if cfg.Scorer != nil {
		adapterOpts = append(adapterOpts, WithAdapterScorer(cfg.Scorer))
	}

	// Step 5b: Optionally create tiered scorer pipeline for cost-controlled scoring.
	if cfg.MaxLLMCallsPerGeneration > 0 || cfg.HeuristicScorer != nil {
		cacheSize := cfg.ScoreCacheSize
		if cacheSize <= 0 {
			cacheSize = 0 // unlimited
		}
		scoreCache := scoring.NewScoreCache(cacheSize)

		budget, err := scoring.NewBudget(cfg.MaxLLMCallsPerGeneration)
		if err != nil {
			return nil, fmt.Errorf("create scoring budget: %w", err)
		}

		heuristic := cfg.HeuristicScorer
		if heuristic == nil {
			heuristic = genome.ConstantScorer(50.0)
		}

		// LLM scorer is the configured adapter-level scorer (may be nil).
		var llmScorer genome.ScorerFunc
		if cfg.Scorer != nil {
			llmScorer = cfg.Scorer
		}

		tiered, err := scoring.NewTieredScorer(scoring.TieredScorerConfig{
			Cache:           scoreCache,
			Budget:          budget,
			HeuristicScorer: heuristic,
			LLMScorer:       llmScorer,
		})
		if err != nil {
			return nil, fmt.Errorf("create tiered scorer: %w", err)
		}

		// Optionally wrap tiered scorer with memory-aware scoring.
		if cfg.MemoryAwareScoringConfig.Enabled && cfg.MemoryExperienceProvider != nil {
			masCfg := cfg.MemoryAwareScoringConfig
			memScorer, err := scoring.NewMemoryAwareScorer(tiered, cfg.MemoryExperienceProvider, masCfg)
			if err != nil {
				return nil, fmt.Errorf("create memory-aware scorer: %w", err)
			}
			adapterOpts = append(adapterOpts, WithAdapterMemoryAwareScoring(memScorer))

			// Also set tiered scoring so the adapter's Run() method has access
			// to the tiered scorer (budget, cache, LLM tracking, etc.).
			// The memory-aware scorer wraps the tiered scorer, so both
			// evidence-based adjustments and tiered scoring stats are preserved
			// with proper context propagation from Run().
			adapterOpts = append(adapterOpts, WithAdapterTieredScoring(tiered, budget, scoreCache))

			slog.Info("[WiredSystem] memory-aware scoring enabled",
				"memory_weight", masCfg.MemoryWeight,
				"cost_weight", masCfg.CostWeight,
				"latency_weight", masCfg.LatencyWeight,
			)
		} else {
			adapterOpts = append(adapterOpts, WithAdapterTieredScoring(tiered, budget, scoreCache))
		}
	}

	// Step 5c: Optionally attach guardrails for pre/post evolution safety checks.
	if cfg.Guardrails != nil {
		adapterOpts = append(adapterOpts, WithAdapterGuardrails(cfg.Guardrails))
	}

	popAdapter, err := NewGenomePopulationAdapter(pop, genomeMutator, crosser, adapterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create population adapter: %w", err)
	}

	// Step 6: Create genealogy recorder.
	genealogy := NewPopulationGenealogyRecorder()

	system := &WiredEvolutionSystem{
		PopAdapter: popAdapter,
		Population: pop,
		Genealogy:  genealogy,
	}

	// Step 7: Attach optional strategy store.
	if cfg.StrategyStore != nil {
		system.StrategyStore = cfg.StrategyStore
	}

	// Step 8: Optionally create dream cycle with evolution-layer adapters.
	if cfg.EnableDreamCycle {
		mutationAdapter, err := NewMutationAdapter(rawMutator)
		if err != nil {
			return nil, fmt.Errorf("create dream cycle mutator adapter: %w", err)
		}

		dreamCycle, err := NewDreamCycle(
			nil, // Scheduler attached later if needed.
			mutationAdapter,
			nil, // Tester requires arena integration; use nil for now.
			genealogy,
			WithDreamCycleConfig(DreamCycleConfig{
				Enabled:              true,
				MinTasksBeforeEvolve: cfg.MinTasksBeforeEvolve,
				MaxMutations:         3,
				MinWinRate:           0.55,
				Cooldown:             5 * time.Minute,
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("create dream cycle: %w", err)
		}
		system.DreamCycle = dreamCycle
	}

	// Step 9: Optionally create scheduler with callback registration.
	if cfg.EnableScheduler && cfg.Callbacks != nil {
		scheduler := NewEvolutionScheduler(
			cfg.Callbacks,
			popAdapter,
			WithTrigger(cfg.SchedulerTrigger),
			WithEnabled(true),
		)

		// Attach dream cycle if available.
		if system.DreamCycle != nil {
			scheduler.SetDreamCycle(system.DreamCycle)
			system.DreamCycle.scheduler = scheduler
		}

		system.Scheduler = scheduler
	}

	slog.Info("[WiredSystem] Evolution system created and wired",
		"population_size", cfg.PopulationSize,
		"elite_count", cfg.EliteCount,
		"mutation_rate", cfg.MutationRate,
		"dream_cycle_enabled", cfg.EnableDreamCycle,
		"scheduler_enabled", cfg.EnableScheduler,
	)

	return system, nil
}

// RunIdleEvolution performs N idle evolution cycles on the wired system.
// This is the primary entry point for zero-cost background evolution.
//
// Args:
//
//	ctx - operation context for cancellation.
//	system - the wired evolution system to run.
//	generations - number of generations to evolve.
//
// Returns:
//
//	error - non-nil if any evolution cycle fails.
func RunIdleEvolution(
	ctx context.Context,
	system *WiredEvolutionSystem,
	generations int,
) error {
	if system == nil || system.PopAdapter == nil {
		return fmt.Errorf("system or population adapter is nil")
	}

	for i := 0; i < generations; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := system.PopAdapter.Run(ctx); err != nil {
			return fmt.Errorf("idle evolution generation %d: %w", i+1, err)
		}
		// Note: Post-scoring is now handled atomically inside PopAdapter.Run()
		// via EvolveAfterScoring. No redundant manual scoring needed here.

		// Record lineage after each evolution cycle.
		_, gen := system.Population.Snapshot()
		prevGen := gen - 1
		if prevGen >= 0 {
			_, err := RecordPopulationLineage(ctx, system.Population, system.Genealogy, prevGen)
			if err != nil {
				slog.WarnContext(ctx, "lineage recording failed",
					"generation", prevGen, "error", err)
			}
		}
	}
	return nil
}

// BestStrategyFromSystem returns the best strategy from the wired system's population.
// This is the deployment-ready strategy after evolution completes.
//
// Args:
//
//	system - the wired evolution system.
//
// Returns:
//
//	*mutation.Strategy - cloned best strategy, or nil if empty.
//	error - non-nil if system is nil.
func BestStrategyFromSystem(system *WiredEvolutionSystem) (*mutation.Strategy, error) {
	if system == nil || system.Population == nil {
		return nil, fmt.Errorf("system or population is nil")
	}
	best := system.Population.BestStrategy()
	if best == nil {
		return nil, fmt.Errorf("population has no strategies")
	}
	return best, nil
}

// Deprecated: Kept for backward compatibility with tests.
// RegisterScheduler registers the wired system's scheduler callback handlers.
//
// Args:
//
//	system - the wired evolution system whose scheduler should be registered.
//
// Returns:
//
//	error - non-nil if the scheduler is nil.
func RegisterScheduler(system *WiredEvolutionSystem) error {
	if system == nil || system.Scheduler == nil {
		return fmt.Errorf("system or scheduler is nil")
	}
	system.Scheduler.Register()
	return nil
}

// Shutdown gracefully stops the wired evolution system.
// It cancels pending evolution goroutines and releases resources.
//
// Args:
//
//	system - the wired evolution system to shut down.
func Shutdown(system *WiredEvolutionSystem) {
	if system == nil {
		return
	}
	if system.Scheduler != nil {
		system.Scheduler.Shutdown()
	}
	slog.Info("[WiredSystem] Evolution system shut down")
}

// guidanceAdapter bridges evolution.GuidanceProvider to mutation.HintProvider.
// It converts between the evolution and mutation package's EvolutionHint and
// StrategyOutcome types, which have identical field definitions.
type guidanceAdapter struct {
	provider GuidanceProvider
}

// newGuidanceAdapter creates a mutation-compatible hint provider from an
// evolution-level GuidanceProvider.
//
// Args:
//
//	provider - the evolution-level guidance provider (must not be nil).
//
// Returns:
//
//	mutation.HintProvider - the adapted provider for the mutation package.
func newGuidanceAdapter(provider GuidanceProvider) mutation.HintProvider {
	return &guidanceAdapter{provider: provider}
}

// HintsForTask delegates to the wrapped provider and converts EvolutionHint types.
//
// Args:
//
//	ctx - operation context.
//	taskType - the task type to get hints for.
//	limit - maximum number of hints to return.
//
// Returns:
//
//	[]mutation.EvolutionHint - the converted hints.
//	error - delegation error from the wrapped provider.
func (a *guidanceAdapter) HintsForTask(
	ctx context.Context,
	taskType string,
	limit int,
) ([]mutation.EvolutionHint, error) {
	hints, err := a.provider.HintsForTask(ctx, taskType, limit)
	if err != nil {
		return nil, err
	}

	result := make([]mutation.EvolutionHint, len(hints))
	for i, h := range hints {
		var paramHints map[string]float64
		if h.ParamHints != nil {
			paramHints = make(map[string]float64, len(h.ParamHints))
			for k, v := range h.ParamHints {
				paramHints[k] = v
			}
		}

		sourceIDs := make([]string, len(h.SourceExperienceIDs))
		copy(sourceIDs, h.SourceExperienceIDs)

		constraints := make([]string, len(h.Constraints))
		copy(constraints, h.Constraints)

		failedPatterns := make([]string, len(h.FailedPatterns))
		copy(failedPatterns, h.FailedPatterns)

		preferredTools := make([]string, len(h.PreferredTools))
		copy(preferredTools, h.PreferredTools)

		promptSnippets := make([]string, len(h.PromptSnippets))
		copy(promptSnippets, h.PromptSnippets)

		result[i] = mutation.EvolutionHint{
			ID:                  h.ID,
			TaskType:            h.TaskType,
			Problem:             h.Problem,
			Solution:            h.Solution,
			Constraints:         constraints,
			FailedPatterns:      failedPatterns,
			PreferredTools:      preferredTools,
			PromptSnippets:      promptSnippets,
			ParamHints:          paramHints,
			Confidence:          h.Confidence,
			SourceExperienceIDs: sourceIDs,
		}
	}

	return result, nil
}

// RecordStrategyOutcome delegates to the wrapped provider and converts types.
//
// Args:
//
//	ctx - operation context.
//	outcome - the mutation-level strategy outcome to record.
//
// Returns:
//
//	error - delegation error from the wrapped provider.
func (a *guidanceAdapter) RecordStrategyOutcome(
	ctx context.Context,
	outcome mutation.StrategyOutcome,
) error {
	evoOutcome := StrategyOutcome{
		StrategyID:   outcome.StrategyID,
		TaskType:     outcome.TaskType,
		Success:      outcome.Success,
		Score:        outcome.Score,
		Cost:         outcome.Cost,
		LatencyMs:    outcome.LatencyMs,
		MutationType: outcome.MutationType,
		Timestamp:    outcome.Timestamp,
	}

	if len(outcome.ExperienceIDs) > 0 {
		evoOutcome.ExperienceIDs = make([]string, len(outcome.ExperienceIDs))
		copy(evoOutcome.ExperienceIDs, outcome.ExperienceIDs)
	}

	return a.provider.RecordStrategyOutcome(ctx, evoOutcome)
}
