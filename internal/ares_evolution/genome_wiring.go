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
	"strings"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/scoring"
	"github.com/Timwood0x10/ares/internal/observability"
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

	// AdaptiveDist adjusts mutation type probabilities based on observed
	// outcomes from previous evolution cycles (optional). When set, Run()
	// records outcome feedback after each evolution cycle.
	adaptiveDist *mutation.AdaptiveDistribution

	// FeedbackRecorder records strategy outcomes to the experience feedback
	// system for experience reinforcement (optional). When set, Run()
	// records outcome feedback after each evolution cycle.
	feedbackRecorder *FeedbackRecorder

	// Metrics records Prometheus counters for evolution events (optional).
	metrics *observability.PrometheusMetrics
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

// WithAdapterAdaptiveDistribution sets the adaptive mutation distribution
// for outcome-driven probability adjustment. When set, Run() records
// outcome feedback after each evolution cycle.
//
// Args:
//
//	ad - the adaptive distribution instance (may be nil to disable).
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterAdaptiveDistribution(ad *mutation.AdaptiveDistribution) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.adaptiveDist = ad
	}
}

// WithAdapterFeedbackRecorder sets the feedback recorder for experience
// reinforcement. When set, Run() records strategy outcomes to the feedback
// service after each evolution cycle.
//
// Args:
//
//	fr - the feedback recorder instance (may be nil to disable).
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterFeedbackRecorder(fr *FeedbackRecorder) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.feedbackRecorder = fr
	}
}

// WithAdapterMetrics sets the metrics recorder for evolution event counters.
//
// Args:
//
//	metrics - the Prometheus metrics instance (may be nil).
//
// Returns:
//
//	GenomeAdapterOption - the configuration function.
func WithAdapterMetrics(metrics *observability.PrometheusMetrics) GenomeAdapterOption {
	return func(a *GenomePopulationAdapter) {
		a.metrics = metrics
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

	// Capture pre-evolution snapshot for outcome recording when feedback
	// components are wired. This lets us compare offspring scores with
	// their parent scores after evolution.
	var agentsBefore []*mutation.Strategy
	if a.adaptiveDist != nil || a.feedbackRecorder != nil {
		agentsBefore, _ = a.pop.Snapshot()
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
			if a.metrics != nil {
				a.metrics.RecordEvolutionGuardrail(string(evt.ErrorCode))
			}
		}

		if preResult.ShouldStop {
			return fmt.Errorf("[GenomeAdapter] pre-evolve guardrail check failed (generation %d): %d event(s), best_score=%.2f, unevaluated=%d/%d",
				preStats.Generation, len(preResult.Events), preStats.BestScore, unevaluated, preStats.Size)
		}
	}

	if err := a.pop.EvolveAfterScoring(ctx, scorer, a.mutator, a.crosser); err != nil {
		return fmt.Errorf("genome evolve on idle: %w", err)
	}

	// Record outcomes for adaptive distribution and feedback service.
	// This closes the feedback loop: evolution results flow back to
	// update probability distributions and experience rankings.
	if agentsBefore != nil {
		a.recordOutcomesLocked(ctx, agentsBefore)
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
			if a.metrics != nil {
				a.metrics.RecordEvolutionGuardrail(string(evt.ErrorCode))
			}
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

// recordOutcomesLocked records strategy outcomes to the adaptive distribution
// and feedback recorder after an evolution cycle. It compares offspring scores
// with their parent scores to determine wins and score deltas.
//
// Args:
//
//	ctx - operation context for cancellation.
//	agentsBefore - pre-evolution population snapshot for parent score lookup.
func (a *GenomePopulationAdapter) recordOutcomesLocked(
	ctx context.Context,
	agentsBefore []*mutation.Strategy,
) {
	parentScores := make(map[string]float64, len(agentsBefore))
	for _, parent := range agentsBefore {
		parentScores[parent.ID] = parent.Score
	}

	agentsAfter, _ := a.pop.Snapshot()

	for _, child := range agentsAfter {
		if child.ParentID == "" {
			continue
		}
		if child.Score < 0 {
			continue
		}

		parentScore, ok := parentScores[child.ParentID]
		if !ok {
			if parts := strings.Split(child.ParentID, "\u00d7"); len(parts) == 2 {
				if ps1, ok1 := parentScores[parts[0]]; ok1 {
					if ps2, ok2 := parentScores[parts[1]]; ok2 {
						parentScore = (ps1 + ps2) / 2
						ok = true
					}
				}
			}
		}
		if !ok {
			continue
		}
		scoreDelta := child.Score - parentScore
		won := scoreDelta > 0

		if a.adaptiveDist != nil {
			a.adaptiveDist.RecordOutcome(
				child.StrategyMutationType,
				scoreDelta,
				0,
				won,
			)
		}

		if a.feedbackRecorder != nil {
			outcome := StrategyOutcome{
				StrategyID: child.ID,
				Success:    won,
				Score:      child.Score,
			}
			if err := a.feedbackRecorder.Register(ctx, outcome); err != nil {
				slog.WarnContext(ctx, "[GenomeAdapter] feedback recording failed",
					"strategy_id", child.ID,
					"error", err,
				)
			}
		}
	}
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

// PopulationSize returns the current population size for guardrail checks.
func (a *GenomePopulationAdapter) PopulationSize() int {
	if a.pop == nil {
		return 0
	}
	return len(a.pop.Agents)
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
	seen := make(map[string]bool, len(agents))
	for _, agent := range agents {
		if agent.ParentID == "" {
			continue
		}
		if agent.Version <= 1 {
			continue
		}

		key := agent.ParentID + "->" + agent.ID
		if seen[key] {
			continue
		}
		seen[key] = true

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
