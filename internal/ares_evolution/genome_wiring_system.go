package evolution

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/scoring"
	aresExperience "github.com/Timwood0x10/ares/internal/ares_experience"
	"github.com/Timwood0x10/ares/internal/ares_observability"
)

// WiredEvolutionSystem holds a fully wired autonomous evolution system.
type WiredEvolutionSystem struct {
	Scheduler             *EvolutionScheduler
	DreamCycle            *DreamCycle
	PopAdapter            *GenomePopulationAdapter
	Population            *genome.Population
	Genealogy             *PopulationGenealogyRecorder
	StrategyStore         StrategyStore
	ActiveStrategyManager *ActiveStrategyManager
	ShadowEvaluator       *ShadowEvaluator
	FeedbackRecorder      *FeedbackRecorder
	AdaptiveDist          *mutation.AdaptiveDistribution
	TieredScorer          *scoring.TieredScorer
	Budget                *scoring.Budget
	ScoreCache            *scoring.ScoreCache
	Metrics               *ares_observability.PrometheusMetrics

	// Intelligence components (Phase 3-5). Set to nil to disable.
	Reflector     *genome.LLMReflector        `json:"-"`
	HypothesisGen *genome.HypothesisGenerator `json:"-"`
	MetaCtrl      *genome.MetaController      `json:"-"`

	// AfterGeneration is called after each idle evolution generation with
	// the generation index and the system. When non-nil, it receives the
	// fully evolved state (population already scored, lineage recorded).
	// Can be used for promotion evaluation, report generation, or metrics.
	// Returning an error is non-fatal — the error is logged and evolution
	// continues to the next generation.
	AfterGeneration func(ctx context.Context, gen int, system *WiredEvolutionSystem) error `json:"-"`

	// AfterRun is called once after RunIdleEvolution completes all generations.
	// When non-nil, it receives the final system state after the evolution loop
	// ends. Can be used for final report generation, persistence, or cleanup.
	// Returning an error is non-fatal — the error is logged but not propagated.
	AfterRun func(ctx context.Context, system *WiredEvolutionSystem) error `json:"-"`
}

// ScoringConfig groups scorer pipeline settings.
type ScoringConfig struct {
	Scorer                   genome.ScorerFunc                `json:"-"`
	HeuristicScorer          genome.ScorerFunc                `json:"-"`
	BatchScorer              BatchScorer                      `json:"-"`
	MaxLLMCallsPerGeneration int                              `json:"max_llm_calls_per_generation,omitempty"`
	ScoreCacheSize           int                              `json:"score_cache_size,omitempty"`
	MemoryAwareScoringConfig scoring.MemoryAwareScoringConfig `json:"memory_aware_scoring,omitempty"`
	MemoryExperienceProvider scoring.ExperienceProvider       `json:"-"`
}

// MutationConfig groups mutation and crossover settings.
type MutationConfig struct {
	MutatorSeed                    int64                               `json:"mutator_seed,omitempty"`
	CrossoverSeed                  int64                               `json:"crossover_seed,omitempty"`
	PromptCrossoverMode            int                                 `json:"prompt_crossover_mode"`
	PromptTemplates                []string                            `json:"prompt_templates,omitempty"`
	EnableExperienceGuidedMutation bool                                `json:"enable_experience_guided_mutation,omitempty"`
	GuidanceProvider               GuidanceProvider                    `json:"-"`
	AdaptiveDistConfig             mutation.AdaptiveDistributionConfig `json:"adaptive_distribution,omitempty"`
}

// GenomeConfig groups population-level genetic algorithm settings.
type GenomeConfig struct {
	PopulationSize         int     `json:"population_size"`
	EliteCount             int     `json:"elite_count"`
	MutationRate           float64 `json:"mutation_rate"`
	MinMutationRate        float64 `json:"min_mutation_rate,omitempty"`
	MaxMutationRate        float64 `json:"max_mutation_rate,omitempty"`
	SurvivalRate           float64 `json:"survival_rate"`
	PopulationSeed         int64   `json:"population_seed,omitempty"`
	UseDeterministicIDs    bool    `json:"use_deterministic_ids,omitempty"`
	MaxStagnantGenerations int     `json:"max_stagnant_generations"`
	DiversityThreshold     float64 `json:"diversity_threshold"`
	BreedingPoolRatio      float64 `json:"breeding_pool_ratio"`
	HistoryMaxSize         int     `json:"history_max_size"`
	SelectionStrategy      string  `json:"selection_strategy,omitempty"`
}

// SchedulerConfig groups scheduler and dream cycle settings.
type SchedulerConfig struct {
	EnableScheduler      bool                             `json:"enable_scheduler"`
	EnableDreamCycle     bool                             `json:"enable_dream_cycle"`
	SchedulerTrigger     EvolutionTrigger                 `json:"scheduler_trigger"`
	MinTasksBeforeEvolve int                              `json:"min_tasks_before_evolve"`
	MaxMutations         int                              `json:"max_mutations"`
	Callbacks            ares_callbacks.CallbackRegistrar `json:"-"`
}

// DependencyConfig groups externally injected dependencies.
type DependencyConfig struct {
	StrategyStore        StrategyStore                         `json:"-"`
	Guardrails           *EvolutionGuardrails                  `json:"-"`
	Metrics              *ares_observability.PrometheusMetrics `json:"-"`
	FeedbackService      *aresExperience.FeedbackService       `json:"-"`
	HintProvider         mutation.HintProvider                 `json:"-"`
	RollbackPolicyConfig RollbackPolicyConfig                  `json:"rollback_policy,omitempty"`
	ShadowEvalConfig     ShadowEvaluationConfig                `json:"shadow_eval_config,omitempty"`
}

// SystemConfig holds configuration for creating a wired evolution system.
// Sub-configs are anonymous-embedded so all fields are accessible directly.
type SystemConfig struct {
	GenomeConfig
	ScoringConfig
	MutationConfig
	SchedulerConfig
	DependencyConfig
}

// DefaultSystemConfig returns sensible defaults.
func DefaultSystemConfig() SystemConfig {
	return SystemConfig{
		GenomeConfig: GenomeConfig{
			PopulationSize:         20,
			EliteCount:             3,
			MutationRate:           0.2,
			SurvivalRate:           0.6,
			MaxStagnantGenerations: 10,
			DiversityThreshold:     0.15,
			BreedingPoolRatio:      0.6,
		},
		SchedulerConfig: SchedulerConfig{
			EnableDreamCycle:     false,
			EnableScheduler:      false,
			MinTasksBeforeEvolve: 10,
			SchedulerTrigger:     TriggerOnIdle,
		},
	}
}

// mutatorResult holds the output of buildMutator.
type mutatorResult struct {
	rawMutator   *mutation.Mutator
	adaptiveDist *mutation.AdaptiveDistribution
	genomeMut    genome.MutatorInterface
	crosser      *genome.Crossover
}

// buildMutator creates the mutation pipeline from config.
func buildMutator(cfg SystemConfig) (*mutatorResult, error) {
	var mutatorOpts []mutation.MutatorOption
	if len(cfg.PromptTemplates) > 0 {
		mutatorOpts = append(mutatorOpts, mutation.WithPromptPool(cfg.PromptTemplates))
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
		el.Info(context.TODO(), "buildMutator", "experience-guided mutation requested; provider wired",
			"hint_provider", fmt.Sprintf("%T", cfg.GuidanceProvider))
		genomeMut = wrapGuidanceProvider(cfg.GuidanceProvider, rawMutator)
	} else if cfg.EnableExperienceGuidedMutation && cfg.GuidanceProvider == nil {
		el.Warn(context.TODO(), "buildMutator", "experience-guided mutation requested but no GuidanceProvider set")
	}

	var adaptiveDist *mutation.AdaptiveDistribution
	if cfg.AdaptiveDistConfig.Enabled {
		var err error
		adaptiveDist, err = mutation.NewAdaptiveDistribution(rawMutator, cfg.AdaptiveDistConfig)
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
func buildAdapterOptions(cfg SystemConfig) ([]GenomeAdapterOption, *scoring.TieredScorer, *scoring.Budget, *scoring.ScoreCache, error) {
	var opts []GenomeAdapterOption

	if cfg.Scorer != nil {
		opts = append(opts, WithAdapterScorer(cfg.Scorer))
	}

	heuristic := cfg.HeuristicScorer
	if heuristic == nil && cfg.Scorer != nil {
		heuristic = cfg.Scorer
	}
	if heuristic == nil {
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
			el.Warn(context.TODO(), "buildAdapterOptions", "failed to create memory-aware scorer, skipping",
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

	scheduler := NewEvolutionScheduler(cfg.Callbacks, popAdapter, schedulerOpts...)
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

// NewWiredEvolutionSystem creates and wires a complete evolution system.
func NewWiredEvolutionSystem(base *mutation.Strategy, cfg SystemConfig) (*WiredEvolutionSystem, error) {
	ctx := context.Background()

	mutResult, err := buildMutator(cfg)
	if err != nil {
		return nil, fmt.Errorf("build mutator: %w", err)
	}

	pop, err := buildPopulation(ctx, base, cfg, mutResult)
	if err != nil {
		return nil, fmt.Errorf("build population: %w", err)
	}

	system := &WiredEvolutionSystem{
		Population:   pop,
		Genealogy:    NewPopulationGenealogyRecorder(),
		AdaptiveDist: mutResult.adaptiveDist,
	}

	adapterOpts, tiered, budget, cache, err := buildAdapterOptions(cfg)
	if err != nil {
		return nil, fmt.Errorf("build adapter options: %w", err)
	}
	system.TieredScorer = tiered
	system.Budget = budget
	system.ScoreCache = cache

	popAdapter, err := NewGenomePopulationAdapter(pop, mutResult.genomeMut, mutResult.crosser, adapterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create population adapter: %w", err)
	}
	system.PopAdapter = popAdapter

	needDreamCycle := cfg.EnableDreamCycle || cfg.EnableScheduler
	var dreamCycle *DreamCycle
	if needDreamCycle {
		dreamMutator := &mutatorAdapter{inner: mutResult.genomeMut}
		dreamCycle, err = buildDreamCycle(dreamMutator, cfg)
		if err != nil {
			return nil, fmt.Errorf("build dream cycle: %w", err)
		}
		system.DreamCycle = dreamCycle
		dreamCycle.genealogy = system.Genealogy
		dreamCycle.population = pop
	}

	if cfg.StrategyStore != nil {
		system.StrategyStore = cfg.StrategyStore
	}

	if cfg.StrategyStore != nil && cfg.RollbackPolicyConfig.Enabled {
		asm, err := buildActiveStrategyManager(cfg)
		if err != nil {
			return nil, fmt.Errorf("build active strategy manager: %w", err)
		}
		system.ActiveStrategyManager = asm
		if system.DreamCycle != nil {
			system.DreamCycle.stateManager = asm
		}
	}

	if cfg.ShadowEvalConfig.Enabled && cfg.Scorer != nil {
		se := buildShadowEvaluator(cfg, base)
		system.ShadowEvaluator = se
		if system.DreamCycle != nil {
			system.DreamCycle.shadowEvaluator = se
		}
	}

	if cfg.EnableScheduler && cfg.Callbacks != nil {
		system.Scheduler = buildScheduler(cfg, popAdapter, dreamCycle)
		system.Scheduler.SetEnabled(true)
		if dreamCycle != nil {
			dreamCycle.scheduler = system.Scheduler
		}
	}

	system.FeedbackRecorder = buildFeedbackRecorder(cfg)
	if system.FeedbackRecorder != nil {
		popAdapter.feedbackRecorder = system.FeedbackRecorder
	}

	if cfg.Metrics != nil {
		popAdapter.metrics = cfg.Metrics
		system.Metrics = cfg.Metrics
		if system.DreamCycle != nil {
			system.DreamCycle.metrics = cfg.Metrics
		}
	}

	return system, nil
}

// buildActiveStrategyManager creates the active strategy manager.
func buildActiveStrategyManager(cfg SystemConfig) (*ActiveStrategyManager, error) {
	rpc := cfg.RollbackPolicyConfig
	var rbOpts []RollbackOption
	if rpc.DegradationThreshold > 0 {
		rbOpts = append(rbOpts, WithDegradationThreshold(rpc.DegradationThreshold))
	}
	if rpc.WindowSize > 0 {
		rbOpts = append(rbOpts, WithRollbackWindowSize(rpc.WindowSize))
	}
	if rpc.MinSamples > 0 {
		rbOpts = append(rbOpts, WithMinRollbackSamples(rpc.MinSamples))
	}
	rollbackPolicy := NewRollbackPolicy(rbOpts...)

	asmOpts := []ASMOption{}
	if cfg.Guardrails != nil {
		asmOpts = append(asmOpts, WithASMGuardrails(cfg.Guardrails))
	}
	return NewActiveStrategyManager(cfg.StrategyStore, rollbackPolicy, asmOpts...)
}

// buildShadowEvaluator creates the shadow evaluator with optional scorer.
func buildShadowEvaluator(cfg SystemConfig, baseStrategy *mutation.Strategy) *ShadowEvaluator {
	shadowEval := NewShadowEvaluator(cfg.ShadowEvalConfig)
	shadowEval.SetActiveStrategy(baseStrategy)
	if cfg.Scorer != nil {
		scorer := cfg.Scorer
		shadowEval.SetShadowScorer(func(_ context.Context, s *mutation.Strategy) float64 {
			return scorer(s)
		})
	}
	el.Info(context.TODO(), "buildShadowEvaluator", "shadow evaluation enabled",
		"min_samples", cfg.ShadowEvalConfig.MinSamples,
		"min_win_rate", cfg.ShadowEvalConfig.MinWinRate,
		"active_strategy", baseStrategy.ID,
		"independent_scorer", cfg.Scorer != nil,
	)
	return shadowEval
}

// guidanceHintAdapter adapts an evolution.GuidanceProvider to mutation.HintProvider.
type guidanceHintAdapter struct {
	inner GuidanceProvider
}

func (a *guidanceHintAdapter) HintsForTask(ctx context.Context, taskType string, limit int) ([]mutation.EvolutionHint, error) {
	hints, err := a.inner.HintsForTask(ctx, taskType, limit)
	if err != nil {
		return nil, err
	}
	res := make([]mutation.EvolutionHint, len(hints))
	for i, h := range hints {
		res[i] = mutation.EvolutionHint{
			ID:                  h.ID,
			TaskType:            h.TaskType,
			Problem:             h.Problem,
			Solution:            h.Solution,
			Constraints:         h.Constraints,
			FailedPatterns:      h.FailedPatterns,
			PreferredTools:      h.PreferredTools,
			PromptSnippets:      h.PromptSnippets,
			ParamHints:          h.ParamHints,
			Confidence:          h.Confidence,
			SourceExperienceIDs: h.SourceExperienceIDs,
		}
	}
	return res, nil
}

func (a *guidanceHintAdapter) RecordStrategyOutcome(ctx context.Context, outcome mutation.StrategyOutcome) error {
	return a.inner.RecordStrategyOutcome(ctx, StrategyOutcome{
		StrategyID:    outcome.StrategyID,
		TaskType:      outcome.TaskType,
		Success:       outcome.Success,
		Score:         outcome.Score,
		Cost:          outcome.Cost,
		LatencyMs:     outcome.LatencyMs,
		MutationType:  outcome.MutationType,
		ExperienceIDs: outcome.ExperienceIDs,
		Timestamp:     outcome.Timestamp,
	})
}

// wrapGuidanceProvider wraps an evolution GuidanceProvider around a raw mutator
// using mutation.NewExperienceGuidedMutator.
func wrapGuidanceProvider(provider GuidanceProvider, raw *mutation.Mutator) genome.MutatorInterface {
	adaptedProvider := &guidanceHintAdapter{inner: provider}
	guided, err := mutation.NewExperienceGuidedMutator(raw, adaptedProvider)
	if err != nil {
		el.Warn(context.TODO(), "wrapGuidanceProvider", "failed to create ExperienceGuidedMutator, falling back to raw mutator",
			"error", err)
		return raw
	}
	el.Info(context.TODO(), "wrapGuidanceProvider", "experience-guided mutation enabled",
		"provider", fmt.Sprintf("%T", provider),
	)
	return guided
}

// RegisterScheduler attaches the system's scheduler OnAgentEnd handler to its
// callback registrar. Returns nil if no scheduler is configured.
func RegisterScheduler(system *WiredEvolutionSystem) error {
	if system == nil || system.Scheduler == nil {
		return nil
	}
	system.Scheduler.Register()
	return nil
}

// Shutdown gracefully shuts down the evolution scheduler if configured.
func Shutdown(system *WiredEvolutionSystem) {
	if system != nil && system.Scheduler != nil {
		system.Scheduler.Shutdown()
	}
}

// BestStrategyFromSystem returns the highest-scoring strategy from the population.
func BestStrategyFromSystem(system *WiredEvolutionSystem) (*mutation.Strategy, error) {
	if system == nil || system.Population == nil {
		return nil, fmt.Errorf("system or population is nil")
	}
	stats := system.Population.Stats()
	if stats.Size == 0 {
		return nil, fmt.Errorf("population is empty")
	}
	return system.Population.Best(), nil
}

// RunIdleEvolution runs N generations of idle evolution on the wired system.
func RunIdleEvolution(ctx context.Context, system *WiredEvolutionSystem, n int) error {
	if system == nil || system.PopAdapter == nil || system.Population == nil {
		return fmt.Errorf("system, pop adapter, and population must not be nil")
	}

	for gen := 0; gen < n; gen++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Capture parent snapshot BEFORE evolving so lineage can reference
		// pre-evolution agent scores for ScoreImprovement computation.
		var parentSnapshot []*mutation.Strategy
		if system.Genealogy != nil {
			parentSnapshot, _ = system.Population.Snapshot()
		}

		if err := system.PopAdapter.Run(ctx); err != nil {
			el.Warn(ctx, "RunIdleEvolution", "generation produced guardrail warning, continuing", "generation", system.Population.Generation,
				"run_iteration", gen,
				"error", err,
			)
		}

		if system.Genealogy != nil {
			_, err := RecordPopulationLineage(ctx, system.Population, system.Genealogy, parentSnapshot, gen)
			if err != nil {
				el.Warn(ctx, "RunIdleEvolution", "failed to record lineage", "generation", system.Population.Generation,
					"run_iteration", gen,
					"error", err,
				)
			}
		}

		// Run reflection cycle to analyze evolution patterns.
		if system.Reflector != nil && system.HypothesisGen != nil {
			history := system.Population.History()
			if len(history) > 0 {
				agents, _ := system.Population.Snapshot()
				ref, err := system.Reflector.Reflect(ctx, history, agents)
				if err != nil {
					el.Warn(ctx, "RunIdleEvolution", "reflection failed, skipping", "generation", system.Population.Generation,
						"run_iteration", gen,
						"error", err,
					)
				} else if ref != nil && len(ref.Recommendations) > 0 {
					hyps := system.HypothesisGen.Generate(ctx, ref)
					if len(hyps) > 0 {
						el.Info(ctx, "RunIdleEvolution", "generated hypotheses from reflection", "generation", system.Population.Generation,
							"run_iteration", gen,
							"count", len(hyps),
						)
					}
				}
			}
		}

		// Apply meta-controller tuning to self-adapt evolution hyperparameters.
		if system.MetaCtrl != nil {
			genome.ApplyMetaToPopulation(system.Population, system.MetaCtrl)
		}

		// Run the post-generation hook (promotion, report, etc.).
		if system.AfterGeneration != nil {
			if err := system.AfterGeneration(ctx, gen, system); err != nil {
				el.Warn(ctx, "RunIdleEvolution", "AfterGeneration hook failed", "generation", system.Population.Generation,
					"run_iteration", gen,
					"error", err,
				)
			}
		}
	}

	// Run the post-run hook for final report generation.
	if system.AfterRun != nil {
		if err := system.AfterRun(ctx, system); err != nil {
			el.Warn(ctx, "RunIdleEvolution", "AfterRun hook failed", "error", err)
		}
	}

	return nil
}
