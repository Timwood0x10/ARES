// Package genome provides population management for genetic algorithm evolution.
// It handles strategy selection, crossover, and mutation across generations.
package genome

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"goagentx/internal/evolution/mutation"
)

// ErrNilBaseStrategy is returned when a nil base strategy is provided to NewPopulation.
var ErrNilBaseStrategy = fmt.Errorf("base strategy must not be nil")

// ErrNilMutator is returned when a nil mutator is provided.
var ErrNilMutator = fmt.Errorf("mutator must not be nil")

// ErrNilCrosser is returned when a nil crosser is provided.
var ErrNilCrosser = fmt.Errorf("crosser must not be nil")

// ErrInvalidPopulationSize is returned when population size is invalid.
var ErrInvalidPopulationSize = fmt.Errorf("population size must be positive")

// ErrInvalidSurvivalRate is returned when survival rate is out of valid range [0, 1].
var ErrInvalidSurvivalRate = fmt.Errorf("survival rate must be between 0 and 1")

// ErrInvalidMutationRate is returned when mutation rate is out of valid range [0, 1].
var ErrInvalidMutationRate = fmt.Errorf("mutation rate must be between 0 and 1")

// ErrInvalidEliteCount is returned when elite count is negative or exceeds size.
var ErrInvalidEliteCount = fmt.Errorf("elite count must be non-negative and <= population size")

// ErrInvalidBreedingPoolRatio is returned when breeding pool ratio is out of range [0, 1].
var ErrInvalidBreedingPoolRatio = fmt.Errorf("breeding pool ratio must be between 0 and 1")

// MutatorInterface wraps mutation.Strategy mutation for the genome package.
// Implementations generate mutated child strategies from a parent strategy.
type MutatorInterface interface {
	// Mutate generates n mutated child strategies from the given parent strategy.
	Mutate(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error)
}

// PopulationConfig holds configuration for creating a population.
type PopulationConfig struct {
	// Size is the target population size (default 20).
	Size int

	// SurvivalRate is the fraction of top performers to keep (default 0.6, i.e., eliminate bottom 40%).
	SurvivalRate float64

	// MutationRate is the probability of mutation after crossover (default 0.2).
	MutationRate float64

	// EliteCount is the number of best individuals to preserve unchanged (default 1).
	EliteCount int

	// BreedingPoolRatio is the fraction of survivors eligible as parents (default 0.3).
	// Only the top BreedingPoolRatio of survivors form the breeding pool.
	// Used by EvolveOnIdle to restrict reproduction to the best survivors.
	BreedingPoolRatio float64
}

// DefaultPopulationConfig returns a PopulationConfig with sensible defaults.
//
// Returns:
//
//	PopulationConfig - configuration with default values applied.
func DefaultPopulationConfig() PopulationConfig {
	return PopulationConfig{
		Size:              20,
		SurvivalRate:      0.6, // Keep 60%, eliminate bottom 40% per design doc
		MutationRate:      0.2,
		EliteCount:        1,
		BreedingPoolRatio: 0.3, // Top 30% of survivors form the breeding pool
	}
}

// PopulationOption is a functional option for configuring Population creation.
type PopulationOption func(*PopulationConfig) error

// WithPopulationSize sets the target population size.
//
// Args:
//
//	size - target number of strategies in each generation (must be > 0).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithPopulationSize(size int) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if size <= 0 {
			return fmt.Errorf("%w: got %d", ErrInvalidPopulationSize, size)
		}
		cfg.Size = size
		return nil
	}
}

// WithSurvivalRate sets the survival selection rate.
//
// Args:
//
//	rate - fraction of top performers to keep (must be in [0, 1]).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithSurvivalRate(rate float64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if rate < 0 || rate > 1 {
			return fmt.Errorf("%w: got %v", ErrInvalidSurvivalRate, rate)
		}
		cfg.SurvivalRate = rate
		return nil
	}
}

// WithMutationRate sets the post-crossover mutation probability.
//
// Args:
//
//	rate - probability of mutating each offspring (must be in [0, 1]).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithMutationRate(rate float64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if rate < 0 || rate > 1 {
			return fmt.Errorf("%w: got %v", ErrInvalidMutationRate, rate)
		}
		cfg.MutationRate = rate
		return nil
	}
}

// WithEliteCount sets the number of elite individuals to preserve unchanged.
//
// Args:
//
//	count - number of best individuals to carry over unchanged (must be >= 0).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithEliteCount(count int) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if count < 0 {
			return fmt.Errorf("%w: got %d", ErrInvalidEliteCount, count)
		}
		cfg.EliteCount = count
		return nil
	}
}

// WithBreedingPoolRatio sets the fraction of survivors that form the breeding pool.
// Only the top BreedingPoolRatio of survivors are eligible as parents during idle evolution.
// Value must be in [0, 1]. Default is 0.3 (top 30%).
//
// Args:
//
//	ratio - fraction of survivors used for breeding (0.0-1.0).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
//	error - ErrInvalidBreedingPoolRatio if ratio is out of range.
func WithBreedingPoolRatio(ratio float64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if ratio < 0 || ratio > 1 {
			return fmt.Errorf("%w: breeding pool ratio must be between 0 and 1, got %v", ErrInvalidBreedingPoolRatio, ratio)
		}
		cfg.BreedingPoolRatio = ratio
		return nil
	}
}

// Population holds a collection of agent strategies that evolve together.
// It manages the lifecycle of strategies across generations using
// selection, crossover, and mutation operations.
type Population struct {
	// Agents contains the individual strategies in this population.
	Agents []*mutation.Strategy

	// Size is the target population size (constant across generations).
	Size int

	// Generation is the current generation number (0 = initial).
	Generation int

	// mu protects concurrent access to Agents and Generation fields.
	mu sync.RWMutex

	// cfg holds the evolution configuration parameters.
	cfg PopulationConfig

	// rng provides deterministic randomness for reproducible evolution.
	rng *rand.Rand
}

// NewPopulation creates a new population from a base strategy.
// It generates initial variants by mutating the base strategy to fill
// the target population size.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	base - the root strategy to evolve (must not be nil).
//	mutator - the mutation engine for generating initial variants (must not be nil).
//	opts - optional configuration functions (WithPopulationSize, etc.).
//
// Returns:
//
//	*Population - the initialized population with generated variants.
//	error - non-nil if validation fails or mutation encounters an error.
func NewPopulation(ctx context.Context, base *mutation.Strategy, mutator MutatorInterface, opts ...PopulationOption) (*Population, error) {
	if base == nil {
		return nil, ErrNilBaseStrategy
	}
	if mutator == nil {
		return nil, ErrNilMutator
	}

	cfg := DefaultPopulationConfig()
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, fmt.Errorf("apply population option: %w", err)
		}
	}

	if cfg.EliteCount > cfg.Size {
		return nil, fmt.Errorf("%w: elite count %d exceeds size %d", ErrInvalidEliteCount, cfg.EliteCount, cfg.Size)
	}

	pop := &Population{
		Agents:     make([]*mutation.Strategy, 0, cfg.Size),
		Size:       cfg.Size,
		Generation: 0,
		cfg:        cfg,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404 - GA doesn't need crypto rand
	}

	err := pop.initializeFromBase(ctx, base, mutator)
	if err != nil {
		return nil, fmt.Errorf("initialize population: %w", err)
	}

	slog.InfoContext(ctx, "population created",
		"size", pop.Size,
		"generation", pop.Generation,
	)

	return pop, nil
}

// initializeFromBase generates initial population by cloning the base strategy
// and mutating it to fill the remaining slots.
func (p *Population) initializeFromBase(ctx context.Context, base *mutation.Strategy, mutator MutatorInterface) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	baseClone := base.Clone()
	p.Agents = append(p.Agents, baseClone)

	if p.Size > 1 {
		variantsNeeded := p.Size - 1
		// Use baseClone (our own copy) instead of the external base reference.
		// This avoids potential data races if external code modifies base concurrently.
		variants, err := mutator.Mutate(ctx, baseClone, variantsNeeded)
		if err != nil {
			return fmt.Errorf("generate initial variants: %w", err)
		}

		p.Agents = append(p.Agents, variants...)
	}

	return nil
}

// Evolve runs one generation of evolution on the population.
// Delegates to doEvolve with standard configuration: configurable survival rate,
// all survivors as parent pool, and configured elite preservation.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	mutator - the mutation engine for generating variations (must not be nil).
//	crosser - the crossover engine for combining parents (must not be nil).
//
// Returns:
//
//	error - non-nil if validation fails or any evolution step encounters an error.
func (p *Population) Evolve(ctx context.Context, mutator MutatorInterface, crosser CrossoverInterface) error {
	return p.doEvolve(ctx, mutator, crosser, evolveConfig{
		survivalRate: p.cfg.SurvivalRate,
		parentPoolFn: func(survivors []*mutation.Strategy) []*mutation.Strategy {
			return survivors // All survivors are eligible parents
		},
		eliteFn:  p.preserveElites,
		logLabel: "evolution completed",
	})
}

// doEvolve runs the core evolution loop shared by Evolve and EvolveOnIdle.
// It performs: validate → lock → sort → select → elite → crossover → mutate → assemble → increment.
//
// Args:
//   - ctx: operation context.
//   - mutator: mutation engine.
//   - crosser: crossover engine.
//   - cfg: evolution configuration capturing behavioral differences.
//
// Returns:
//   - error: non-nil if validation or any step fails.
func (p *Population) doEvolve(ctx context.Context, mutator MutatorInterface, crosser CrossoverInterface, cfg evolveConfig) error {
	if mutator == nil {
		return ErrNilMutator
	}
	if crosser == nil {
		return ErrNilCrosser
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.Agents) == 0 {
		return ErrSelectionEmptyPopulation
	}

	// Step 1: Sort by score and select survivors.
	sorted := make([]*mutation.Strategy, len(p.Agents))
	copy(sorted, p.Agents)
	SortByScore(sorted)

	survivorCount := max(1, int(float64(len(sorted))*cfg.survivalRate))
	survivorCount = min(survivorCount, len(sorted))
	survivors := sorted[:survivorCount]

	// Step 2: Preserve elites (method-specific).
	elites := cfg.eliteFn(survivors)

	// Step 3: Generate offspring using method-specific parent pool.
	parentPool := cfg.parentPoolFn(survivors)
	remainingSlots := p.Size - len(elites)
	if remainingSlots <= 0 && len(elites) >= p.Size {
		// No room for offspring; use elites as next gen (trim if needed).
		nextGen := elites[:min(len(elites), p.Size)]
		p.Agents = nextGen
		p.Generation++
		slog.InfoContext(ctx, cfg.logLabel,
			"generation", p.Generation,
			"population_size", len(p.Agents),
			"elite_count", len(elites),
		)
		return nil
	}

	offspring, err := p.generateOffspring(ctx, parentPool, mutator, crosser, remainingSlots)
	if err != nil {
		return fmt.Errorf("generate offspring: %w", err)
	}

	// Step 4: Assemble next generation.
	nextGen := make([]*mutation.Strategy, 0, p.Size)
	nextGen = append(nextGen, elites...)
	nextGen = append(nextGen, offspring...)

	// Pad if under target size.
	for len(nextGen) < p.Size && len(survivors) > 0 {
		idx := len(nextGen) % len(survivors)
		nextGen = append(nextGen, survivors[idx].Clone())
	}

	p.Agents = nextGen
	p.Generation++

	slog.InfoContext(ctx, cfg.logLabel,
		"generation", p.Generation,
		"population_size", len(p.Agents),
		"survivor_count", survivorCount,
		"elite_count", len(elites),
	)

	return nil
}

// preserveElites copies the top EliteCount survivors without modification.
// Elites are deep-cloned to prevent shared state across generations.
func (p *Population) preserveElites(survivors []*mutation.Strategy) []*mutation.Strategy {
	eliteCount := min(p.cfg.EliteCount, len(survivors))
	if eliteCount <= 0 {
		return []*mutation.Strategy{}
	}

	elites := make([]*mutation.Strategy, 0, eliteCount)
	for i := 0; i < eliteCount; i++ {
		elites = append(elites, survivors[i].Clone())
	}

	return elites
}

// generateOffspring creates new strategies through crossover and mutation
// to fill the specified number of population slots.
func (p *Population) generateOffspring(ctx context.Context, parentPool []*mutation.Strategy, mutator MutatorInterface, crosser CrossoverInterface, count int) ([]*mutation.Strategy, error) {
	if count <= 0 {
		return []*mutation.Strategy{}, nil
	}

	offspring := make([]*mutation.Strategy, 0, count)

	for len(offspring) < count {
		select {
		case <-ctx.Done():
			return offspring, ctx.Err()
		default:
		}

		parentA := parentPool[p.rng.Intn(len(parentPool))]
		parentB := parentPool[p.rng.Intn(len(parentPool))]

		child, err := crosser.Crossover(ctx, parentA, parentB)
		if err != nil {
			return nil, fmt.Errorf("crossover failed: %w", err)
		}

		// Apply mutation based on configured rate.
		// The Mutate call is only triggered when the probability check passes,
		// ensuring mutators with side effects (e.g., counters) are not invoked
		// on offspring that skip mutation.
		if p.rng.Float64() < p.cfg.MutationRate {
			mutated, err := mutator.Mutate(ctx, child, 1)
			if err != nil {
				return nil, fmt.Errorf("mutate offspring: %w", err)
			}
			// Mutate(n=1) returns exactly one variant; use it as the mutated child.
			if len(mutated) > 0 {
				child = mutated[0]
			}
			// If len(mutated) == 0, the mutator returned no variants;
			// keep the unmutated crossover child as-is.
		}

		offspring = append(offspring, child)
	}

	return offspring, nil
}

// Snapshot returns a thread-safe copy of all agents and the current generation.
// This is the safe way for external code to read population state without
// holding the internal mutex.
//
// Returns:
//
//	[]*mutation.Strategy - a copy of all agents (deep-cloned).
//	int - the current generation number.
func (p *Population) Snapshot() ([]*mutation.Strategy, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	agents := make([]*mutation.Strategy, len(p.Agents))
	for i, a := range p.Agents {
		agents[i] = a.Clone()
	}
	return agents, p.Generation
}

// Best returns the highest-scoring strategy in the current population.
// If multiple strategies share the same highest score, the first one
// encountered during iteration is returned.
//
// Returns:
//
//	*mutation.Strategy - the best strategy, or nil if the population is empty.
func (p *Population) Best() *mutation.Strategy {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.Agents) == 0 {
		return nil
	}

	best := p.Agents[0]
	for _, agent := range p.Agents[1:] {
		if agent.Score > best.Score {
			best = agent
		}
	}

	return best
}

// EvolveOnIdle runs a simplified evolution cycle triggered during system idle time.
// Delegates to doEvolve with idle-specific configuration: configurable survival rate,
// top BreedingPoolRatio of survivors as breeding pool, and single elite preservation.
//
// This is the zero-token evolution loop specified in the design document:
// it uses pre-computed task scores (no LLM calls needed) and performs
// selection → crossover → mutation purely as data operations.
//
// This method is designed to be called from Callback EventAgentEnd handler,
// requiring no additional LLM API calls (zero token cost for evolution itself).
//
// Args:
//
//   - ctx: operation context for cancellation.
//   - mutator: mutation engine for generating variations (must not be nil).
//   - crosser: crossover engine for combining parent strategies (must not be nil).
//
// Returns:
//
//   - error: non-nil if validation fails or any step encounters an error.
func (p *Population) EvolveOnIdle(ctx context.Context, mutator MutatorInterface, crosser CrossoverInterface) error {
	return p.doEvolve(ctx, mutator, crosser, evolveConfig{
		survivalRate: p.cfg.SurvivalRate, // Use configured rate (default 0.6), not hardcoded value
		parentPoolFn: func(survivors []*mutation.Strategy) []*mutation.Strategy {
			poolSize := int(float64(len(survivors)) * p.cfg.BreedingPoolRatio)
			if poolSize < 2 {
				poolSize = min(2, len(survivors))
			}
			return survivors[:poolSize]
		},
		eliteFn: func(survivors []*mutation.Strategy) []*mutation.Strategy {
			if len(survivors) == 0 {
				return []*mutation.Strategy{}
			}
			return []*mutation.Strategy{survivors[0].Clone()}
		},
		logLabel: "evolve_on_idle completed",
	})
}

// BestStrategy returns a deep clone of the highest-scoring strategy in the population.
// This is intended for deployment to production after evolution completes.
// Returns nil if the population is empty.
//
// Returns:
//
//   - *mutation.Strategy: cloned best strategy, or nil if empty.
func (p *Population) BestStrategy() *mutation.Strategy {
	best := p.Best()
	if best == nil {
		return nil
	}
	return best.Clone()
}

// Stats returns population statistics for the current generation.
// The statistics include score distribution metrics across all agents.
//
// Returns:
//
//	*PopulationStats - snapshot of population statistics (never nil).
func (p *Population) Stats() *PopulationStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := &PopulationStats{
		Generation: p.Generation,
		Size:       len(p.Agents),
	}

	if len(p.Agents) == 0 {
		return stats
	}

	var totalScore float64
	bestScore := p.Agents[0].Score
	worstScore := p.Agents[0].Score

	for _, agent := range p.Agents {
		totalScore += agent.Score
		if agent.Score > bestScore {
			bestScore = agent.Score
		}
		if agent.Score < worstScore {
			worstScore = agent.Score
		}
	}

	stats.AvgScore = totalScore / float64(len(p.Agents))
	stats.BestScore = bestScore
	stats.WorstScore = worstScore

	return stats
}

// CurrentGeneration returns the current generation number under read lock.
// This is the thread-safe way to access Generation from outside the package.
func (p *Population) CurrentGeneration() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Generation
}

// PopulationStats holds statistical information about a population's state.
type PopulationStats struct {
	// Generation is the current generation number.
	Generation int

	// Size is the number of agents in the population.
	Size int

	// AvgScore is the average score across all agents.
	AvgScore float64

	// BestScore is the highest score among all agents.
	BestScore float64

	// WorstScore is the lowest score among all agents.
	WorstScore float64
}

// evolveConfig captures the configurable differences between Evolve and EvolveOnIdle.
type evolveConfig struct {
	// survivalRate is the fraction of survivors to keep (0.0-1.0).
	survivalRate float64

	// parentPoolFn selects which survivors are eligible as parents.
	parentPoolFn func(survivors []*mutation.Strategy) []*mutation.Strategy

	// eliteFn preserves elite individuals from the survivor set.
	eliteFn func(survivors []*mutation.Strategy) []*mutation.Strategy

	// logLabel is the label used in slog output for this evolution run.
	logLabel string
}
