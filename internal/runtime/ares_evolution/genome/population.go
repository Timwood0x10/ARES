// Package genome provides population management for genetic algorithm evolution.
// It handles strategy selection, crossover, and mutation across generations.
package genome

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/logger"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// el is the package-level structured logger. Use el.Info/Warn/Debug/Error
// throughout the genome package — it automatically attaches module="genome"
// and the method name to every log line.
var el = logger.New("genome")

// Sentinel errors are defined in errors.go.

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

	// paretoFront stores the Pareto-optimal front from the latest generation
	// when using multi-objective fitness. Updated after each scoring pass.
	paretoFront []*mutation.Strategy

	// stagnantGens counts consecutive generations without best-score improvement.
	stagnantGens int

	// currentMutationRate is the runtime mutation rate adjusted by adaptive logic.
	// Initialized from cfg.MutationRate and modified by adjustMutationRateLocked.
	// The original cfg.MutationRate is preserved as the base rate for drift-back.
	currentMutationRate float64

	// recoveryActions tracks diversity recovery actions taken in the current generation.
	// Reset at the start of each evolution cycle and captured into history at the end.
	recoveryActions map[string]int

	// history stores per-generation stats snapshots for trajectory reporting.
	// Each evolution cycle appends a snapshot when history recording is
	// enabled via WithHistoryEnabled (there is no HistoryEnabled bool field —
	// a positive HistoryMaxSize IS the switch; see population_options.go).
	history []GenerationHistoryEntry

	// HistoryMaxSize limits the number of historical entries stored.
	// When set to a positive value, older entries are trimmed when the limit
	// is exceeded. When set to 0, entries are recorded without limit.
	// Default: 0 with no WithHistoryEnabled option, so history is not
	// recorded unless explicitly enabled.
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
		recoveryActions:     make(map[string]int),
		HistoryMaxSize:      cfg.HistoryMaxSize,
	}

	err := pop.initializeFromBase(ctx, base, mutator)
	if err != nil {
		return nil, fmt.Errorf("initialize population: %w", err)
	}

	el.InfoContext(ctx, "population created",
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

// EvolveSteadyState runs one generation of steady-state GA: only a small number
// of offspring (replaceRate × populationSize) are generated each generation,
// replacing the worst individuals. The majority of the population persists
// unchanged, making this suitable for online learning scenarios.
//
// Args:
//
//	ctx - context for cancellation.
//	mutator - the mutation operator.
//	crosser - the crossover operator.
//	replaceRate - fraction of the population to replace [0.0, 0.5].
//	             Default: 0.3. Clamped to [0.1, 0.5] for stability.
//
// Returns:
//
//	error - non-nil if evolution fails.
func (p *Population) EvolveSteadyState(ctx context.Context, mutator MutatorInterface, crosser CrossoverInterface, replaceRate float64) error {
	if replaceRate <= 0 || replaceRate > 0.5 {
		replaceRate = 0.3
	}
	if replaceRate < 0.1 {
		replaceRate = 0.1
	}

	// In steady-state, we keep the elite and replace only the worst individuals.
	// The number of offspring is replaceRate * populationSize.
	offspringCount := max(1, int(float64(p.Size)*replaceRate))

	return p.doEvolve(ctx, mutator, crosser, evolveConfig{
		survivalRate: 1.0 - replaceRate,
		parentPoolFn: func(survivors []*mutation.Strategy) []*mutation.Strategy {
			return survivors
		},
		eliteFn:  p.preserveElites,
		logLabel: "steady-state evolution completed",
		// Limit offspring count for steady-state: only generate offspringCount
		// individuals instead of filling to Size - eliteCount.
		maxOffspring: offspringCount,
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

	// Phase 1 (under write lock): validation, sorting, survivor/elite
	// selection, parent pool. All pure computation on snapshots — no I/O.
	// The locked flag + defer guarantees Unlock on every return path.
	p.mu.Lock()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			p.mu.Unlock()
		}
	}
	defer unlock()

	if len(p.Agents) == 0 {
		return ErrSelectionEmptyPopulation
	}

	if err := p.ensureEvaluatedBeforeSelection(); err != nil {
		return fmt.Errorf("pre-evolution validation: %w", err)
	}

	sorted := make([]*mutation.Strategy, len(p.Agents))
	copy(sorted, p.Agents)
	SortByScore(sorted)

	if p.cfg.AgentMaxAge > 0 {
		keep := sorted[:0]
		for _, s := range sorted {
			age := p.Generation - s.GenerationCreated
			if s.StrategyMutationType == mutation.MutationRoot || s.GenerationCreated == 0 || age <= p.cfg.AgentMaxAge {
				keep = append(keep, s)
			}
		}
		sorted = keep
		if len(sorted) == 0 {
			return fmt.Errorf("genome.doEvolve: all agents aged out (AgentMaxAge=%d)", p.cfg.AgentMaxAge)
		}
	}

	survivorCount := max(1, int(float64(len(sorted))*cfg.survivalRate))
	survivorCount = min(survivorCount, len(sorted))
	survivors := sorted[:survivorCount]

	elites := cfg.eliteFn(survivors)
	elites = p.preservePromptDiversityLocked(elites, sorted)

	parentPool := cfg.parentPoolFn(survivors)
	remainingSlots := p.Size - len(elites)
	if cfg.maxOffspring > 0 && cfg.maxOffspring < remainingSlots {
		remainingSlots = cfg.maxOffspring
	}

	// Early return: no room for offspring.
	if remainingSlots <= 0 && len(elites) >= p.Size {
		nextGen := elites[:min(len(elites), p.Size)]
		p.Agents = nextGen
		p.Generation++
		p.updateBestEverLocked()
		gen := p.Generation
		rate := p.currentMutationRate
		size := len(p.Agents)
		unlock()
		el.Info(ctx, "doEvolve", "evolution completed, no offspring produced",
			"generation", gen,
			"population_size", size,
			"elite_count", len(elites),
			"mutation_rate", rate,
			"note", "no offspring produced, skipped adaptive adjustments",
		)
		return nil
	}

	selector, err := p.buildSelector()
	if err != nil && !errors.Is(err, ErrNoSelectorNeeded) {
		return fmt.Errorf("genome.doEvolve: build selector: %w", err)
	}

	// Snapshot state needed by generateOffspring so we can release the lock
	// during the I/O-capable mutation phase. Concurrent Evolve calls are safe:
	// phase 2 only reads these snapshots and the parentPool/elites pointers
	// (which are never mutated after phase 1 — a concurrent phase 3 replaces
	// p.Agents but does not write through the old pointers).
	offspringParams := offspringParams{
		parentPool:   parentPool,
		mutator:      mutator,
		crosser:      crosser,
		selector:     selector,
		count:        remainingSlots,
		mutationRate: p.currentMutationRate,
		generation:   p.Generation,
		callbacks:    p.cfg.Callbacks,
		popSize:      p.Size,
		rng:          rand.New(rand.NewSource(p.rng.Int63())), //nolint:gosec // deterministic seed from locked rng
	}
	gen := p.Generation
	popSize := p.Size
	unlock()

	// Phase 2 (outside lock): generate offspring. The guided mutator may
	// perform LLM network I/O (HintsForTask); holding the write lock across
	// it would block Stats/Snapshot/BestStrategy for the full duration —
	// the same reason ScoreAgents already scores outside the lock.
	offspring, err := p.generateOffspringUnlocked(ctx, offspringParams)
	if err != nil {
		return fmt.Errorf("genome.doEvolve: generate offspring: %w", err)
	}

	// Phase 3 (under write lock): assemble next generation and run adaptive
	// adjustments.
	p.mu.Lock()
	defer p.mu.Unlock()

	// Step 4: Assemble next generation.
	nextGen := make([]*mutation.Strategy, 0, popSize)
	nextGen = append(nextGen, elites...)
	nextGen = append(nextGen, offspring...)

	// Pad if under target size. Each survivor is used at most once to avoid
	// duplicate elite IDs in the next generation.
	survivorIdx := 0
	for len(nextGen) < popSize && survivorIdx < len(survivors) {
		clone := survivors[survivorIdx].Clone()
		clone.GenerationCreated = gen + 1
		nextGen = append(nextGen, clone)
		survivorIdx++
	}

	p.Agents = nextGen
	p.Generation++

	// Update best-ever tracking after assembling the new generation.
	p.updateBestEverLocked()

	// Apply fitness sharing to penalize crowded regions of parameter space
	// before adaptive adjustments, so diversity metrics reflect shared scores.
	// Elites are protected from penalty to preserve their scores.
	p.applyFitnessSharing(len(elites))

	// --- Recovery mechanisms ---
	// Three mechanisms run in sequence: mutation rate boost, stagnation reset,
	// and fresh mutant injection. All three respond to the same diversity
	// signals, so we capture pre-state and log a consolidated summary afterward
	// to make attribution transparent.
	preMutationRate := p.currentMutationRate
	preActions := copyRecoveryActions(p.recoveryActions)

	p.adjustMutationRateLocked()
	p.handleStagnationLocked()

	// Check for diversity collapse and inject fresh mutants if needed.
	report := p.measureDiversityReportLocked()
	if report.Overall < p.cfg.DiversityThreshold || report.DominantLineageShare > 0.6 {
		p.injectFreshMutantsLocked(len(elites))
	}

	// Consolidated recovery summary: single structured log line showing
	// which mechanism(s) fired and the diversity context that triggered them.
	postActions := copyRecoveryActions(p.recoveryActions)
	mutationBoosted := postActions["mutation_rate_boost"] - preActions["mutation_rate_boost"]
	stagnationReset := postActions["stagnation_reset"] - preActions["stagnation_reset"]
	freshInjection := postActions["fresh_injection"] - preActions["fresh_injection"]

	if mutationBoosted > 0 || stagnationReset > 0 || freshInjection > 0 {
		el.Warn(context.Background(), "doEvolve", "recovery mechanisms triggered",
			"generation", p.Generation,
			"overall_diversity", report.Overall,
			"dominant_lineage_share", report.DominantLineageShare,
			"numeric_diversity", report.Numeric,
			"categorical_diversity", report.Categorical,
			"lineage_diversity", report.Lineage,
			"mutation_rate_before", preMutationRate,
			"mutation_rate_after", p.currentMutationRate,
			"mutation_rate_boosted", mutationBoosted > 0,
			"stagnation_reset", stagnationReset > 0,
			"fresh_injection", freshInjection > 0,
		)
	}

	el.Info(ctx, "doEvolve", "evolution completed",
		"generation", p.Generation,
		"population_size", len(p.Agents),
		"elite_count", len(elites),
		"mutation_rate", p.currentMutationRate,
	)

	// Invoke generation callback if set.
	// computeStatsLocked is used directly (not Stats()) because doEvolve
	// already holds the write lock; calling Stats() would attempt RLock
	// on a non-reentrant RWMutex and self-deadlock.
	if p.cfg.Callbacks.OnGeneration != nil {
		best, avg, worst := p.computeStatsLocked()
		stats := PopulationStats{
			Generation: p.Generation,
			Size:       len(p.Agents),
			BestScore:  best,
			AvgScore:   avg,
			WorstScore: worst,
			Diversity:  p.measureDiversityReportLocked(),
		}
		p.cfg.Callbacks.OnGeneration(ctx, stats)
	}

	return nil
}

// offspringParams bundles the population state that generateOffspringUnlocked
// needs, snapshotted by doEvolve Phase 1 under the write lock. Passing a
// struct keeps the function signature within the 5-param limit.
type offspringParams struct {
	parentPool   []*mutation.Strategy
	mutator      MutatorInterface
	crosser      CrossoverInterface
	selector     Selection
	count        int
	mutationRate float64
	generation   int
	callbacks    EvolveCallbacks
	popSize      int
	rng          *rand.Rand
}

// generateOffspringUnlocked produces offspring strategies WITHOUT holding the
// population write lock. All population state it needs is carried in params
// (snapshotted by the caller under the lock). This is the I/O-safe variant
// used by doEvolve Phase 2: the guided mutator's HintsForTask may perform
// LLM network calls, and holding p.mu across them would block
// Stats/Snapshot/BestStrategy for the full duration (the same reason
// ScoreAgents already scores outside the lock).
func (p *Population) generateOffspringUnlocked(ctx context.Context, params offspringParams) ([]*mutation.Strategy, error) {
	if params.count <= 0 {
		return []*mutation.Strategy{}, nil
	}

	offspring := make([]*mutation.Strategy, 0, params.count)

	for len(offspring) < params.count {
		select {
		case <-ctx.Done():
			return offspring, ctx.Err()
		default:
		}

		var parentA, parentB *mutation.Strategy
		if params.selector != nil {
			winners, err := params.selector.Select(ctx, params.parentPool, 2)
			if err != nil {
				return nil, fmt.Errorf("select parents: %w", err)
			}
			switch len(winners) {
			case 0:
				return nil, errors.New("select returned empty winners")
			case 1:
				parentA = winners[0]
				parentB = params.parentPool[params.rng.Intn(len(params.parentPool))]
			default:
				parentA = winners[0]
				parentB = winners[1]
			}
		} else {
			parentA = params.parentPool[params.rng.Intn(len(params.parentPool))]
			parentB = params.parentPool[params.rng.Intn(len(params.parentPool))]
		}

		child, err := params.crosser.Crossover(ctx, parentA, parentB)
		if err != nil {
			return nil, fmt.Errorf("crossover failed: %w", err)
		}

		if params.callbacks.OnCrossover != nil {
			params.callbacks.OnCrossover(context.Background(), child, 0)
		}

		if params.rng.Float64() < params.mutationRate {
			mutated, err := params.mutator.Mutate(ctx, child, 1)
			if err != nil {
				return nil, fmt.Errorf("mutate offspring: %w", err)
			}
			if len(mutated) > 0 {
				mutated[0].ParentID = child.ParentID
				child = mutated[0]
			}
			if params.callbacks.OnMutation != nil {
				params.callbacks.OnMutation(context.Background(), child, 0)
			}
		}

		child.GenerationCreated = params.generation + 1
		offspring = append(offspring, child)
	}

	return offspring, nil
}

// buildSelector creates a Selection strategy based on the configured SelectionStrategy.
// Returns ErrNoSelectorNeeded for "random" or "" (backward compatible random parent selection).
func (p *Population) buildSelector() (Selection, error) {
	switch p.cfg.SelectionStrategy {
	case "", "random":
		return nil, ErrNoSelectorNeeded
	case "tournament":
		return NewTournamentSelection(
			WithTournamentSize(p.cfg.TournamentSize),
			WithTournamentSeed(p.rng.Int63()),
		)
	case "rank":
		return NewRankSelection(), nil
	case "sus":
		return NewSUSSelection(), nil
	case "roulette":
		return NewRouletteWheelSelection()
	case "truncation":
		return NewTruncationSelection(), nil
	case "lineage_rank":
		return NewLineageRankSelection()
	case "nsga2", "nondominated":
		return NewNondominatedSortingSelection(p.rng.Int63()), nil
	default:
		return nil, fmt.Errorf("unsupported selection strategy: %s", p.cfg.SelectionStrategy)
	}
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
	// Copy agents under read lock to avoid holding the write lock during external scorer calls
	// (scorer may be an LLM call or network I/O that blocks for seconds).
	p.mu.RLock()
	agents := make([]*mutation.Strategy, len(p.Agents))
	copy(agents, p.Agents)
	p.mu.RUnlock()

	// Score each agent outside the lock.
	scores := make([]float64, len(agents))
	for i, agent := range agents {
		func() {
			defer func() {
				if r := recover(); r != nil {
					el.WarnContext(context.Background(), "scorer panicked for agent, marking as unevaluated",
						"generation", p.Generation,
						"agent_index", i,
						"agent_id", agent.ID,
						"parent_id", agent.ParentID,
						"mutation_type", agent.StrategyMutationType,
						"panic_value", r,
					)
					scores[i] = ScoreUnevaluated
				}
			}()
			scores[i] = scorer(agent)
		}()
	}

	// Write scores back under write lock.
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, agent := range p.Agents {
		if i < len(scores) && agent.ID == agents[i].ID {
			agent.Score = scores[i]
			// Reset selection-adjusted score each generation. SelectionScore is
			// derived from Score by fitness sharing; without this reset, survivors
			// carried across generations would keep a stale (possibly penalty-laden)
			// value, and crowding penalties would compound across generations.
			agent.SelectionScore = 0
			// Invoke fitness callback if set.
			if p.cfg.Callbacks.OnFitness != nil {
				p.cfg.Callbacks.OnFitness(context.Background(), agent, scores[i])
			}
		} else if i < len(scores) {
			// Population changed during the scoring window (e.g. a concurrent
			// evolve replaced p.Agents): the score is stale for this slot and
			// is silently dropped. Log it so the loss is observable —
			// ID matching prevents writing the score onto the WRONG agent,
			// but the drop itself was previously invisible.
			el.WarnContext(context.Background(), "score dropped: population changed during scoring",
				"generation", p.Generation,
				"scored_agent_id", agents[i].ID,
				"current_agent_id", agent.ID)
		}
	}

	p.updateBestEverLocked()
}

// ParetoFrontStrategy returns the current Pareto-optimal strategies (deep clones).
// Returns nil if multi-objective tracking is not enabled (no DimensionScores set).
func (p *Population) ParetoFrontStrategy() []*mutation.Strategy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.paretoFront) == 0 {
		return nil
	}
	result := make([]*mutation.Strategy, len(p.paretoFront))
	for i, s := range p.paretoFront {
		result[i] = s.Clone()
	}
	return result
}

// MultiObjectiveScorerFunc is defined in population_config.go.

// ScoreAgentsMulti scores all agents using a multi-objective scorer.
// Sets both DimensionScores and Score (aggregate) on each agent.
//
// The scorer runs OUTSIDE the lock, mirroring ScoreAgents: a multi-objective
// scorer may be an LLM/network call that blocks for seconds, and running it
// under p.mu.Lock() starved every reader — a scorer that called back into
// Stats() deadlocked on the held write lock.
func (p *Population) ScoreAgentsMulti(scorer MultiObjectiveScorerFunc) {
	// Copy agent pointers under read lock; scoring does not write through
	// them (the scorer contract is read-only — see ScorerFunc).
	p.mu.RLock()
	agents := make([]*mutation.Strategy, len(p.Agents))
	copy(agents, p.Agents)
	gen := p.Generation
	p.mu.RUnlock()

	type multiScore struct {
		dims     map[string]float64
		agg      float64
		panicked bool
	}
	scores := make([]multiScore, len(agents))
	for i, agent := range agents {
		func() {
			defer func() {
				if r := recover(); r != nil {
					el.WarnContext(context.Background(), "multi-objective scorer panicked for agent, marking as unevaluated",
						"generation", gen,
						"agent_index", i,
						"agent_id", agent.ID,
						"panic_value", r,
					)
					scores[i] = multiScore{panicked: true}
				}
			}()
			dims, agg := scorer(agent)
			scores[i] = multiScore{dims: dims, agg: agg}
		}()
	}

	// Write results back under write lock, matched by ID so a concurrent
	// evolve that replaced p.Agents cannot receive a stale slot's score.
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, agent := range p.Agents {
		if i >= len(scores) || agent.ID != agents[i].ID {
			// Population changed during the scoring window: the score is
			// stale for this slot and is silently dropped (same contract as
			// ScoreAgents — ID matching prevents writing onto the WRONG agent).
			if i < len(agents) && i < len(scores) {
				el.WarnContext(context.Background(), "multi score dropped: population changed during scoring",
					"generation", gen,
					"scored_agent_id", agents[i].ID,
					"current_agent_id", agent.ID)
			}
			continue
		}
		if scores[i].panicked {
			agent.Score = ScoreUnevaluated
			agent.DimensionScores = nil
			agent.SelectionScore = 0
			continue
		}
		agent.DimensionScores = scores[i].dims
		agent.Score = scores[i].agg
		agent.SelectionScore = 0
	}
	p.updateBestEverLocked()
}

// updateBestEverLocked checks all evaluated agents against the current bestEver
// and updates it if a higher score is found. Also updates the Pareto front when
// multi-objective fitness is enabled (DimensionScores set).
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
	// Update Pareto front for multi-objective mode.
	var withDims []*mutation.Strategy
	for _, a := range p.Agents {
		if IsScoreEvaluated(a.Score) && a.DimensionScores != nil {
			withDims = append(withDims, a)
		}
	}
	if len(withDims) > 0 {
		p.paretoFront = ParetoFront(withDims)
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
// This method uses pre-computed task scores to perform selection → crossover →
// mutation as data operations. The evolution step itself requires no LLM API calls,
// but the caller must have already evaluated all agent scores (e.g. via ScoreAgents).
// If the scorer uses an LLM, the scoring step incurs token cost regardless of this
// method's name — the "zero token" claim refers only to the selection/crossover/mutation
// operations within evolve, not to the prerequisite scoring pass.
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

// BestEverID returns the ID of the best-ever strategy, or "" if no strategy
// has been evaluated. Callers that need the PRE-cycle best-ever must capture
// it before ScoreAgents/Evolve — those calls can promote a new bestEver.
func (p *Population) BestEverID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.bestEver == nil {
		return ""
	}
	return p.bestEver.ID
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

	stats.BestScore, stats.AvgScore, stats.WorstScore = p.computeStatsLocked()
	stats.Diversity = p.measureDiversityReportLocked()

	return stats
}

// computeStatsLocked calculates best/avg/worst scores from current agents.
// Caller must hold at least a read lock on p.mu.
func (p *Population) computeStatsLocked() (bestScore, avgScore, worstScore float64) {
	if len(p.Agents) == 0 {
		return 0, 0, 0
	}
	var totalScore float64
	bestScore = p.Agents[0].Score
	worstScore = p.Agents[0].Score
	for _, agent := range p.Agents {
		totalScore += agent.Score
		if agent.Score > bestScore {
			bestScore = agent.Score
		}
		if agent.Score < worstScore {
			worstScore = agent.Score
		}
	}
	return bestScore, totalScore / float64(len(p.Agents)), worstScore
}

// appendHistoryLocked appends a generation snapshot to the history.
// Caller must hold p.mu write lock. Handles HistoryMaxSize truncation.

func (p *Population) EvolveAfterScoring(ctx context.Context, scorer ScorerFunc, mutator MutatorInterface, crosser CrossoverInterface) error {
	if scorer == nil {
		return errors.New("scorer must not be nil; use NoopScorer to skip scoring")
	}
	p.ScoreAgents(scorer)
	if err := p.EvolveOnIdle(ctx, mutator, crosser); err != nil {
		return fmt.Errorf("evolution: %w", err)
	}
	p.ScoreAgents(scorer)
	p.mu.Lock()
	p.appendHistoryLocked()
	p.mu.Unlock()
	return nil
}
