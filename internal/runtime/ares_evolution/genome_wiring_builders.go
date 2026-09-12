package evolution

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/scoring"
)

// buildMutator creates the mutation pipeline from config.
func buildMutator(cfg SystemConfig) (*mutatorResult, error) {
	var mutatorOpts []mutation.MutatorOption
	if len(cfg.PromptTemplates) > 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithPromptPool(cfg.PromptTemplates))
	}
	// Wire the deployment-configured tool whitelist pool so the elite/random
	// mutation path can actually emit Params["tools"] choices. Previously this
	// option was never supplied here, making the pool path dead configuration —
	// only guided mutation produced tool choices (and always from registered-name
	// aliases). With a pool wired, both paths share the deployment's config as
	// the single source for the tool vocabulary. An empty pool keeps tool mutation
	// disabled (unchanged behavior).
	if len(cfg.ToolPool) > 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithToolPool(cfg.ToolPool))
	}
	if cfg.MutatorSeed != 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithSeed(cfg.MutatorSeed))
	}

	rawMutator, err := mutation.NewMutator(mutatorOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mutator: %w", err)
	}

	var genomeMut genome.MutatorInterface = rawMutator

	if cfg.EnableExperienceGuidedMutation && cfg.GuidanceProvider != nil {
		log.InfoContext(context.Background(), "experience-guided mutation requested; provider wired", "method", "buildMutator",
			"hint_provider", fmt.Sprintf("%T", cfg.GuidanceProvider))
		genomeMut = wrapGuidanceProvider(cfg.GuidanceProvider, rawMutator)
	} else if cfg.EnableExperienceGuidedMutation && cfg.GuidanceProvider == nil {
		log.WarnContext(context.Background(), "experience-guided mutation requested but no GuidanceProvider set", "method", "buildMutator")
	}

	var adaptiveDist *mutation.AdaptiveDistribution
	if cfg.AdaptiveDistConfig.Enabled {
		// Compose instead of either/or: when guidance is also enabled, the
		// adaptive distribution drives the GUIDED mutator (adaptive
		// probabilities seed the guided type sampling; unguided batches
		// still use the tuned probabilities via the base). Previously this
		// branch unconditionally wrapped rawMutator, silently discarding
		// the guided wrapper (REVIEW 3.4#5).
		inner := mutation.AdaptiveMutater(rawMutator)
		if guided, ok := genomeMut.(*mutation.ExperienceGuidedMutator); ok {
			inner = guided
		}
		adaptiveDist, err = mutation.NewAdaptiveDistribution(inner, cfg.AdaptiveDistConfig)
		if err != nil {
			return nil, fmt.Errorf("create adaptive distribution: %w", err)
		}
		genomeMut = adaptiveDist
	}

	crosserOpts := []genome.CrossoverOption{}
	if cfg.CrossoverSeed != 0 {
		crosserOpts = append(crosserOpts, genome.WithSeed(cfg.CrossoverSeed))
	}
	if cfg.PromptCrossoverMode != 0 {
		crosserOpts = append(crosserOpts, genome.WithPromptMode(
			genome.PromptCrossoverMode(cfg.PromptCrossoverMode),
		))
	}
	crosser, err := genome.NewCrossover(crosserOpts...)
	if err != nil {
		return nil, fmt.Errorf("create crossover: %w", err)
	}

	return &mutatorResult{
		rawMutator:   rawMutator,
		adaptiveDist: adaptiveDist,
		genomeMut:    genomeMut,
		crosser:      crosser,
	}, nil
}

// buildPopulation creates the genome population from config.
func buildPopulation(ctx context.Context, base *mutation.Strategy, cfg SystemConfig, mutResult *mutatorResult) (*genome.Population, error) {
	popOpts := []genome.PopulationOption{
		genome.WithPopulationSize(cfg.PopulationSize),
		genome.WithEliteCount(cfg.EliteCount),
		genome.WithMutationRate(cfg.MutationRate),
		genome.WithSurvivalRate(cfg.SurvivalRate),
		genome.WithDiversityThreshold(cfg.DiversityThreshold),
		genome.WithBreedingPoolRatio(cfg.BreedingPoolRatio),
		genome.WithFitnessSharingSampling(50, 30),
	}
	if cfg.PopulationSeed != 0 {
		popOpts = append(popOpts, genome.WithPopulationSeed(cfg.PopulationSeed))
	}
	if cfg.MinMutationRate > 0 {
		popOpts = append(popOpts, genome.WithMinMutationRate(cfg.MinMutationRate))
	}
	if cfg.MaxMutationRate > 0 {
		popOpts = append(popOpts, genome.WithMaxMutationRate(cfg.MaxMutationRate))
	}
	if cfg.MaxStagnantGenerations > 0 {
		popOpts = append(popOpts, genome.WithMaxStagnantGenerations(cfg.MaxStagnantGenerations))
	}
	if cfg.HistoryMaxSize > 0 {
		popOpts = append(popOpts, genome.WithHistoryEnabled(cfg.HistoryMaxSize))
	}
	if cfg.SelectionStrategy != "" {
		popOpts = append(popOpts, genome.WithSelectionStrategy(cfg.SelectionStrategy))
	}

	return genome.NewPopulation(ctx, base, mutResult.genomeMut, popOpts...)
}

// buildAdapterOptions creates GenomePopulationAdapter options from config.
func buildAdapterOptions(ctx context.Context, cfg SystemConfig) ([]GenomeAdapterOption, *scoring.TieredScorer, *scoring.Budget, *scoring.ScoreCache, error) {
	var opts []GenomeAdapterOption

	if cfg.Scorer != nil {
		opts = append(opts, WithAdapterScorer(cfg.Scorer))
	}

	heuristic := cfg.HeuristicScorer
	if heuristic == nil && cfg.Scorer != nil {
		heuristic = cfg.Scorer
	}
	if heuristic == nil {
		// Zero-token mode: no LLM scorer and no heuristic wired. The constant
		// baseline gives the GA a flat fitness landscape — evolution selection
		// is effectively random. This is the accepted backward-compat default
		// (the shadow gate is NOT affected by this heuristic —
		// buildShadowEvaluator only wires a scorer when cfg.Scorer != nil, so
		// the gate stays fail-closed in zero-token mode or uses the ReplayScorer
		// when a store is present).
		log.WarnContext(ctx, "No heuristic scorer configured; evolution runs with a constant baseline (50.0). "+
			"The G2 shadow gate is unaffected (fail-closed until ReplayScorer is wired).",
			"method", "buildAdapterOptions",
		)
		heuristic = genome.ConstantScorer(50.0)
	}

	cache := scoring.NewScoreCache(cfg.ScoreCacheSize)
	cache.SetMaxCacheAge(2) // re-evaluate strategies every 3 generations
	if cfg.MaxLLMCallsPerGeneration <= 0 {
		cfg.MaxLLMCallsPerGeneration = 100
	}
	budget, err := scoring.NewBudget(cfg.MaxLLMCallsPerGeneration)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create budget: %w", err)
	}

	var llmScorer genome.ScorerFunc
	if cfg.Scorer != nil {
		llmScorer = cfg.Scorer
	}

	tieredCfg := scoring.TieredScorerConfig{
		Cache:           cache,
		Budget:          budget,
		HeuristicScorer: heuristic,
		LLMScorer:       llmScorer,
	}
	tiered, err := scoring.NewTieredScorer(tieredCfg)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create tiered scorer: %w", err)
	}
	opts = append(opts, WithAdapterTieredScoring(tiered, budget, cache))

	if cfg.MemoryAwareScoringConfig.Enabled && cfg.MemoryExperienceProvider != nil {
		memScorer, err := scoring.NewMemoryAwareScorer(tiered, cfg.MemoryExperienceProvider,
			cfg.MemoryAwareScoringConfig)
		if err != nil {
			log.WarnContext(context.Background(), "failed to create memory-aware scorer, skipping", "method", "buildAdapterOptions",
				"error", err)
		} else {
			opts = append(opts, WithAdapterMemoryAwareScoring(memScorer))
		}
	}

	if cfg.BatchScorer != nil {
		opts = append(opts, WithAdapterBatchScoring(cfg.BatchScorer))
	}

	if cfg.Guardrails != nil {
		opts = append(opts, WithAdapterGuardrails(cfg.Guardrails))
	}

	return opts, tiered, budget, cache, nil
}

// mutatorAdapter adapts a genome.MutatorInterface to evolution.MutatorInterface
// by converting between Strategy types.
type mutatorAdapter struct {
	inner genome.MutatorInterface
}

func (a *mutatorAdapter) Mutate(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
	ms, err := a.inner.Mutate(ctx, strategyToMutation(&parent), n)
	if err != nil {
		return nil, fmt.Errorf("mutator adapter: %w", err)
	}
	res := make([]Strategy, len(ms))
	for i, m := range ms {
		res[i] = *mutationToStrategy(m)
	}
	return res, nil
}

func strategyToMutation(s *Strategy) *mutation.Strategy {
	if s == nil {
		return nil
	}
	params := make(map[string]any, len(s.Params))
	for k, v := range s.Params {
		params[k] = v
	}
	return &mutation.Strategy{
		ID:                   s.ID,
		Version:              s.Version,
		Params:               params,
		ParentID:             s.ParentID,
		PromptTemplate:       s.PromptTemplate,
		StrategyMutationType: parseMutationType(s.StrategyMutationType),
		MutationDesc:         s.MutationDesc,
		Score:                s.Score,
		CreatedAt:            s.CreatedAt,
	}
}

func mutationToStrategy(s *mutation.Strategy) *Strategy {
	if s == nil {
		return nil
	}
	params := make(map[string]any, len(s.Params))
	for k, v := range s.Params {
		params[k] = v
	}
	return &Strategy{
		ID:                   s.ID,
		Version:              s.Version,
		Params:               params,
		ParentID:             s.ParentID,
		PromptTemplate:       s.PromptTemplate,
		StrategyMutationType: s.StrategyMutationType.String(),
		MutationDesc:         s.MutationDesc,
		Score:                s.Score,
		CreatedAt:            s.CreatedAt,
	}
}

func parseMutationType(s string) mutation.MutationType {
	for _, mt := range []mutation.MutationType{
		mutation.MutationParameter,
		mutation.MutationPrompt,
		mutation.MutationTool,
	} {
		if mt.String() == s {
			return mt
		}
	}
	return mutation.MutationParameter
}

// buildDreamCycle creates the dream cycle orchestrator from config.
// Returns nil without error when dream cycle is not used.
func buildDreamCycle(mutator MutatorInterface, cfg SystemConfig) (*DreamCycle, error) {
	dreamCfg := DefaultDreamCycleConfig()
	dreamCfg.MinTasksBeforeEvolve = cfg.MinTasksBeforeEvolve
	dreamCfg.MaxMutations = cfg.MaxMutations

	var tester TesterInterface
	if cfg.Scorer != nil {
		var err error
		tester, err = NewRegressionTester(cfg.Scorer)
		if err != nil {
			return nil, fmt.Errorf("create regression tester: %w", err)
		}
	}

	dreamOpts := []DreamCycleOption{
		WithDreamCycleConfig(dreamCfg),
	}
	if cfg.Guardrails != nil {
		dreamOpts = append(dreamOpts, WithDreamCycleGuardrails(cfg.Guardrails))
	}
	if cfg.StrategyStore != nil {
		dreamOpts = append(dreamOpts, WithStrategyStore(cfg.StrategyStore))
	}
	if cfg.Metrics != nil {
		dreamOpts = append(dreamOpts, WithDreamCycleMetrics(cfg.Metrics))
	}
	if cfg.HintProvider != nil {
		dreamOpts = append(dreamOpts, WithDreamCycleHintProvider(cfg.HintProvider))
	}

	return NewDreamCycle(nil, mutator, tester, nil, dreamOpts...)
}

// buildScheduler creates the evolution scheduler from config.
func buildScheduler(cfg SystemConfig, popAdapter *GenomePopulationAdapter, dreamCycle *DreamCycle) *EvolutionScheduler {
	schedulerOpts := []SchedulerOption{
		WithTrigger(cfg.SchedulerTrigger),
	}
	if cfg.Guardrails != nil {
		schedulerOpts = append(schedulerOpts, WithSchedulerGuardrails(cfg.Guardrails))
	}

	scheduler := NewEvolutionScheduler(cfg.EventStore, popAdapter, schedulerOpts...)
	scheduler.SetDreamCycle(dreamCycle)
	return scheduler
}

// buildFeedbackRecorder creates a FeedbackRecorder if FeedbackService is set.
func buildFeedbackRecorder(cfg SystemConfig) *FeedbackRecorder {
	if cfg.FeedbackService == nil {
		return nil
	}
	return NewFeedbackRecorder(cfg.FeedbackService)
}
