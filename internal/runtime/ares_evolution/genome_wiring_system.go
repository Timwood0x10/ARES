package evolution

import (
	"context"
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/scoring"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/diff"
	evogenome "github.com/Timwood0x10/ares/internal/runtime/evolution/genome"
	aresExperience "github.com/Timwood0x10/ares/internal/runtime/memory/experience"
	"github.com/Timwood0x10/ares/internal/runtime/observability"
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
	Metrics               *observability.PrometheusMetrics

	// Intelligence components. Set to nil to disable.
	Reflector     *genome.LLMReflector        `json:"-"`
	HypothesisGen *genome.HypothesisGenerator `json:"-"`
	MetaCtrl      *genome.MetaController      `json:"-"`

	// Lifecycle is the strategy orchestrator. When set,
	// it is the sole entry point for promoting a candidate strategy.
	// Run() submits to Lifecycle.Submit instead of Deploy directly.
	Lifecycle *StrategyLifecycle `json:"-"`

	// Diff Engine + Coordinator for graph structure evolution.
	// When set, each generation's mutation is diffed and patches submitted.
	DiffReg     *diff.Registry                    `json:"-"`
	Coordinator *coordinator.EvolutionCoordinator `json:"-"`
	GenomeReg   *evogenome.Registry               `json:"-"`

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
	// DeterministicScorerEnabled indicates that a zero-LLM deterministic
	// scorer is wired. When true, the shadow gate's hasScorer check
	// passes even without an LLM scorer, so the shadow gate stays registered
	// and can produce shadow comparison evidence from execution attribution
	// alone. This breaks the "zero-token ⇒ no shadow gate" deadlock.
	DeterministicScorerEnabled bool `json:"deterministic_scorer_enabled,omitempty"`
}

// MutationConfig groups mutation and crossover settings.
type MutationConfig struct {
	MutatorSeed         int64    `json:"mutator_seed,omitempty"`
	CrossoverSeed       int64    `json:"crossover_seed,omitempty"`
	PromptCrossoverMode int      `json:"prompt_crossover_mode"`
	PromptTemplates     []string `json:"prompt_templates,omitempty"`
	// ToolPool is the set of tool-whitelist configurations the mutator may emit
	// as Params["tools"] (each entry is a comma-separated whitelist string). It
	// is wired to mutation.WithToolPool so the elite/random mutation path can
	// actually produce tool choices from the deployment's config; without it the
	// pool was dead configuration (only guided mutation produced tool choices).
	ToolPool                       []string                            `json:"tool_pool,omitempty"`
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
	EnableScheduler      bool                 `json:"enable_scheduler"`
	EnableDreamCycle     bool                 `json:"enable_dream_cycle"`
	SchedulerTrigger     EvolutionTrigger     `json:"scheduler_trigger"`
	MinTasksBeforeEvolve int                  `json:"min_tasks_before_evolve"`
	MaxMutations         int                  `json:"max_mutations"`
	EventStore           EventStoreSubscriber `json:"-"`
}

// DependencyConfig groups externally injected dependencies.
type DependencyConfig struct {
	StrategyStore        StrategyStore                    `json:"-"`
	Guardrails           *EvolutionGuardrails             `json:"-"`
	Metrics              *observability.PrometheusMetrics `json:"-"`
	FeedbackService      *aresExperience.FeedbackService  `json:"-"`
	HintProvider         mutation.HintProvider            `json:"-"`
	RollbackPolicyConfig RollbackPolicyConfig             `json:"rollback_policy,omitempty"`
	ShadowEvalConfig     ShadowEvaluationConfig           `json:"shadow_eval_config,omitempty"`
}

// SystemConfig holds configuration for creating a wired evolution system.
// Sub-configs are anonymous-embedded so all fields are accessible directly.
type SystemConfig struct {
	GenomeConfig
	ScoringConfig
	MutationConfig
	SchedulerConfig
	DependencyConfig

	// Lifecycle, when non-nil, overrides the default StrategyLifecycle and
	// RuntimeFitnessAggregator configuration (fitness window, judge
	// thresholds, weights, watch interval, gate settings). Bootstrap wires
	// it from the evolution YAML config; nil keeps the
	// code defaults from DefaultLifecycleConfig.
	Lifecycle *LifecycleConfig
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
		ScoringConfig: ScoringConfig{
			MemoryAwareScoringConfig: scoring.DefaultMemoryAwareScoringConfig(),
		},
		MutationConfig: MutationConfig{
			EnableExperienceGuidedMutation: true,
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

// GenerationActive reports whether any evolution generation is currently
// executing — the GA dream cycle or a population-adapter run. The live-chaos
// loop polls this to honor the GA quiet window.
func (s *WiredEvolutionSystem) GenerationActive() bool {
	if s == nil {
		return false
	}
	if s.DreamCycle != nil && s.DreamCycle.GenerationActive() {
		return true
	}
	return s.PopAdapter != nil && s.PopAdapter.GenerationActive()
}

// NewWiredEvolutionSystem creates and wires a complete evolution system.
// NewWiredEvolutionSystem creates and wires a complete evolution system. The
// core (mutator, population, adapter, dream cycle) is built here; the optional
// governance/scheduler/lifecycle/feedback subsystems are attached by the
// helpers below so each stays within the function-size and param-count limits.
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

	adapterOpts, tiered, budget, cache, err := buildAdapterOptions(ctx, cfg)
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
	// Lineage write side: the adapter's Run is the production evolution
	// path, so it must record lineage into the system's genealogy recorder
	// (RunIdleEvolution — the only previous caller — has no production
	// driver).
	popAdapter.genealogy = system.Genealogy
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
	if err := attachStrategyGovernance(base, cfg, system, tiered, popAdapter); err != nil {
		return nil, err
	}
	attachSchedulerAndLifecycle(cfg, system, popAdapter, dreamCycle)
	attachFeedbackAndMetrics(cfg, system, popAdapter)
	return system, nil
}

// attachStrategyGovernance wires the strategy store, ActiveStrategyManager and
// ShadowEvaluator onto the system (and back into the population adapter and
// dream cycle).
func attachStrategyGovernance(
	base *mutation.Strategy,
	cfg SystemConfig,
	system *WiredEvolutionSystem,
	tiered *scoring.TieredScorer,
	popAdapter *GenomePopulationAdapter,
) error {
	if cfg.StrategyStore != nil {
		system.StrategyStore = cfg.StrategyStore
	}

	// The ASM is built whenever a strategy store exists — no longer
	// gated on RollbackPolicyConfig.Enabled. The rollback policy stays armed
	// or disarmed via LifecycleConfig.RollbackArmed (watch-loop behavior),
	// because the lifecycle AND its gate pipeline must exist even when the
	// rollback net is disarmed: that is exactly the posture where the
	// shadow gate re-arms fail-closed (see shadowGateMode in ares_bootstrap).
	if cfg.StrategyStore != nil {
		asm, err := buildActiveStrategyManager(cfg)
		if err != nil {
			return fmt.Errorf("build active strategy manager: %w", err)
		}
		system.ActiveStrategyManager = asm
		popAdapter.activeStrategyMgr = asm
		if system.DreamCycle != nil {
			system.DreamCycle.stateManager = asm
		}
	}

	// ShadowEvaluator is built whenever shadow evaluation is
	// enabled — not only when an LLM scorer exists. buildShadowEvaluator
	// is nil-scorer-safe, and the StrategyLifecycle's shadow gate needs
	// the evaluator instance to exist so it can judge comparisons fed by
	// whichever sampler is active (DreamCycle when enabled). With the old
	// `&& cfg.Scorer != nil` condition the shadow gate silently vanished in
	// every default config (LLM scoring off) — a gate that doesn't exist
	// cannot even pass through.
	if cfg.ShadowEvalConfig.Enabled {
		se := buildShadowEvaluator(cfg, tiered, base)
		system.ShadowEvaluator = se
		// ShadowEvaluator is no longer exclusively tied to DreamCycle.
		// When DreamCycle exists it still gets the evaluator for its internal
		// deploy path, but the StrategyLifecycle also gets it so the shadow
		// gate works independently of whether DreamCycle is enabled.
		if system.DreamCycle != nil {
			system.DreamCycle.shadowEvaluator = se
		}
	}
	return nil
}

// attachSchedulerAndLifecycle wires the scheduler and constructs the
// StrategyLifecycle (wrapping the ASM as the sole Deploy/Rollback caller) with
// its shadow gate, evaluator, sampler and metrics options.
func attachSchedulerAndLifecycle(
	cfg SystemConfig,
	system *WiredEvolutionSystem,
	popAdapter *GenomePopulationAdapter,
	dreamCycle *DreamCycle,
) {
	if cfg.EnableScheduler && cfg.EventStore != nil {
		system.Scheduler = buildScheduler(cfg, popAdapter, dreamCycle)
		system.Scheduler.SetEnabled(true)
		if dreamCycle != nil {
			dreamCycle.scheduler = system.Scheduler
		}
	}

	// Construct the StrategyLifecycle when an
	// ActiveStrategyManager is wired. It wraps the ASM so it is the sole
	// caller of Deploy/Rollback. The lifecycle is injected into the
	// population adapter so Run() submits to it instead of deploying
	// directly. The ShadowEvaluator and (optional) evaluator gates are
	// attached so the verify pipeline works without DreamCycle.
	if system.ActiveStrategyManager != nil {
		aggCfg := DefaultAggregatorConfig()
		lcCfg := DefaultLifecycleConfig()
		if cfg.Lifecycle != nil {
			lcCfg = *cfg.Lifecycle
			// The aggregator mirrors the lifecycle's judging knobs so both
			// stages apply the same cold-start/window semantics from YAML.
			aggCfg = AggregatorConfig{
				WindowSize:            lcCfg.FitnessWindow,
				MinSamplesBeforeJudge: lcCfg.MinSamplesBeforeJudge,
				ColdStartScore:        lcCfg.ColdStartScore,
				Weights:               lcCfg.Weights,
			}
		}
		agg := NewRuntimeFitnessAggregator(nil, aggCfg) // store set later by bootstrap
		lcOpts := []LifecycleOption{}
		// Propagate the wiring layer's explicit shadow-gate decision. The
		// gate's absence must be a decision (WithShadowGateDisabled), never an
		// emergent property of nil-checking — the reason travels with it and is
		// reported by the snapshot.
		if cfg.Lifecycle != nil && cfg.Lifecycle.DisableShadowGate {
			lcOpts = append(lcOpts, WithShadowGateDisabled(cfg.Lifecycle.ShadowGateSkipReason))
		}
		if system.ShadowEvaluator != nil {
			lcOpts = append(lcOpts, WithLifecycleShadowEvaluator(system.ShadowEvaluator))
			// Wire the task-level shadow feeder so the shadow gate has
			// candidate-vs-active comparison evidence when DreamCycle does
			// not feed any. Exactly ONE feeder may own StartShadow/
			// RecordResult: wiring both would let the sampler's StartShadow
			// reset DreamCycle's accumulated comparisons on every Submit.
			//
			// The condition is cfg.EnableDreamCycle, NOT system.DreamCycle
			// == nil: a DreamCycle INSTANCE is built whenever
			// EnableDreamCycle OR EnableScheduler is set (see needDreamCycle
			// above), and bootstrap runs EnableDreamCycle=false with
			// EnableScheduler=true — so a nil-check would skip the sampler in
			// every production config, i.e. exactly the case the sampler
			// exists to fix. Locked by TestWiring_ShadowSampler_WiredInBootstrapShape.
			if !cfg.EnableDreamCycle {
				// The replay evidence window width is configurable
				// (ShadowEvaluationConfig.ReplayWindowSpan). Zero keeps the
				// scorer's 10-minute default — an operator who never sets it
				// gets the same evidence granularity as before.
				lcOpts = append(lcOpts, WithLifecycleShadowSampler(
					NewShadowSampler(system.ShadowEvaluator, cfg.ShadowEvalConfig.MinSamples,
						WithReplayWindowSpan(cfg.ShadowEvalConfig.ReplayWindowSpan)),
				))
			}
		}
		if cfg.Metrics != nil {
			lcOpts = append(lcOpts, WithLifecycleMetrics(cfg.Metrics))
		}
		lc := NewStrategyLifecycle(system.ActiveStrategyManager, agg, lcCfg, lcOpts...)
		system.Lifecycle = lc
		popAdapter.lifecycle = lc
	}
}

// attachFeedbackAndMetrics wires the feedback recorder and propagates metrics
// into the adapter, system and dream cycle.
func attachFeedbackAndMetrics(cfg SystemConfig, system *WiredEvolutionSystem, popAdapter *GenomePopulationAdapter) {
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
}

// RegisterScheduler attaches the system's scheduler to its EventStore by
// subscribing for agent lifecycle events. Returns nil if no scheduler is
// configured.
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
		return nil, errors.New("system or population is nil")
	}
	stats := system.Population.Stats()
	if stats.Size == 0 {
		return nil, errors.New("population is empty")
	}
	return system.Population.Best(), nil
}
