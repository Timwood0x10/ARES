// Package genome provides population management for genetic algorithm evolution.
// It handles strategy selection, crossover, and mutation across generations.
package genome

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
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

// ErrInvalidMinMutationRate is returned when min mutation rate is out of range [0, 1].
var ErrInvalidMinMutationRate = fmt.Errorf("min mutation rate must be between 0 and 1")

// ErrInvalidMaxMutationRate is returned when max mutation rate is out of range [0, 1].
var ErrInvalidMaxMutationRate = fmt.Errorf("max mutation rate must be between 0 and 1")

// ErrInvalidMaxStagnantGenerations is returned when max stagnant generations is negative.
var ErrInvalidMaxStagnantGenerations = fmt.Errorf("max stagnant generations must be non-negative")

// ErrInvalidDiversityThreshold is returned when diversity threshold is out of range [0, 1].
var ErrInvalidDiversityThreshold = fmt.Errorf("diversity threshold must be between 0 and 1")

// GenerationHistoryEntry captures a per-generation snapshot for trajectory reporting.
type GenerationHistoryEntry struct {
	Generation     int
	PopulationSize int
	BestScore      float64
	AvgScore       float64
	WorstScore     float64
	Diversity      float64 // overall diversity metric

	// MutationTypes records the mutation type distribution at this generation.
	MutationTypes map[string]int `json:"mutation_types"`

	// NumDiverse counts distinct lineages (agents with unique ParentIDs) at this generation.
	NumDiverse int `json:"num_diverse"`
}

// DiversityWeightConfig holds relative weights for diversity metric components.
// Each weight represents the contribution of its component to Overall diversity.
type DiversityWeightConfig struct {
	// Numeric is the weight for numeric parameter distance [default 0.4].
	Numeric float64 `json:"numeric"`

	// Categorical is the weight for categorical attribute distance [default 0.4].
	Categorical float64 `json:"categorical"`

	// Lineage is the weight for parent ID concentration [default 0.2].
	Lineage float64 `json:"lineage"`
}

// normalize ensures weights are valid and normalizes them to sum to 1.0.
// Returns normalized copy; zero fields receive default values.
func (w DiversityWeightConfig) normalize() DiversityWeightConfig {
	result := w

	// Apply defaults for zero values.
	if result.Numeric == 0 && result.Categorical == 0 && result.Lineage == 0 {
		// All zeros → use documented defaults.
		result.Numeric = 0.4
		result.Categorical = 0.4
		result.Lineage = 0.2
		return result
	}

	if result.Numeric == 0 {
		result.Numeric = 0.4
	}
	if result.Categorical == 0 {
		result.Categorical = 0.4
	}
	if result.Lineage == 0 {
		result.Lineage = 0.2
	}

	// Normalize to sum = 1.0.
	total := result.Numeric + result.Categorical + result.Lineage
	if total > 0 && !approxEqual(total, 1.0) {
		result.Numeric /= total
		result.Categorical /= total
		result.Lineage /= total
	}

	return result
}

// approxEqual checks if two floats are within a small epsilon.
func approxEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}

// FitnessSharingSigma is the sharing coefficient for fitness sharing.
// It controls how strongly crowded niches are penalized.
const FitnessSharingSigma = 0.3

// FitnessNicheRadius is the distance threshold below which two agents
// are considered to occupy the same niche in parameter space.
const FitnessNicheRadius = 0.15

// FitnessSharingSampleLimit is the threshold above which fitness sharing
// switches from exhaustive O(n²) pairwise comparison to randomized sampling
// of neighbors per agent. Populations at or below this limit use exact distances;
// larger populations sample FitnessSharingSampleSize random neighbors instead.
const FitnessSharingSampleLimit = 50

// FitnessSharingSampleSize is the number of random neighbors checked per agent
// when the scored population exceeds FitnessSharingSampleLimit. This bounds the
// inner loop to O(k) where k = SampleSize instead of O(n).
const FitnessSharingSampleSize = 30

// MutatorInterface wraps mutation.Strategy mutation for the genome package.
// Implementations generate mutated child strategies from a parent strategy.
type MutatorInterface interface {
	// Mutate generates n mutated child strategies from the given parent strategy.
	Mutate(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error)
}

// PopulationConfig holds configuration for creating a population.
type PopulationConfig struct {
	// Size is the target population size (default 20).
	Size int `json:"size"`

	// SurvivalRate is the fraction of top performers to keep (default 0.6, i.e., eliminate bottom 40%).
	SurvivalRate float64 `json:"survival_rate"`

	// MutationRate is the probability of mutation after crossover (default 0.2).
	MutationRate float64 `json:"mutation_rate"`

	// EliteCount is the number of best individuals to preserve unchanged (default 3).
	EliteCount int `json:"elite_count"`

	// BreedingPoolRatio is the fraction of survivors eligible as parents (default 0.3).
	// Only the top BreedingPoolRatio of survivors form the breeding pool.
	// Used by EvolveOnIdle to restrict reproduction to the best survivors.
	BreedingPoolRatio float64 `json:"breeding_pool_ratio"`

	// Seed is the random seed for deterministic population creation (0 = non-deterministic).
	Seed int64 `json:"seed,omitempty"`

	// MinMutationRate is the floor for adaptive mutation rate clamping (default 0.05).
	MinMutationRate float64 `json:"min_mutation_rate"`

	// MaxMutationRate is the ceiling for adaptive mutation rate clamping (default 0.5).
	MaxMutationRate float64 `json:"max_mutation_rate"`

	// MaxStagnantGenerations is the number of generations without best-score improvement
	// before triggering a reset of bottom performers (default 10).
	MaxStagnantGenerations int `json:"max_stagnant_generations"`

	// DiversityThreshold is the minimum average pairwise distance in parameter space.
	// When actual diversity drops below this threshold, the adaptive mutation rate
	// becomes more aggressive and stagnation reset may inject random individuals.
	// Range [0, 1], default 0.15.
	DiversityThreshold float64 `json:"diversity_threshold"`

	// TournamentSize is the number of competitors per tournament round (default 3).
	// Used when UseTournamentSelection is true. Values < 2 fall back to random selection.
	TournamentSize int `json:"tournament_size"`

	// UseTournamentSelection enables tournament-based parent selection during offspring generation.
	// When false (default), parents are chosen randomly from the breeding pool.
	UseTournamentSelection bool `json:"use_tournament_selection"`

	// DiversityWeights controls the relative contribution of each diversity
	// component to the overall diversity metric. All weights must be non-negative
	// and sum to approximately 1.0 for meaningful results.
	//
	// Default values (if left zero): Numeric=0.4, Categorical=0.4, Lineage=0.2.
	// These defaults were chosen based on initial experimentation but should be
	// calibrated via ablation study for production use (see GA Hardening Plan v0.2.0).
	DiversityWeights DiversityWeightConfig `json:"diversity_weights"`

	// FitnessSharingSampleLimit is the population size threshold above which
	// fitness sharing switches from exact O(m²) pairwise comparison to sampled
	// O(m*k) mode where k=FitnessSharingSampleSize neighbors per agent.
	// Default 50; set to 0 to disable sampling (always use exact mode).
	FitnessSharingSampleLimit int `json:"fitness_sharing_sample_limit"`

	// FitnessSharingSampleSize is the number of random neighbors checked per agent
	// when in sampled fitness sharing mode. Default 30.
	FitnessSharingSampleSize int `json:"fitness_sharing_size"`

	// HistoryMaxSize limits the number of historical generation entries (0 = unlimited).
	// When > 0, each evolution cycle appends a GenerationHistoryEntry to the history.
	HistoryMaxSize int `json:"history_max_size"`

	// AdaptiveConfig controls adaptive mutation rate tuning behavior.
	// When nil, default adaptive parameters are used.
	AdaptiveConfig *AdaptiveConfig `json:"adaptive_config,omitempty"`
}

// DefaultPopulationConfig returns a PopulationConfig with sensible defaults.
//
// Returns:
//
//	PopulationConfig - configuration with default values applied.
func DefaultPopulationConfig() PopulationConfig {
	return PopulationConfig{
		Size:                      20,
		SurvivalRate:              0.6,
		MutationRate:              0.2,
		EliteCount:                3,
		BreedingPoolRatio:         0.6,
		MinMutationRate:           0.05,
		MaxMutationRate:           0.5,
		MaxStagnantGenerations:    10,
		DiversityThreshold:        0.15,
		TournamentSize:            3,
		UseTournamentSelection:    false,                   // Opt-in for backward compatibility
		DiversityWeights:          DiversityWeightConfig{}, // Zero → defaults applied in normalize()
		FitnessSharingSampleLimit: 50,
		FitnessSharingSampleSize:  30,
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

// WithPopulationSeed sets the random seed for deterministic population behavior.
// When set to a non-zero value, the population's internal RNG produces
// reproducible results across runs. When zero (default), the RNG is seeded
// from the current time and results are non-deterministic.
//
// Args:
//
//	seed - the random seed value (0 = non-deterministic).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithPopulationSeed(seed int64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		cfg.Seed = seed
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

// WithMinMutationRate sets the minimum adaptive mutation rate floor.
// The adaptive mutation rate will never go below this value.
//
// Args:
//
//	rate - floor mutation rate (must be in [0, 1]).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithMinMutationRate(rate float64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if rate < 0 || rate > 1 {
			return fmt.Errorf("%w: got %v", ErrInvalidMinMutationRate, rate)
		}
		cfg.MinMutationRate = rate
		return nil
	}
}

// WithMaxMutationRate sets the maximum adaptive mutation rate ceiling.
// The adaptive mutation rate will never exceed this value.
//
// Args:
//
//	rate - ceiling mutation rate (must be in [0, 1]).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithMaxMutationRate(rate float64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if rate < 0 || rate > 1 {
			return fmt.Errorf("%w: got %v", ErrInvalidMaxMutationRate, rate)
		}
		cfg.MaxMutationRate = rate
		return nil
	}
}

// WithMaxStagnantGenerations sets the stagnation threshold for triggering reset.
// After this many generations without best-score improvement, the bottom
// performers are reset to inject fresh genetic material.
//
// Args:
//
//	n - number of generations before reset trigger (must be >= 0).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithMaxStagnantGenerations(n int) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if n < 0 {
			return fmt.Errorf("%w: got %d", ErrInvalidMaxStagnantGenerations, n)
		}
		cfg.MaxStagnantGenerations = n
		return nil
	}
}

// WithDiversityThreshold sets the minimum diversity threshold.
// When actual population diversity drops below this value, the adaptive
// mutation rate becomes more aggressive to restore exploration.
//
// Args:
//
//	threshold - minimum average pairwise distance (must be in [0, 1], default 0.15).
//
// Returns:
//
//	PopulationOption - functional option to apply the setting.
func WithDiversityThreshold(threshold float64) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if threshold < 0 || threshold > 1 {
			return fmt.Errorf("%w: got %v", ErrInvalidDiversityThreshold, threshold)
		}
		cfg.DiversityThreshold = threshold
		return nil
	}
}

// WithTournamentSelection enables tournament-based parent selection with the given size.
// When enabled, generateOffspring uses TournamentSelection to pick parents instead of
// random selection from the breeding pool. This increases selection pressure toward
// higher-scoring individuals.
//
// Args:
//
//	size - number of competitors per tournament (must be >= 2).
//
// Returns:
//
//	PopulationOption - functional option to enable tournament selection.
//	error - ErrInvalidTournamentSize if size < 2.
func WithTournamentSelection(size int) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if size < 2 {
			return fmt.Errorf("%w: got %d", ErrInvalidTournamentSize, size)
		}
		cfg.UseTournamentSelection = true
		cfg.TournamentSize = size
		return nil
	}
}

// WithDiversityWeights sets custom diversity component weights.
//
// Args:
//
//	w - weight configuration; zero values use sensible defaults.
//
// Returns:
//
//	PopulationOption - functional option for NewPopulation.
func WithDiversityWeights(w DiversityWeightConfig) PopulationOption {
	return func(cfg *PopulationConfig) error {
		cfg.DiversityWeights = w
		return nil
	}
}

// WithFitnessSharingSampling configures the fitness sharing sampling behavior.
//
// Args:
//
//	limit - population size threshold to switch to sampled mode (0 = never sample).
//	size - neighbors to check per agent when sampling (must be < limit).
//
// Returns:
//
//	PopulationOption - functional option.
//	error - ErrInvalidMutationRate if size >= limit or either is negative.
func WithFitnessSharingSampling(limit, size int) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if limit < 0 {
			return fmt.Errorf("%w: limit must be >= 0", ErrInvalidMutationRate)
		}
		if size < 0 {
			return fmt.Errorf("%w: size must be >= 0", ErrInvalidMutationRate)
		}
		if limit > 0 && size >= limit {
			return fmt.Errorf("%w: size (%d) must be < limit (%d)", ErrInvalidMutationRate, size, limit)
		}
		cfg.FitnessSharingSampleLimit = limit
		cfg.FitnessSharingSampleSize = size
		return nil
	}
}

// WithHistoryEnabled enables per-generation history tracking for trajectory reporting.
// When maxSize > 0, each evolution cycle appends a GenerationHistoryEntry.
// HistoryMaxSize limits the number of stored entries (0 = unlimited).
//
// Args:
//
//	maxSize - maximum number of historical entries to keep (0 = unlimited).
//
// Returns:
//
//	PopulationOption - functional option to enable history tracking.
func WithHistoryEnabled(maxSize int) PopulationOption {
	return func(cfg *PopulationConfig) error {
		if maxSize < 0 {
			return fmt.Errorf("history max size must be >= 0, got %d", maxSize)
		}
		cfg.HistoryMaxSize = maxSize
		return nil
	}
}

// WithAdaptiveConfig sets custom adaptive mutation rate tuning parameters.
// When nil or unset, DefaultAdaptiveConfig() is used.
//
// Args:
//   - ac: the adaptive configuration (nil to use defaults).
//
// Returns:
//   - PopulationOption: functional option for NewPopulation.
func WithAdaptiveConfig(ac *AdaptiveConfig) PopulationOption {
	return func(cfg *PopulationConfig) error {
		cfg.AdaptiveConfig = ac
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

	// bestScore tracks the highest score seen across generations for stagnation detection.
	bestScore float64

	// bestEver holds the highest-scoring strategy seen across all generations.
	// Updated after each scoring pass. Used by BestStrategy() for deployment.
	bestEver *mutation.Strategy

	// bestEverGeneration records the generation number when the best-ever score
	// was discovered. Used by BestEverGeneration() for accurate reporting.
	bestEverGeneration int

	// stagnantGens counts consecutive generations without best-score improvement.
	stagnantGens int

	// currentMutationRate is the runtime mutation rate adjusted by adaptive logic.
	// Initialized from cfg.MutationRate and modified by adjustMutationRateLocked.
	// The original cfg.MutationRate is preserved as the base rate for drift-back.
	currentMutationRate float64

	// history stores per-generation stats snapshots for trajectory reporting.
	// When HistoryEnabled is true, each evolution cycle appends a snapshot.
	history []GenerationHistoryEntry

	// HistoryMaxSize limits the number of historical entries (0 = unlimited).
	HistoryMaxSize int
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

	if cfg.MinMutationRate > cfg.MaxMutationRate {
		return nil, fmt.Errorf("min mutation rate %f exceeds max mutation rate %f", cfg.MinMutationRate, cfg.MaxMutationRate)
	}

	seed := cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	pop := &Population{
		Agents:              make([]*mutation.Strategy, 0, cfg.Size),
		Size:                cfg.Size,
		Generation:          0,
		cfg:                 cfg,
		rng:                 rand.New(rand.NewSource(seed)), // #nosec G404 - GA doesn't need crypto rand
		bestScore:           math.Inf(-1),
		currentMutationRate: cfg.MutationRate,
		HistoryMaxSize:      cfg.HistoryMaxSize,
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
	baseClone.StrategyMutationType = mutation.MutationRoot
	baseClone.MutationDesc = "root strategy"
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
// Pre-condition: all agents in the population must have been evaluated (Score >= 0)
// before calling this method. Call ScoreAgents first if needed.
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

	// Guard: refuse to select parents from unevaluated population.
	if err := p.ensureEvaluatedBeforeSelection(); err != nil {
		return fmt.Errorf("pre-evolution validation: %w", err)
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

		// Update best-ever tracking after assembling the new generation.
		p.updateBestEverLocked()

		// Skip adaptive adjustments when no offspring were produced — no new
		// genetic material entered the pool, so diversity/stagnation signals
		// would be misleading.
		slog.InfoContext(ctx, cfg.logLabel,
			"generation", p.Generation,
			"population_size", len(p.Agents),
			"elite_count", len(elites),
			"mutation_rate", p.currentMutationRate,
		)
		return nil
	}

	// Prepare tournament selector once for reproducibility.
	var ts *TournamentSelection
	if p.cfg.UseTournamentSelection {
		var err error
		ts, err = NewTournamentSelection(
			WithTournamentSize(p.cfg.TournamentSize),
			WithTournamentSeed(p.rng.Int63()),
		)
		if err != nil {
			return fmt.Errorf("create tournament selector: %w", err)
		}
	}

	offspring, err := p.generateOffspring(ctx, parentPool, mutator, crosser, ts, remainingSlots)
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

	// Update best-ever tracking after assembling the new generation.
	p.updateBestEverLocked()

	// Apply fitness sharing to penalize crowded regions of parameter space
	// before adaptive adjustments, so diversity metrics reflect shared scores.
	// Elites are protected from penalty to preserve their scores.
	p.applyFitnessSharing(len(elites))

	p.adjustMutationRateLocked()
	p.handleStagnationLocked()

	// Check for diversity collapse and inject fresh mutants if needed.
	report := p.measureDiversityReportLocked()
	if report.Overall < p.cfg.DiversityThreshold || report.DominantLineageShare > 0.6 {
		p.injectFreshMutantsLocked(len(elites))
		slog.Warn("diversity collapse detected, injected fresh mutants",
			"generation", p.Generation,
			"overall_diversity", report.Overall,
			"dominant_lineage_share", report.DominantLineageShare,
			"numeric_diversity", report.Numeric,
			"categorical_diversity", report.Categorical,
			"lineage_diversity", report.Lineage,
		)
	}

	slog.InfoContext(ctx, cfg.logLabel,
		"generation", p.Generation,
		"population_size", len(p.Agents),
		"elite_count", len(elites),
		"mutation_rate", p.currentMutationRate,
	)

	return nil
}

// generateOffspring creates new strategies through crossover and mutation
// to fill the specified number of population slots.
// When UseTournamentSelection is configured, parents are chosen via tournament
// selection (higher-scoring individuals have higher probability). Otherwise,
// parents are selected randomly from the breeding pool (backward compatible).
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	parentPool - eligible parent strategies for crossover.
//	mutator - the mutation engine for generating variations.
//	crosser - the crossover engine for combining parents.
//	ts - optional tournament selector (nil for random selection).
//	count - number of offspring to generate.
//
// Returns:
//
//	[]*mutation.Strategy - generated offspring strategies.
//	error - non-nil if generation fails or context is cancelled.
func (p *Population) generateOffspring(ctx context.Context, parentPool []*mutation.Strategy, mutator MutatorInterface, crosser CrossoverInterface, ts *TournamentSelection, count int) ([]*mutation.Strategy, error) {
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

		var parentA, parentB *mutation.Strategy
		if ts != nil {
			// Tournament selection: run 2 tournaments to pick 2 parents.
			winners, err := ts.Select(ctx, parentPool, 2)
			if err != nil {
				return nil, fmt.Errorf("tournament select parents: %w", err)
			}
			if len(winners) >= 2 {
				parentA = winners[0]
				parentB = winners[1]
			} else if len(winners) == 1 {
				parentA = winners[0]
				parentB = parentPool[p.rng.Intn(len(parentPool))] // Fallback
			} else {
				return nil, fmt.Errorf("tournament returned no winners")
			}
		} else {
			// Original random selection (backward compatible).
			parentA = parentPool[p.rng.Intn(len(parentPool))]
			parentB = parentPool[p.rng.Intn(len(parentPool))]
		}

		child, err := crosser.Crossover(ctx, parentA, parentB)
		if err != nil {
			return nil, fmt.Errorf("crossover failed: %w", err)
		}

		// Apply mutation based on configured rate.
		// The Mutate call is only triggered when the probability check passes,
		// ensuring mutators with side effects (e.g., counters) are not invoked
		// on offspring that skip mutation.
		if p.rng.Float64() < p.currentMutationRate {
			mutated, err := mutator.Mutate(ctx, child, 1)
			if err != nil {
				return nil, fmt.Errorf("mutate offspring: %w", err)
			}
			// Mutate(n=1) returns exactly one variant; use it as the mutated child.
			if len(mutated) > 0 {
				// Preserve original crossover parent IDs so outcome recording
				// can look up parent scores in the pre-evolution snapshot.
				mutated[0].ParentID = child.ParentID
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

// ScoreAgents applies the given scoring function to each agent in-place.
// This is thread-safe: it acquires a write lock and updates each agent's Score
// field directly, unlike Snapshot() which returns deep clones that discard writes.
//
// If the scorer panics for any agent, the panic is caught, logged as a warning,
// and the agent's score is set to ScoreUnevaluated so subsequent guards catch it.
// Other agents continue to be scored normally.
//
// Args:
//
//	scorer - function that takes an agent (read-only) and returns its fitness score.
func (p *Population) ScoreAgents(scorer func(*mutation.Strategy) float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, agent := range p.Agents {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("scorer panicked for agent, marking as unevaluated",
						"generation", p.Generation,
						"agent_index", i,
						"agent_id", agent.ID,
						"parent_id", agent.ParentID,
						"mutation_type", agent.StrategyMutationType,
						"panic_value", r,
					)
					agent.Score = ScoreUnevaluated
				}
			}()
			agent.Score = scorer(agent)
		}()
	}

	p.updateBestEverLocked()
}

// updateBestEverLocked checks all evaluated agents against the current bestEver
// and updates it if a higher score is found.
//
// Concurrency safety contract:
//   - Caller MUST hold p.mu write lock (not just RLock). This is enforced by
//     all current call sites: ScoreAgents() line ~972, doEvolve() lines ~759/806.
//     The write lock is required because this method mutates p.bestEver and
//     p.bestEverGeneration.
//   - The method stores a.Clone() (deep copy) into p.bestEver, ensuring the
//     returned reference from BestStrategy() can never alias an agent in
//     p.Agents. This prevents callers from corrupting population state.
//
// This method intentionally skips unevaluated agents (ScoreUnevaluated) so that
// panic-recovered or yet-to-be-scored agents never become bestEver.
func (p *Population) updateBestEverLocked() {
	for _, a := range p.Agents {
		if !IsScoreEvaluated(a.Score) {
			continue
		}
		if p.bestEver == nil || a.Score > p.bestEver.Score {
			p.bestEver = a.Clone()
			p.bestEverGeneration = p.Generation
		}
	}
}

// Best returns a deep clone of the highest-scoring strategy in the current population.
// Returns nil if the population is empty. The clone ensures callers cannot accidentally
// corrupt the population state, consistent with BestStrategy() and Snapshot().
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

	return best.Clone()
}

// EvolveOnIdle runs a simplified evolution cycle triggered during system idle time.
// Delegates to doEvolve with idle-specific configuration: configurable survival rate,
// top BreedingPoolRatio of survivors as breeding pool, and configured elite count.
//
// This is the zero-token evolution loop specified in the design document:
// it uses pre-computed task scores (no LLM calls needed) and performs
// selection → crossover → mutation purely as data operations.
//
// This method is designed to be called from Callback EventAgentEnd handler,
// requiring no additional LLM API calls (zero token cost for evolution itself).
//
// Pre-condition: all agents in the population must have been evaluated (Score >= 0)
// before calling this method. Call ScoreAgents first if needed.
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
		eliteFn:  p.preserveElites,
		logLabel: "evolve_on_idle completed",
	})
}

// BestStrategy returns a deep clone of the best-ever strategy across all generations.
// If no strategy has ever been evaluated, falls back to the current population's best.
// Returns nil if the population is empty and no best-ever exists.
//
// Returns:
//
//	*mutation.Strategy: cloned best-ever strategy, current best clone, or nil.
func (p *Population) BestStrategy() *mutation.Strategy {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.bestEver != nil {
		return p.bestEver.Clone()
	}

	// Fallback: return current population best if bestEver not yet set.
	if len(p.Agents) == 0 {
		return nil
	}
	best := p.Agents[0]
	for _, agent := range p.Agents[1:] {
		if IsScoreEvaluated(agent.Score) && agent.Score > best.Score {
			best = agent
		}
	}
	if !IsScoreEvaluated(best.Score) {
		return nil
	}
	return best.Clone()
}

// BestEverScore returns the score of the best-ever strategy, or ScoreUnevaluated if none exists.
//
// Returns:
//
//	float64 - the best-ever score, or ScoreUnevaluated if no strategy has been evaluated.
func (p *Population) BestEverScore() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.bestEver == nil {
		return ScoreUnevaluated
	}
	return p.bestEver.Score
}

// BestEverGeneration returns the generation number when the best-ever score was discovered.
// Returns 0 if no strategy has ever been evaluated (generation 0 is the initial population).
//
// Returns:
//
//	int - the generation number of the best-ever discovery.
func (p *Population) BestEverGeneration() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.bestEver == nil {
		return 0
	}
	return p.bestEverGeneration
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
	stats.Diversity = p.measureDiversityReportLocked()

	return stats
}

// appendHistoryLocked appends a generation snapshot to the history.
// Caller must hold p.mu write lock. Handles HistoryMaxSize truncation.
func (p *Population) appendHistoryLocked() {
	if p.HistoryMaxSize == 0 {
		return // history not enabled (default)
	}

	entry := GenerationHistoryEntry{
		Generation:     p.Generation,
		PopulationSize: len(p.Agents),
		Diversity:      p.measureDiversityReportLocked().Overall,
	}

	if len(p.Agents) == 0 {
		p.history = append(p.history, entry)
		return
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

	entry.BestScore = bestScore
	entry.AvgScore = totalScore / float64(len(p.Agents))
	entry.WorstScore = worstScore

	// Record mutation type distribution and diverse lineage count.
	entry.MutationTypes = make(map[string]int)
	parentSet := make(map[string]struct{})
	for _, agent := range p.Agents {
		mt := agent.StrategyMutationType.String()
		if mt == "" {
			mt = "unknown"
		}
		entry.MutationTypes[mt]++
		if agent.ParentID != "" {
			parentSet[agent.ParentID] = struct{}{}
		}
	}
	entry.NumDiverse = len(parentSet)

	p.history = append(p.history, entry)

	// Truncate if exceeding max size.
	if p.HistoryMaxSize > 0 && len(p.history) > p.HistoryMaxSize {
		p.history = p.history[len(p.history)-p.HistoryMaxSize:]
	}
}

// History returns all recorded generation history entries (deep copy).
// Returns nil if history is empty or not enabled.
func (p *Population) History() []GenerationHistoryEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.history) == 0 {
		return nil
	}
	cp := make([]GenerationHistoryEntry, len(p.history))
	copy(cp, p.history)
	return cp
}

// HistoryCount returns the number of recorded history entries.
func (p *Population) HistoryCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.history)
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

	// Diversity is the detailed diversity breakdown of the population.
	Diversity DiversityReport
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

// ScorerFunc is a function that assigns a fitness score to a strategy.
// It returns the strategy's evaluated score. If the scorer cannot evaluate
// the strategy (e.g., no LLM available), it should return ScoreUnevaluated (-1)
// and the evolution will handle it as a scoring failure.
type ScorerFunc func(agent *mutation.Strategy) float64

// NoopScorer is a ScorerFunc that preserves existing scores without modification.
// Agents with ScoreUnevaluated remain unevaluated — use this ONLY when you have
// already scored all agents externally and just need to satisfy the type signature.
func NoopScorer(agent *mutation.Strategy) float64 {
	return agent.Score
}

// ConstantScorer returns a ScorerFunc that always assigns the given score.
// Useful in tests where real evaluation is unavailable.
func ConstantScorer(score float64) ScorerFunc {
	return func(agent *mutation.Strategy) float64 {
		return score
	}
}

// EvolveAfterScoring performs one atomic generation of evolution with automatic
// pre-scoring and post-scoring. This is the recommended entry point for most
// callers because it eliminates the risk of calling Evolve with unevaluated agents.
//
// The method guarantees:
//   - All unevaluated agents are scored before selection (pre-scoring).
//   - Evolution proceeds only if scoring succeeds for all agents.
//   - Newly created offspring are scored after evolution (post-scoring).
//
// This implements the "score first, evolve later" temporal constraint as an atomic operation.
//
// Args:
//
//	ctx - operation context for cancellation.
//	scorer - function that assigns fitness scores; called for each agent.
//	  Must NOT be nil. Use NoopScorer if scoring should be skipped.
//	mutator - mutation engine (must not be nil).
//	crosser - crossover engine (must not be nil).
//
// Returns:
//
//	error - non-nil if scoring fails for any agent or evolution encounters an error.
func (p *Population) EvolveAfterScoring(ctx context.Context, scorer ScorerFunc, mutator MutatorInterface, crosser CrossoverInterface) error {
	// NOTE: This method acquires/releases the population lock 3 times:
	//
	//   1. ScoreAgents (pre-scoring) — write lock, scores all agents
	//   2. doEvolve via EvolveOnIdle — write lock, runs full evolution cycle
	//   3. ScoreAgents (post-scoring) — write lock, scores offspring
	//
	// Between steps 2 and 3, other goroutines COULD modify the population
	// (e.g., Stats(), BestStrategy(), external reads). This is safe because:
	//   - Each phase independently acquires its own lock
	//   - Post-scoring operates on whatever agents exist after evolve completes
	//   - Callers are responsible for ensuring no concurrent Evolve() calls
	//
	// If atomicity across all three phases is needed in the future, refactor to
	// hold a single lock for the entire method body.
	if scorer == nil {
		return fmt.Errorf("scorer must not be nil; use NoopScorer to skip scoring")
	}

	// Phase 1: Pre-score all agents (overwrites unevaluated scores).
	p.ScoreAgents(scorer)

	// Phase 2: Run evolution (ensureEvaluatedBeforeSelection guard passes inside).
	if err := p.EvolveOnIdle(ctx, mutator, crosser); err != nil {
		return fmt.Errorf("evolution: %w", err)
	}

	// Phase 3: Post-score newly created offspring (Score=-1 from mutator).
	p.ScoreAgents(scorer)

	// Record generation history after post-scoring so stats reflect all scored agents.
	p.mu.Lock()
	p.appendHistoryLocked()
	p.mu.Unlock()

	return nil
}
