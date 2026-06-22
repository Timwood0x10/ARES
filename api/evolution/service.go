// Package evolution provides a high-level API for autonomous genetic algorithm
// based strategy evolution. It wraps internal evolution components into a clean,
// import-and-use abstraction.
package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"goagentx/internal/evolution"
	"goagentx/internal/evolution/genome"
	"goagentx/internal/evolution/mutation"
)

// Service provides high-level genetic algorithm evolution operations.
// It wraps either a fully wired evolution system (WiredEvolutionSystem) or
// a raw population with mutator and crosser, exposing a simple API for
// running evolution, retrieving results, and managing lifecycle.
type Service struct {
	wiredSystem *evolution.WiredEvolutionSystem
	population  *genome.Population
	mutator     *mutation.Mutator
	crosser     *genome.Crossover
	config      *SystemConfig
}

// NewService creates a new evolution service instance with the given configuration.
//
// Args:
//
//	cfg - service configuration (use DefaultConfig() for sensible defaults).
//
// Returns:
//
//	*Service - the initialized evolution service instance.
//	error - ErrNilConfig if cfg is nil, ErrNilBaseStrategy if base strategy is nil,
//	        ErrInvalidRate if any rate is outside [0,1], or internal creation error.
func NewService(cfg *SystemConfig) (*Service, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	if cfg.BaseStrategy == nil {
		return nil, ErrNilBaseStrategy
	}

	if cfg.SurvivalRate < 0 || cfg.SurvivalRate > 1 {
		return nil, fmt.Errorf("%w: survival_rate=%v", ErrInvalidRate, cfg.SurvivalRate)
	}
	if cfg.MutationRate < 0 || cfg.MutationRate > 1 {
		return nil, fmt.Errorf("%w: mutation_rate=%v", ErrInvalidRate, cfg.MutationRate)
	}
	if cfg.MinMutationRate < 0 || cfg.MinMutationRate > 1 {
		return nil, fmt.Errorf("%w: min_mutation_rate=%v", ErrInvalidRate, cfg.MinMutationRate)
	}
	if cfg.MaxMutationRate < 0 || cfg.MaxMutationRate > 1 {
		return nil, fmt.Errorf("%w: max_mutation_rate=%v", ErrInvalidRate, cfg.MaxMutationRate)
	}
	if cfg.MinMutationRate > cfg.MaxMutationRate {
		return nil, fmt.Errorf("min_mutation_rate=%v > max_mutation_rate=%v", cfg.MinMutationRate, cfg.MaxMutationRate)
	}
	if cfg.BreedingPoolRatio < 0 || cfg.BreedingPoolRatio > 1 {
		return nil, fmt.Errorf("%w: breeding_pool_ratio=%v", ErrInvalidRate, cfg.BreedingPoolRatio)
	}

	s := &Service{
		config: cfg,
	}

	if cfg.EnableWiredMode {
		wired, err := s.createWiredSystem(cfg)
		if err != nil {
			return nil, fmt.Errorf("create wired system: %w", err)
		}
		s.wiredSystem = wired
	} else {
		pop, mut, cross, err := s.createRawComponents(cfg)
		if err != nil {
			return nil, fmt.Errorf("create raw components: %w", err)
		}
		s.population = pop
		s.mutator = mut
		s.crosser = cross
	}

	slog.Info("evolution service created",
		"population_size", cfg.PopulationSize,
		"elite_count", cfg.EliteCount,
		"wired_mode", cfg.EnableWiredMode,
	)

	return s, nil
}

// createWiredSystem creates a fully wired evolution system from the API config.
func (s *Service) createWiredSystem(cfg *SystemConfig) (*evolution.WiredEvolutionSystem, error) {
	baseStrategy := toInternalStrategy(cfg.BaseStrategy)

	internalCfg := evolution.DefaultSystemConfig()
	internalCfg.PopulationSize = cfg.PopulationSize
	internalCfg.EliteCount = cfg.EliteCount
	internalCfg.SurvivalRate = cfg.SurvivalRate
	internalCfg.MutationRate = cfg.MutationRate
	internalCfg.MinMutationRate = cfg.MinMutationRate
	internalCfg.MaxMutationRate = cfg.MaxMutationRate
	internalCfg.MaxStagnantGenerations = cfg.MaxStagnantGenerations
	internalCfg.DiversityThreshold = cfg.DiversityThreshold
	internalCfg.BreedingPoolRatio = cfg.BreedingPoolRatio
	internalCfg.MutatorSeed = cfg.Seed
	internalCfg.CrossoverSeed = cfg.Seed
	internalCfg.PopulationSeed = cfg.Seed
	if cfg.Seed != 0 {
		internalCfg.UseDeterministicIDs = true
	}
	if cfg.UseDeterministicIDs != nil {
		internalCfg.UseDeterministicIDs = *cfg.UseDeterministicIDs
	}

	system, err := evolution.NewWiredEvolutionSystem(baseStrategy, internalCfg)
	if err != nil {
		return nil, fmt.Errorf("new wired evolution system: %w", err)
	}

	return system, nil
}

// createRawComponents creates raw population, mutator, and crosser (non-wired mode).
func (s *Service) createRawComponents(cfg *SystemConfig) (*genome.Population, *mutation.Mutator, *genome.Crossover, error) {
	baseStrategy := toInternalStrategy(cfg.BaseStrategy)

	useDetIDs := cfg.Seed != 0
	if cfg.UseDeterministicIDs != nil {
		useDetIDs = *cfg.UseDeterministicIDs
	}

	var mutatorOpts []mutation.MutatorOption
	if cfg.Seed != 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithSeed(cfg.Seed))
	}
	if useDetIDs {
		mutatorOpts = append(mutatorOpts, mutation.WithDeterministicIDs(true))
	}
	if len(cfg.PromptPool) > 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithPromptPool(cfg.PromptPool))
	}

	rawMutator, err := mutation.NewMutator(mutatorOpts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new mutator: %w", err)
	}

	genomeMutator, err := evolution.NewGenomeMutatorAdapter(rawMutator)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new genome mutator adapter: %w", err)
	}

	var crosserOpts []genome.CrossoverOption
	if cfg.Seed != 0 {
		crosserOpts = append(crosserOpts, genome.WithSeed(cfg.Seed))
	}
	if useDetIDs {
		crosserOpts = append(crosserOpts, genome.WithDeterministicIDs(true))
	}

	crosser, err := genome.NewCrossover(crosserOpts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new crossover: %w", err)
	}

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
	if cfg.Seed != 0 {
		popOpts = append(popOpts, genome.WithPopulationSeed(cfg.Seed))
	}

	pop, err := genome.NewPopulation(context.Background(), baseStrategy, genomeMutator, popOpts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new population: %w", err)
	}

	return pop, rawMutator, crosser, nil
}

// Evolve runs N generations of evolution and returns the complete result.
// This is the primary entry point for executing evolution cycles.
//
// Args:
//
//	ctx - operation context for cancellation.
//	generations - number of generations to run (overrides config.Generations if > 0).
//
// Returns:
//
//	*EvolutionResult - the result containing best strategy, per-generation stats, and lineages.
//	error - ErrNotInitialized if system not ready, or evolution execution error.
func (s *Service) Evolve(ctx context.Context, generations int) (*EvolutionResult, error) {
	if generations <= 0 {
		generations = s.config.Generations
	}

	result := &EvolutionResult{
		Stats:    make([]Stats, 0, generations),
		Lineages: make([]StrategyLineage, 0),
	}

	// Initialize scores before first generation so selection has meaningful data.
	s.initScores(s.config.Seed)

	for i := 0; i < generations; i++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		if s.wiredSystem != nil {
			if err := evolution.RunIdleEvolution(ctx, s.wiredSystem, 1); err != nil {
				return nil, fmt.Errorf("evolve generation %d: %w", i+1, err)
			}

			stats := s.collectStats()
			result.Stats = append(result.Stats, stats)

			lineages := s.collectLineages()
			result.Lineages = append(result.Lineages, lineages...)
		} else if s.population != nil && s.mutator != nil && s.crosser != nil {
			mutAdapter, err := s.genomeMutatorAdapter()
			if err != nil {
				return nil, fmt.Errorf("get mutator adapter gen %d: %w", i+1, err)
			}
			if err := s.population.EvolveOnIdle(ctx, mutAdapter, s.crosser); err != nil {
				return nil, fmt.Errorf("evolve generation %d: %w", i+1, err)
			}

			// Re-score after each evolution so next generation selects on fresh data.
			s.initScores(0)

			stats := s.collectStats()
			result.Stats = append(result.Stats, stats)
		} else {
			return nil, ErrNotInitialized
		}

		result.TotalGens++
	}

	best, err := s.BestStrategy()
	if err != nil {
		slog.WarnContext(ctx, "failed to get best strategy after evolution", "error", err)
	} else {
		result.BestStrategy = best
	}

	slog.InfoContext(ctx, "evolution completed",
		"total_generations", result.TotalGens,
	)

	return result, nil
}

// BestStrategy returns the current best strategy from the evolution system.
//
// Returns:
//
//	*Strategy - cloned best strategy, or error if not available.
//	error - ErrNotInitialized if no system is active.
func (s *Service) BestStrategy() (*Strategy, error) {
	if s.wiredSystem != nil {
		internalBest, err := evolution.BestStrategyFromSystem(s.wiredSystem)
		if err != nil {
			return nil, fmt.Errorf("get best strategy from wired system: %w", err)
		}
		return toAPIStrategy(internalBest), nil
	}

	if s.population != nil {
		internalBest := s.population.BestStrategy()
		if internalBest == nil {
			return nil, fmt.Errorf("population has no strategies")
		}
		return toAPIStrategy(internalBest), nil
	}

	return nil, ErrNotInitialized
}

// Stats returns current population statistics.
//
// Returns:
//
//	*Stats - snapshot of population statistics.
//	error - ErrNotInitialized if no system is active.
func (s *Service) Stats() (*Stats, error) {
	if s.wiredSystem != nil && s.wiredSystem.Population != nil {
		stats := collectStatsFromPopulation(s.wiredSystem.Population)
		return &stats, nil
	}

	if s.population != nil {
		stats := collectStatsFromPopulation(s.population)
		return &stats, nil
	}

	return nil, ErrNotInitialized
}

// Lineages returns all recorded strategy lineages from evolution history.
//
// Returns:
//
//	[]StrategyLineage - copy of recorded lineages.
//	error - ErrNotInitialized if no system is active.
func (s *Service) Lineages() ([]StrategyLineage, error) {
	if s.wiredSystem != nil && s.wiredSystem.Genealogy != nil {
		internalLineages := s.wiredSystem.Genealogy.Lineages()
		apiLineages := make([]StrategyLineage, 0, len(internalLineages))
		for _, l := range internalLineages {
			apiLineages = append(apiLineages, toAPILineage(l))
		}
		return apiLineages, nil
	}

	return []StrategyLineage{}, nil
}

// Shutdown gracefully stops the evolution system and releases resources.
// It is safe to call Shutdown multiple times; subsequent calls are no-ops.
func (s *Service) Shutdown() {
	if s.wiredSystem != nil {
		evolution.Shutdown(s.wiredSystem)
	}
	slog.Info("evolution service shut down")
}

// --- Internal helpers ---

// toAPIStrategy converts an internal mutation.Strategy to the public API Strategy type.
func toAPIStrategy(s *mutation.Strategy) *Strategy {
	if s == nil {
		return nil
	}
	return &Strategy{
		ID:             s.ID,
		Name:           s.Name,
		Version:        s.Version,
		Params:         mutation.CloneParams(s.Params),
		ParentID:       s.ParentID,
		PromptTemplate: s.PromptTemplate,
		MutationType:   s.StrategyMutationType.String(),
		Score:          s.Score,
		CreatedAt:      s.CreatedAt,
	}
}

// toInternalStrategy converts an API Strategy to an internal mutation.Strategy.
func toInternalStrategy(s *Strategy) *mutation.Strategy {
	if s == nil {
		return nil
	}
	return &mutation.Strategy{
		ID:                   s.ID,
		Name:                 s.Name,
		Version:              s.Version,
		Params:               mutation.CloneParams(s.Params),
		ParentID:             s.ParentID,
		PromptTemplate:       s.PromptTemplate,
		StrategyMutationType: mutation.ParseMutationType(s.MutationType),
		Score:                s.Score,
		CreatedAt:            s.CreatedAt,
	}
}

// toAPILineage converts an internal evolution.StrategyLineage to the public API type.
func toAPILineage(l evolution.StrategyLineage) StrategyLineage {
	return StrategyLineage{
		ParentID:     l.ParentID,
		ChildID:      l.ChildID,
		MutationType: l.MutationType,
		WinRate:      l.WinRate,
		ScoreDelta:   l.ScoreImprovement,
		Timestamp:    l.Timestamp,
	}
}

// genomeMutatorAdapter creates a genome-compatible mutator adapter from the raw mutator.
//
// Returns:
//
//	genome.MutatorInterface - the adapter instance.
//	error - non-nil if adapter creation fails.
func (s *Service) genomeMutatorAdapter() (genome.MutatorInterface, error) {
	if s.mutator == nil {
		return nil, ErrNotInitialized
	}
	adapter, err := evolution.NewGenomeMutatorAdapter(s.mutator)
	if err != nil {
		return nil, fmt.Errorf("create genome mutator adapter: %w", err)
	}
	return adapter, nil
}

// collectStats gathers current statistics from the active system.
func (s *Service) collectStats() Stats {
	if s.wiredSystem != nil && s.wiredSystem.Population != nil {
		return collectStatsFromPopulation(s.wiredSystem.Population)
	}
	if s.population != nil {
		return collectStatsFromPopulation(s.population)
	}
	return Stats{}
}

// collectStatsFromPopulation extracts stats from a genome.Population into API Stats.
func collectStatsFromPopulation(pop *genome.Population) Stats {
	ps := pop.Stats()
	return Stats{
		Generation: ps.Generation,
		Size:       ps.Size,
		BestScore:  ps.BestScore,
		AvgScore:   ps.AvgScore,
		WorstScore: ps.WorstScore,
	}
}

// collectLineages gathers all recorded lineages from the genealogy recorder.
func (s *Service) collectLineages() []StrategyLineage {
	if s.wiredSystem != nil && s.wiredSystem.Genealogy != nil {
		internal := s.wiredSystem.Genealogy.Lineages()
		result := make([]StrategyLineage, 0, len(internal))
		for _, l := range internal {
			result = append(result, toAPILineage(l))
		}
		return result
	}
	return []StrategyLineage{}
}

// Default scorer constants used by scoreAgents when no custom scorer is configured.
const (
	defaultTargetTemp    = 0.7
	defaultProximityGain = 2.5
	defaultBaseScore     = 50.0
	defaultNoiseRange    = 30.0
	defaultBonusRange    = 20.0
	defaultMaxScore      = 100.0
)

// scoreAgents assigns fitness scores to agents in the population.
// If s.config.Scorer is set, it delegates to that. Otherwise it uses a
// temperature-proximity heuristic (target=0.7, base=50 + noise + proximity bonus).
func (s *Service) scoreAgents(pop *genome.Population, rng *rand.Rand) {
	pop.ScoreAgents(func(agent *mutation.Strategy) float64 {
		if s.config.Scorer != nil {
			return s.config.Scorer(agent.Params)
		}
		temp := defaultTargetTemp
		if v, ok := agent.Params["temperature"].(float64); ok {
			temp = v
		}
		proximity := 1 - absFloat64(temp-defaultTargetTemp)*defaultProximityGain
		score := defaultBaseScore + rng.Float64()*defaultNoiseRange + proximity*defaultBonusRange
		if score > defaultMaxScore {
			score = defaultMaxScore
		}
		if score < 0 {
			score = 0
		}
		return score
	})
}

func absFloat64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// initScores initializes scores for all agents in the population using the provided seed.
// If seed is 0, the current time is used as seed.
func (s *Service) initScores(seed int64) {
	var r *rand.Rand
	if seed != 0 {
		r = rand.New(rand.NewSource(seed)) // #nosec G404 - scoring doesn't need crypto rand
	} else {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	if s.wiredSystem != nil && s.wiredSystem.Population != nil {
		s.scoreAgents(s.wiredSystem.Population, r)
	} else if s.population != nil {
		s.scoreAgents(s.population, r)
	}
}
