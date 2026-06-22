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

	"goagentx/internal/callbacks"
	"goagentx/internal/evolution/genome"
	"goagentx/internal/evolution/mutation"
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

// Run executes one genome evolution cycle (EvolveOnIdle) when triggered by scheduler.
// After evolution, if a scorer is configured, all unevaluated agents (IsScoreEvaluated() == false)
// receive a fitness score — closing the scoring loop for the scheduler path.
//
// Args:
//
//	ctx - operation context for cancellation.
//
// Returns:
//
//	error - non-nil if evolution fails.
func (a *GenomePopulationAdapter) Run(ctx context.Context) error {
	if err := a.pop.EvolveOnIdle(ctx, a.mutator, a.crosser); err != nil {
		return fmt.Errorf("genome evolve on idle: %w", err)
	}

	// Score newly generated offspring so next generation's selection
	// operates on valid fitness values instead of Score=-1 defaults.
	if a.scorer != nil {
		a.pop.ScoreAgents(func(agent *mutation.Strategy) float64 {
			if !genome.IsScoreEvaluated(agent.Score) {
				return a.scorer(agent)
			}
			return agent.Score
		})
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

// Population returns the underlying genome population for direct access.
//
// Returns:
//
//	*genome.Population - the managed population.
func (a *GenomePopulationAdapter) Population() *genome.Population {
	return a.pop
}

// GenomeMutatorAdapter wraps a *mutation.Mutator to implement genome.MutatorInterface.
// This enables genome.Population to use the production mutator directly.
type GenomeMutatorAdapter struct {
	mutator *mutation.Mutator
}

// NewGenomeMutatorAdapter creates a genome-compatible mutator adapter.
//
// Args:
//
//	m - the production mutator to wrap (must not be nil).
//
// Returns:
//
//	*GenomeMutatorAdapter - the adapter instance.
//	error - non-nil if mutator is nil.
func NewGenomeMutatorAdapter(m *mutation.Mutator) (*GenomeMutatorAdapter, error) {
	if m == nil {
		return nil, fmt.Errorf("mutator must not be nil")
	}
	return &GenomeMutatorAdapter{mutator: m}, nil
}

// Mutate delegates to the wrapped mutation.Mutator.
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

	// Step 1: Create production mutator with optional seed and deterministic IDs.
	var mutatorOpts []mutation.MutatorOption
	if cfg.MutatorSeed != 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithSeed(cfg.MutatorSeed))
	}
	if cfg.UseDeterministicIDs {
		mutatorOpts = append(mutatorOpts, mutation.WithDeterministicIDs(true))
	}
	rawMutator, err := mutation.NewMutator(mutatorOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mutator: %w", err)
	}

	// Step 2: Wrap for genome compatibility.
	genomeMutator, err := NewGenomeMutatorAdapter(rawMutator)
	if err != nil {
		return nil, fmt.Errorf("create genome mutator adapter: %w", err)
	}

	// Step 3: Create crossover engine with optional seed and deterministic IDs.
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

	// Step 5: Create scheduler-compatible adapter with optional scorer.
	var adapterOpts []GenomeAdapterOption
	if cfg.Scorer != nil {
		adapterOpts = append(adapterOpts, WithAdapterScorer(cfg.Scorer))
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

		// Record lineage after each evolution cycle.
		// Use Snapshot() for thread-safe read of population state.
		_, gen := system.Population.Snapshot()
		prevGen := gen - 1
		if prevGen >= 0 {
			_, err := RecordPopulationLineage(ctx, system.Population, system.Genealogy, prevGen)
			if err != nil {
				slog.WarnContext(ctx, "[WiredSystem] Failed to record lineage",
					"generation", i+1,
					"error", err,
				)
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
