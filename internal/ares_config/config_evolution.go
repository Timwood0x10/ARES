package ares_config

import (
	"github.com/Timwood0x10/ares/internal/runtime/evolution/deployment"
)

// EvolutionConfig holds genetic algorithm evolution system configuration.
// When Enabled is false (default), the entire evolution pipeline is skipped
// during bootstrap — no scheduler, no dream cycle, no GA overhead.
// This makes the genome/mutation libraries available as pure utilities while
// keeping the expensive evolution orchestration opt-in.
// EvolutionConfig controls the GA evolution pipeline.
// All fields have sensible defaults — only set what you need to override.
type EvolutionConfig struct {
	// Enabled activates the full evolution pipeline (scheduler + dream cycle + GA).
	// Default: false — must be explicitly enabled in YAML.
	Enabled bool `yaml:"enabled"`

	// PopulationSize is the number of agents in each GA generation.
	// Larger = more diverse search, slower per generation. Default: 20.
	PopulationSize int `yaml:"population_size"`

	// EliteCount is the number of top agents preserved unchanged per generation.
	// Prevents loss of the best solutions. Default: 2.
	EliteCount int `yaml:"elite_count"`

	// SurvivalRate is the fraction of population that survives selection [0.0, 1.0].
	// Higher = more diversity, slower convergence. Default: 0.6.
	SurvivalRate float64 `yaml:"survival_rate"`

	// MutationRate is the base probability of gene mutation per agent.
	// Higher = more exploration, less stability. Default: 0.2.
	MutationRate float64 `yaml:"mutation_rate"`

	// MinMutationRate is the floor for adaptive mutation rate decay.
	// Prevents mutation from dropping too low. Default: 0.05.
	MinMutationRate float64 `yaml:"min_mutation_rate"`

	// MaxMutationRate is the ceiling for adaptive mutation rate bursts.
	// Prevents excessive random search. Default: 0.5.
	MaxMutationRate float64 `yaml:"max_mutation_rate"`

	// Generations is the maximum number of GA generations to run.
	// 0 means unlimited (run until manually stopped). Default: 15.
	Generations int `yaml:"generations"`

	// BreedingPoolRatio is the fraction of population used as crossover parents.
	// Higher = more offspring from top individuals. Default: 0.5.
	BreedingPoolRatio float64 `yaml:"breeding_pool_ratio"`

	// MinInterval is the minimum time between evolution scheduler runs.
	// Format: duration string (e.g., "5m", "10m"). Default: "5m".
	MinInterval string `yaml:"min_interval"`

	// SelectionStrategy selects the parent selection algorithm.
	// Supported: "tournament", "rank", "roulette", "sus", "truncation", "random".
	// Default: "tournament".
	SelectionStrategy string `yaml:"selection_strategy"`

	// TournamentSize is the number of competitors per tournament selection round.
	// Larger = stronger selection pressure (faster convergence, less diversity).
	// Only used when selection_strategy is "tournament". Default: 3.
	TournamentSize int `yaml:"tournament_size"`

	// CrossoverType selects the parameter recombination strategy.
	// Supported: "uniform", "two_point", "segment".
	// Default: "uniform".
	CrossoverType string `yaml:"crossover_type"`

	// TargetFitness stops evolution when the best fitness reaches this threshold.
	// 0 means no target (run until Generations). Scale: 0-100.
	TargetFitness float64 `yaml:"target_fitness"`

	// SteadyState enables steady-state GA: each generation replaces only a fraction
	// of the population instead of full generational replacement.
	// Default: false.
	SteadyState bool `yaml:"steady_state"`

	// SteadyStateReplaceRate is the fraction of population replaced each generation
	// in steady-state mode [0.0, 1.0]. Only used when steady_state is true.
	// Default: 0.3.
	SteadyStateReplaceRate float64 `yaml:"steady_state_replace_rate"`

	// Deployment configures safe promotion of evolution patches to the live
	// runtime via the DeploymentPipeline. Disabled by default — when enabled,
	// accepted patches are promoted through staging → live instead of applied
	// directly by the Coordinator.
	Deployment deployment.DeploymentConfig `yaml:"deployment"`

	// LLMScoring configures the opt-in LLM-backed strategy scorer for the
	// GA evolution system. When Enabled is false (the default), evolution
	// uses the constant baseline scorer, preserving prior behavior.
	LLMScoring LLMScoringConfig `yaml:"llm_scoring"`

	// Lifecycle configures the StrategyLifecycle control plane: fitness
	// window, judge thresholds, JUDGE weights and the rollback
	// watch interval. Zero-value fields fall back to code defaults in
	// bootstrap, so an absent section preserves the built-in behavior.
	Lifecycle EvolutionLifecycleConfig `yaml:"lifecycle"`

	// Rollback configures degradation detection thresholds for the active
	// strategy. Scale is [0,1] — DegradationThreshold compares against a
	// window mean of normalized samples. Zero values fall back to code
	// defaults.
	Rollback EvolutionRollbackConfig `yaml:"rollback"`

	// Shadow configures shadow-evaluation thresholds for the G2 verify gate.
	// Zero values fall back to code defaults.
	Shadow EvolutionShadowConfig `yaml:"shadow"`

	// ShadowExecution configures real-execution shadow A/B for candidate
	// strategies: when enabled, a submitted
	// candidate is executed on buffered recent real tasks inside an isolated,
	// side-effect-free runner before the G2 gate judges it, producing
	// candidate-specific evidence. Default: disabled — G2 then replays each
	// strategy's own history, which is NOT candidate-specific for a
	// never-executed candidate.
	ShadowExecution ShadowExecutionConfig `yaml:"shadow_execution"`

	// ChannelFeedback configures the two perception channels evolution was
	// blind to: cross-agent collaboration receipts
	// and tool-call outcomes. Both default off — an agent perceives the world
	// only through task/tool/collaboration, and until an operator opts in, only
	// the task channel feeds the verdict.
	ChannelFeedback ChannelFeedbackConfig `yaml:"channel_feedback"`

	// evolution.tool_projection removed with its package (was default-disabled; unknown YAML keys are ignored, so
	// existing config files keep loading).
	// Gates configures the verify-gate pipeline thresholds (eval-suite
	// minimum score, manual approval hold). Zero values fall back to code
	// defaults.
	Gates EvolutionGateConfig `yaml:"gates"`

	// ToolPool is the list of tool-whitelist configurations the GA mutator may
	// emit as Params["tools"] (e.g. a "narrow" config naming only web_search and
	// a "broad" one naming everything). Each entry is a comma-separated tool
	// whitelist string written verbatim into a candidate's Params["tools"]. Empty
	// disables pool-based tool mutation (guided mutation may still produce tool
	// choices from experience hints). This is the SINGLE source for the mutator's
	// tool vocabulary — the value must name REGISTERED tools (see
	// EvolutionGuardrailsConfig.KnownTools), because a whitelist naming unknown
	// tools intersects to zero at runtime and the executors fall back to the full
	// set. Default: empty.
	ToolPool []string `yaml:"tool_pool"`

	// Guardrails configures the tool-set selection guardrails (upper bound,
	// require-any-tool, and the registered-tool vocabulary used to reject
	// whitelists naming unregistered tools). Absent = code defaults (bound
	// disabled, vocabulary disabled), preserving prior behavior.
	Guardrails EvolutionGuardrailsConfig `yaml:"guardrails"`
}

// EvolutionGuardrailsConfig mirrors the `evolution.guardrails` YAML block. It
// maps onto evolution.EvolutionGuardrails options in bootstrap. All fields are
// opt-in: zero/zero-value disables the corresponding check.
type EvolutionGuardrailsConfig struct {
	// MaxToolsEnabled is the upper bound on an evolved tool whitelist size.
	// 0 (default) disables the bound. A positive value rejects any candidate
	// whose Params["tools"] enables more than this many tools.
	MaxToolsEnabled int `yaml:"max_tools_enabled"`

	// RequireAnyTool, when true, rejects an evolved strategy that enables zero
	// tools. Off by default so text-only strategies are not rejected.
	RequireAnyTool bool `yaml:"require_any_tool"`

	// KnownTools is the REGISTERED tool vocabulary. A candidate whitelist naming
	// a tool not in this list is rejected at selection time — the runtime would
	// otherwise intersect an all-unknown whitelist to zero and silently fall back
	// to the FULL tool set, turning a "narrow" strategy into the broadest one.
	// Supplied in YAML as the actual registered tool names; empty disables the
	// check.
	KnownTools []string `yaml:"known_tools"`
}

// EvolutionLifecycleConfig mirrors the `evolution.lifecycle` YAML block.
// It maps onto evolution.LifecycleConfig in bootstrap.
// The `penalty` block (cost/latency budgets) is intentionally
// absent: task events carry no cost/latency data yet, so no config field
// is exposed for it (see the ares_evolution fitness_aggregator tech-debt
// note).
type EvolutionLifecycleConfig struct {
	// FitnessWindow is the number of runtime samples kept for rollback
	// evaluation. Default: 50.
	FitnessWindow int `yaml:"fitness_window"`
	// MinSamplesBeforeJudge is the minimum runtime sample count before
	// promote/rollback decisions are made. Default: 10.
	MinSamplesBeforeJudge int `yaml:"min_samples_before_judge"`
	// ColdStartScore is the fallback fitness when no evidence exists.
	// Default: 0.5.
	ColdStartScore float64 `yaml:"cold_start_score"`
	// OutcomeWeight weights task outcome samples in the JUDGE aggregate.
	// Zero-weight fields inherit the code defaults only when ALL weights
	// are unset; partial specs are used as-is (the aggregator normalizes
	// the sum at query time).
	OutcomeWeight float64 `yaml:"outcome_weight"`
	// DimensionEvalWeight weights dimension_eval evidence.
	DimensionEvalWeight float64 `yaml:"dimension_eval_weight"`
	// WorkflowWeight weights workflow-sourced fitness evidence.
	WorkflowWeight float64 `yaml:"workflow_weight"`
	// SchedulerWeight weights scheduler-sourced fitness evidence.
	SchedulerWeight float64 `yaml:"scheduler_weight"`
	// RecoveryWeight weights recovery-sourced fitness evidence.
	RecoveryWeight float64 `yaml:"recovery_weight"`
	// WatchInterval is the rollback watch-loop tick interval (duration
	// string, e.g. "30s"). Default: "30s". Valid values parse via
	// time.ParseDuration and must be positive; invalid strings are ignored
	// and the default applies.
	WatchInterval string `yaml:"watch_interval"`
	// BlacklistGenerations is how many generations a rolled-back candidate
	// stays banned from re-nomination (rollback-oscillation damping).
	// Default: 3.
	BlacklistGenerations int `yaml:"blacklist_generations"`
	// MinActiveDuration is how long a promoted strategy must stay active
	// before another candidate may replace it (duration string, e.g. "90s").
	// It throttles promote churn so the rollback window can accumulate
	// evidence between promotions. Default: 3 × watch_interval. Invalid or
	// non-positive strings fall back to the default.
	MinActiveDuration string `yaml:"min_active_duration"`
}

// EvolutionRollbackConfig mirrors the `evolution.rollback` YAML block.
type EvolutionRollbackConfig struct {
	// Enabled arms the automatic post-deployment rollback (the canary safety
	// net). Tri-state pointer: nil (absent) means true — an operator who does
	// not mention rollback gets it, because the promote path relies on it.
	// An explicit `enabled: false` disables the watch-loop rollback AND
	// re-arms the G2 shadow gate fail-closed (see the shadow-gate invariant
	// in ares_bootstrap): with neither pre- nor post-deployment verification,
	// refusing promotion is the only correct behavior.
	Enabled *bool `yaml:"enabled"`
	// DegradationThreshold is the mean-score drop fraction (on a [0,1]
	// scale) that triggers rollback. Default: 0.15.
	DegradationThreshold float64 `yaml:"degradation_threshold"`
	// WindowSize is the sliding-window length for degradation detection.
	// Default: 5.
	WindowSize int `yaml:"window_size"`
	// MinSamples is the minimum window sample count before a rollback
	// decision is made. Default: 3.
	MinSamples int `yaml:"min_samples"`
}

// IsEnabled reports whether automatic rollback is armed. Unset (nil)
// defaults to true: the rollback net is part of the promote path's safety
// contract, so only an explicit YAML false disarms it.
func (c EvolutionRollbackConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// EvolutionShadowConfig mirrors the `evolution.shadow` YAML block.
type EvolutionShadowConfig struct {
	// MinSamples is the minimum shadow-comparison count before the G2 gate
	// makes a deployment decision. Default: 20.
	MinSamples int `yaml:"min_samples"`
	// MinWinRate is the minimum shadow win rate for promotion [0,1].
	// Default: 0.55.
	MinWinRate float64 `yaml:"min_win_rate"`
	// ReplayWindowSpan is the width of ONE replay evidence window (duration
	// string, e.g. "10m"). Each comparison reads a distinct slice of history,
	// so MinSamples is satisfied by independent evidence. Zero/unset falls
	// back to the 10-minute default.
	ReplayWindowSpan string `yaml:"replay_window_span"`
	// ReplayQueryLimit caps the evidence records read per window query. Zero/
	// unset falls back to the default (200).
	ReplayQueryLimit int `yaml:"replay_query_limit"`
}

// ShadowExecutionConfig mirrors the `evolution.shadow_execution` YAML block
// for real-execution A/B of candidate strategies.
type ShadowExecutionConfig struct {
	// Enabled turns on real-execution shadow A/B. Default: false — when
	// disabled, the G2 gate judges candidates by replaying each strategy's
	// own history, which is not candidate-specific for a never-executed
	// candidate.
	Enabled bool `yaml:"enabled"`
	// SampleSize is how many of the most recent finalized real tasks each
	// candidate judgment executes in isolation (both A/B arms per task).
	// Default: 3. Non-positive values fall back to the default.
	SampleSize int `yaml:"sample_size"`
}

// ChannelFeedbackConfig mirrors the `evolution.channel_feedback` YAML block.
// It arms the collaboration and tool-call
// perception channels as evolution fitness dimensions.
//
// Each channel has an `enabled` switch AND a weight. The switch controls
// whether the observation is RECORDED (the producer-side instrumentation); the
// weight controls whether it COUNTS in the fitness aggregate. They are separate
// on purpose: an operator can turn a channel on to inspect the evidence in the
// audit trail before letting it move any verdict.
type ChannelFeedbackConfig struct {
	// CollabEnabled records cross-agent collaboration receipts (initiator,
	// target, topic, outcome, latency) as fitness evidence under
	// source="collaboration". Default: false.
	CollabEnabled bool `yaml:"collab_enabled"`
	// CollabWeight is the collaboration channel's weight in the JUDGE
	// aggregate. Default: 0 — recorded but not yet trusted to move a verdict.
	CollabWeight float64 `yaml:"collab_weight"`
	// ToolEnabled records tool-call outcomes (tool, caller, outcome, latency)
	// as fitness evidence under source="tool_call". Default: false.
	ToolEnabled bool `yaml:"tool_enabled"`
	// ToolWeight is the tool channel's weight in the JUDGE aggregate.
	// Default: 0 — same staged-adoption reasoning as CollabWeight.
	ToolWeight float64 `yaml:"tool_weight"`
}

// AnyEnabled reports whether at least one channel is armed. The wiring layer
// uses it to decide whether to build the recorder at all: with both channels
// off, constructing an observer that nothing feeds would be dead wiring.
func (c ChannelFeedbackConfig) AnyEnabled() bool {
	return c.CollabEnabled || c.ToolEnabled
}

// EvolutionGateConfig mirrors the `evolution.gates` YAML block.
type EvolutionGateConfig struct {
	// EvalMinScore is the minimum G3 eval-suite score for a candidate to
	// pass [0,1]. Default: 0.7.
	EvalMinScore float64 `yaml:"eval_min_score"`
	// RequireManualApproval holds candidates in SHADOW until an operator
	// calls POST /api/evolution/approve. Default: false.
	RequireManualApproval bool `yaml:"require_manual_approval"`
	// EvalSuite loads the G3 regression test suite from a YAML file
	// (eval.TestSuite schema: {name, description, test_cases: [...]}).
	// Empty string disables the G3 gate (it degrades to pass-through).
	EvalSuite string `yaml:"eval_suite"`
	// EvalStrict fails the G3 gate closed when eval infrastructure is
	// missing. Default false preserves backward compatibility;
	// production should set it true so an unwired gate cannot silently
	// pass every candidate.
	EvalStrict bool `yaml:"eval_strict"`
	// RegressionEnabled arms the arena preserved-case regression gate
	// (candidate vs active strategy A/B over the eval suite's cases, Welch
	// significance; rejects only on a significant drop). M-G2 default:
	// when eval_suite and the eval LLM client are available the gate arms
	// automatically (nil = auto). An explicit `regression_enabled: false`
	// is the documented opt-out (Warn-logged at bootstrap); an explicit
	// `true` also makes missing prerequisites a bootstrap error instead of
	// silent degradation. Each check costs 2×regression_runs LLM scoring
	// rounds.
	// Default: nil (auto-arm when infrastructure exists).
	RegressionEnabled *bool `yaml:"regression_enabled"`
	// RegressionRuns is the per-strategy run count for the regression gate
	// (baseline and compare each run the preserved cases this many times).
	// Default 5 when zero.
	RegressionRuns int `yaml:"regression_runs"`
	// RegressionMinWinRate is the informational win-rate floor surfaced in
	// the gate's pass reason. The REJECT decision is significance-based,
	// not win-rate-based. Default 0.55 when zero.
	RegressionMinWinRate float64 `yaml:"regression_min_win_rate"`
}

// LLMScoringConfig configures the opt-in LLM-backed strategy scorer for the
// GA evolution system. When Enabled is false (the default), evolution uses the
// constant baseline scorer (ConstantScorer(50.0)), preserving prior behavior
// and avoiding uncontrolled LLM API costs during tests.
type LLMScoringConfig struct {
	// Enabled activates the LLM-backed scorer. When false, the GA evolution
	// system falls back to the constant baseline scorer. Default: false.
	Enabled bool `yaml:"enabled"`

	// Seed enables deterministic LLM scoring when > 0. Forces the LLM
	// temperature to 0 and embeds the seed in the evaluation prompt so
	// identical strategies always receive the same score. Default: 0
	// (non-deterministic).
	Seed int64 `yaml:"seed"`

	// MaxCallsPerGeneration caps the number of LLM scoring calls per
	// generation to control cost. When the budget is exhausted, remaining
	// strategies are scored by the deterministic heuristic fallback.
	// Default: 100 when zero.
	MaxCallsPerGeneration int `yaml:"max_calls_per_generation"`
}
