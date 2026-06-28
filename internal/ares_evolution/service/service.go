// Package evolution provides a high-level API for autonomous genetic algorithm
// based strategy evolution. It wraps internal evolution components into a clean,
// import-and-use abstraction.
package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/scoring"
)

const (
	// concurrentScoreLimit is the maximum number of concurrent LLM scoring calls.
	concurrentScoreLimit = 15

	// maxLineages caps the total recorded lineage entries to prevent
	// unbounded memory growth during long-running evolution sessions.
	maxLineages = 1000
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

	// lineages tracks recorded parent-child lineages for non-wired mode.
	lineages []StrategyLineage
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
	internalCfg.PromptCrossoverMode = cfg.PromptCrossoverMode

	// Thread the API-level scorer into the wired system's adapter so the
	// scheduler path automatically scores new offspring after each generation.
	// This ensures both Service.Evolve and scheduler-triggered paths have
	// a closed scoring loop.
	if cfg.Scorer != nil {
		apiScorer := cfg.Scorer
		internalCfg.Scorer = func(agent *mutation.Strategy) float64 {
			return apiScorer(toAPIStrategy(agent))
		}
	}

	// Wire guardrails when enabled.
	if cfg.Guardrails != nil && cfg.Guardrails.Enabled {
		var guardrailOpts []evolution.GuardrailOption
		if cfg.Guardrails.BaselineScore > 0 {
			guardrailOpts = append(guardrailOpts, evolution.WithBaselineScore(cfg.Guardrails.BaselineScore))
		}
		if cfg.Guardrails.MaxStagnantGenerations > 0 {
			guardrailOpts = append(guardrailOpts, evolution.WithMaxStagnantGenerations(cfg.Guardrails.MaxStagnantGenerations))
		}
		if cfg.Guardrails.MaxLineageShare > 0 {
			guardrailOpts = append(guardrailOpts, evolution.WithMaxLineageShare(cfg.Guardrails.MaxLineageShare))
		}
		guardrails, err := evolution.NewEvolutionGuardrails(guardrailOpts...)
		if err != nil {
			return nil, fmt.Errorf("new evolution guardrails: %w", err)
		}
		internalCfg.Guardrails = guardrails
	}

	// Wire guidance provider for experience-guided mutation.
	if cfg.GuidanceProvider != nil {
		internalCfg.GuidanceProvider = &apiGuidanceBridge{provider: cfg.GuidanceProvider}
		internalCfg.EnableExperienceGuidedMutation = cfg.EnableExperienceGuidedMutation
	}

	// Wire memory experience provider for memory-aware scoring.
	if cfg.MemoryExperienceProvider != nil {
		internalCfg.MemoryExperienceProvider = &apiMemoryBridge{provider: cfg.MemoryExperienceProvider}
	}

	// Map memory-aware scoring config.
	if cfg.MemoryAwareScoringConfig.Enabled {
		internalCfg.MemoryAwareScoringConfig = scoring.MemoryAwareScoringConfig{
			Enabled:               cfg.MemoryAwareScoringConfig.Enabled,
			MemoryWeight:          cfg.MemoryAwareScoringConfig.MemoryWeight,
			CostWeight:            cfg.MemoryAwareScoringConfig.CostWeight,
			LatencyWeight:         cfg.MemoryAwareScoringConfig.LatencyWeight,
			RegressionWeight:      cfg.MemoryAwareScoringConfig.RegressionWeight,
			MinEvidenceBonus:      cfg.MemoryAwareScoringConfig.MinEvidenceBonus,
			MaxEvidenceBonus:      cfg.MemoryAwareScoringConfig.MaxEvidenceBonus,
			ExperienceLookupLimit: cfg.MemoryAwareScoringConfig.ExperienceLookupLimit,
		}
	}

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
	switch cfg.PromptCrossoverMode {
	case 1:
		crosserOpts = append(crosserOpts, genome.WithPromptMode(genome.PromptHalfSplit))
	case 2:
		crosserOpts = append(crosserOpts, genome.WithPromptMode(genome.PromptUniform))
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
	s.initScores()

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

			// Re-score after each evolution so next generation selects on fresh data.
			s.initScores()

			stats := s.collectStats()
			result.Stats = append(result.Stats, stats)

			lineages := s.collectLineages()
			result.Lineages = append(result.Lineages, lineages...)
		} else if s.population != nil && s.mutator != nil && s.crosser != nil {
			prevSnapshot, _ := s.population.Snapshot()
			prevBest := bestFromStrategies(prevSnapshot)

			mutAdapter, err := s.genomeMutatorAdapter()
			if err != nil {
				return nil, fmt.Errorf("get mutator adapter gen %d: %w", i+1, err)
			}
			if err := s.population.EvolveOnIdle(ctx, mutAdapter, s.crosser); err != nil {
				return nil, fmt.Errorf("evolve generation %d: %w", i+1, err)
			}

			// Re-score after each evolution so next generation selects on fresh data.
			s.initScores()

			// Record lineages for non-wired mode: link parent→child.
			s.recordGenealogy(prevBest)

			// Record lineages for non-wired mode: track each offspring's parent-child relationship.
			s.recordLineages()

			stats := s.collectStats()
			result.Stats = append(result.Stats, stats)
			result.Lineages = append(result.Lineages, s.lineages...)
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
	// Non-wired mode: return tracked lineages.
	result := make([]StrategyLineage, len(s.lineages))
	copy(result, s.lineages)
	return result, nil
}

// Shutdown gracefully stops the evolution system and releases resources.
// It is safe to call Shutdown multiple times; subsequent calls are no-ops.
func (s *Service) Shutdown() {
	if s.wiredSystem != nil {
		evolution.Shutdown(s.wiredSystem)
	}
	slog.Info("evolution service shut down")
}

// SaveBestStrategy persists the best strategy to a JSON file at the given path.
// Returns an error if serialization or file I/O fails.
func (s *Service) SaveBestStrategy(path string) error {
	best, err := s.BestStrategy()
	if err != nil {
		return fmt.Errorf("get best strategy: %w", err)
	}
	if best == nil {
		return fmt.Errorf("no best strategy available")
	}

	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(best, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal strategy: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	slog.Info("best strategy saved", "path", path, "id", best.ID, "score", best.Score)
	return nil
}

// LoadBestStrategy loads a best strategy from a JSON file at the given path.
// Returns the loaded strategy, or an error if the file cannot be read.
func LoadBestStrategy(path string) (*Strategy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s Strategy
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal strategy: %w", err)
	}
	return &s, nil
}

// ──────────────────────────────────────────────
// Bridge adapters: convert API provider interfaces
// into internal interfaces for wired system wiring.
// ──────────────────────────────────────────────

// apiGuidanceBridge adapts an API GuidanceProvider into an internal
// evolution.GuidanceProvider so the wired system can use it for
// experience-guided mutation.
type apiGuidanceBridge struct {
	provider GuidanceProvider
}

// HintsForTask delegates to the API provider and converts EvolutionHint types.
func (b *apiGuidanceBridge) HintsForTask(ctx context.Context, taskType string, limit int) ([]evolution.EvolutionHint, error) {
	hints, err := b.provider.HintsForTask(ctx, taskType, limit)
	if err != nil {
		return nil, err
	}
	if hints == nil {
		return nil, nil
	}
	result := make([]evolution.EvolutionHint, len(hints))
	for i, h := range hints {
		var paramHints map[string]float64
		if h.ParamHints != nil {
			paramHints = make(map[string]float64, len(h.ParamHints))
			for k, v := range h.ParamHints {
				paramHints[k] = v
			}
		}
		constraints := make([]string, len(h.Constraints))
		copy(constraints, h.Constraints)
		failedPatterns := make([]string, len(h.FailedPatterns))
		copy(failedPatterns, h.FailedPatterns)
		preferredTools := make([]string, len(h.PreferredTools))
		copy(preferredTools, h.PreferredTools)
		promptSnippets := make([]string, len(h.PromptSnippets))
		copy(promptSnippets, h.PromptSnippets)
		sourceIDs := make([]string, len(h.SourceExperienceIDs))
		copy(sourceIDs, h.SourceExperienceIDs)

		result[i] = evolution.EvolutionHint{
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

// RecordStrategyOutcome delegates to the API provider and converts types.
func (b *apiGuidanceBridge) RecordStrategyOutcome(ctx context.Context, outcome evolution.StrategyOutcome) error {
	apiOutcome := StrategyOutcome{
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
		apiOutcome.ExperienceIDs = make([]string, len(outcome.ExperienceIDs))
		copy(apiOutcome.ExperienceIDs, outcome.ExperienceIDs)
	}
	return b.provider.RecordStrategyOutcome(ctx, apiOutcome)
}

// apiMemoryBridge adapts an API MemoryExperienceProvider into an internal
// scoring.ExperienceProvider for memory-aware scoring.
type apiMemoryBridge struct {
	provider MemoryExperienceProvider
}

// FindSimilar delegates directly to the API provider (same signature).
func (b *apiMemoryBridge) FindSimilar(ctx context.Context, taskType string, limit int) (int, float64, error) {
	return b.provider.FindSimilar(ctx, taskType, limit)
}

// ──────────────────────────────────────────────
// Type conversion helpers
// ──────────────────────────────────────────────

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

// scoreAgents assigns fitness scores to agents in the population.
// If s.config.Scorer is set, it delegates to that. Otherwise it uses a
// deterministic parameter-aware heuristic (no random noise):
//   - temperature: lower is better (0.0→+25, 1.0→+0)
//   - top_k near 30 balances focus vs breadth (penalty dist²/10)
//   - "precise" prompt template earns a bonus (+15)
func (s *Service) scoreAgents(pop *genome.Population) {
	// Fast path: deterministic scorer — no clone/concurrency overhead.
	if s.config.Scorer == nil {
		pop.ScoreAgents(func(agent *mutation.Strategy) float64 {
			return DeterministicScore(toAPIStrategy(agent))
		})
		return
	}

	snap, _ := pop.Snapshot()

	// Batch path: if BatchScorer is set, score all agents in one call.
	if bs := s.config.BatchScorer; bs != nil {
		apiStrategies := make([]*Strategy, len(snap))
		for i, a := range snap {
			apiStrategies[i] = toAPIStrategy(a)
		}
		scores := bs.BatchScore(apiStrategies)
		scoreMap := make(map[string]float64, len(snap))
		for i, a := range snap {
			if i < len(scores) {
				scoreMap[a.ID] = scores[i]
			}
		}
		pop.ScoreAgents(func(agent *mutation.Strategy) float64 {
			if sc, ok := scoreMap[agent.ID]; ok {
				return sc
			}
			return 0
		})
		return
	}

	// Slow path: per-agent scoring with concurrency limit.
	scores := make([]float64, len(snap))
	sem := make(chan struct{}, concurrentScoreLimit)
	var wg sync.WaitGroup

	for i, agent := range snap {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, a *mutation.Strategy) {
			defer wg.Done()
			defer func() { <-sem }()
			scores[idx] = s.config.Scorer(toAPIStrategy(a))
		}(i, agent)
	}
	wg.Wait()

	scoreMap := make(map[string]float64, len(snap))
	for i, a := range snap {
		scoreMap[a.ID] = scores[i]
	}

	pop.ScoreAgents(func(agent *mutation.Strategy) float64 {
		if sc, ok := scoreMap[agent.ID]; ok {
			return sc
		}
		return 0
	})
}

// initScores initializes scores for all agents in the population.
func (s *Service) initScores() {
	if s.wiredSystem != nil && s.wiredSystem.Population != nil {
		s.scoreAgents(s.wiredSystem.Population)
	} else if s.population != nil {
		s.scoreAgents(s.population)
	}
}

// bestFromStrategies returns the strategy with the highest score from a slice.
func bestFromStrategies(agents []*mutation.Strategy) *mutation.Strategy {
	if len(agents) == 0 {
		return nil
	}
	best := agents[0]
	for _, a := range agents[1:] {
		if a.Score > best.Score {
			best = a
		}
	}
	return best
}

// recordGenealogy records a lineage entry between the previous generation's best
// and the current best if they differ and both exist (non-wired mode).
func (s *Service) recordGenealogy(prevBest *mutation.Strategy) {
	agents, _ := s.population.Snapshot()
	if len(agents) == 0 || prevBest == nil {
		return
	}
	currentBest := bestFromStrategies(agents)
	if currentBest == nil || currentBest.ID == prevBest.ID {
		return
	}
	scoreDelta := currentBest.Score - prevBest.Score
	s.lineages = append(s.lineages, StrategyLineage{
		ParentID:     prevBest.ID,
		ChildID:      currentBest.ID,
		MutationType: "evolution",
		WinRate:      0,
		ScoreDelta:   scoreDelta,
		Timestamp:    time.Now().UnixMilli(),
	})
}

// recordLineages records lineage entries for each offspring in non-wired mode,
// capturing parent-child relationships from the population snapshot.
// Lineages are capped at maxLineages entries to prevent unbounded growth.
func (s *Service) recordLineages() {
	if s.population == nil {
		return
	}
	snapshot, _ := s.population.Snapshot()
	for _, agent := range snapshot {
		if agent.ParentID != "" {
			s.lineages = append(s.lineages, StrategyLineage{
				ParentID:     agent.ParentID,
				ChildID:      agent.ID,
				MutationType: agent.StrategyMutationType.String(),
				WinRate:      0, // Not measured in simple GA mode
				ScoreDelta:   agent.Score,
				Timestamp:    time.Now().Unix(),
			})
		}
	}

	// Trim oldest entries if over cap.
	if len(s.lineages) > maxLineages {
		excess := len(s.lineages) - maxLineages
		s.lineages = append(s.lineages[:0], s.lineages[excess:]...)
	}
}
