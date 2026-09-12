package ares_bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/evidence"
	evoprovider "github.com/Timwood0x10/ares/internal/knowledge/provider/evolution"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	evoService "github.com/Timwood0x10/ares/internal/runtime/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/runtime/observability"
)

// wireGAEvolution wires the GA population adapter (step 9 of Bootstrap): it
// builds the GA system, attaches the coordinator bridge to the population
// adapter, and starts the background evolution ticker. Extracted from Bootstrap
// to keep its cyclomatic complexity within lint limits.
//
//nolint:gocyclo // it is a complex wiring hub with many config fields.

// buildStrategyStore selects the persistent (PostgreSQL) strategy store when a database is configured, falling back to the in-memory store otherwise. A PG store's dedicated *sql.DB is registered in the bootstrap cleanups so a FAILED bootstrap releases its connections instead of leaking them.

func buildStrategyStore(ctx context.Context, cfg *ares_config.Config, cleanups *[]func()) evolution.StrategyStore {
	var memStore evolution.StrategyStore
	if cfg.Storage.Enabled && cfg.Storage.Type == storageTypePostgres && cfg.Storage.Host != "" {
		pgStore, closePG, err := newPGStrategyStore(cfg)
		if err != nil {
			log.WarnContext(ctx, "bootstrap: PG strategy store init failed, falling back to in-memory", "error", err)
			memStore = evolution.NewMemoryStrategyStore(0)
		} else {
			memStore = pgStore
			// The PG store owns a dedicated *sql.DB that nothing else
			// closes: register it in the bootstrap cleanups so a FAILED
			// bootstrap (or a later wiring error in this function) releases
			// the connections instead of leaking them across bootstrap
			// retries. On success the handle lives for the process
			// lifetime, like every other bootstrap-owned store.
			if cleanups != nil {
				*cleanups = append(*cleanups, closePG)
			}
			log.InfoContext(ctx, "bootstrap: PG strategy store wired (persistent)")
		}
	} else {
		memStore = evolution.NewMemoryStrategyStore(0)
	}

	return memStore
}

// assembleGAConfig builds the GA SystemConfig from the YAML evolution block: rollback policy, shadow evaluation, replay window, lifecycle, metrics, tuning, guardrails, and the experience-guided mutation provider.

func assembleGAConfig(ctx context.Context, cfg *ares_config.Config, comp *Components, memStore evolution.StrategyStore, guidanceProvider evolution.GuidanceProvider) evolution.SystemConfig {
	gaCfg := evolution.DefaultSystemConfig()
	gaCfg.EnableDreamCycle = false
	// EnableScheduler is now always true so the ticker path goes
	// through scheduler.Tick (which applies shouldEvolve + guardrails).
	// When the legacy scheduler exists, SetAdapter is still called so it
	// can drive event-triggered evolution, but the ticker no longer
	// bypasses the scheduler's throttling.
	gaCfg.EnableScheduler = true
	gaCfg.EventStore = comp.EventStore
	gaCfg.StrategyStore = memStore
	// Rollback thresholds come from the evolution.rollback YAML
	// block; zero values fall back to the code defaults
	// that match the previous hardcoded wiring.
	// Enabled is now tri-state (nil defaults true) instead of hardcoded:
	// an operator can disarm the rollback net, which (a) stops the watch loop
	// from triggering and (b) re-arms the G2 gate fail-closed — see
	// shadowGateMode below.
	rbCfg := cfg.Evolution.Rollback
	rollbackArmed := rbCfg.IsEnabled()
	gaCfg.RollbackPolicyConfig = evolution.RollbackPolicyConfig{
		Enabled:              rollbackArmed,
		DegradationThreshold: defaultFloat(rbCfg.DegradationThreshold, 0.15),
		WindowSize:           defaultInt(rbCfg.WindowSize, 5),
		MinSamples:           defaultInt(rbCfg.MinSamples, 3),
	}
	// Enable shadow evaluation independently of DreamCycle.
	// Thresholds come from the evolution.shadow YAML block.
	shCfg := cfg.Evolution.Shadow
	gaCfg.ShadowEvalConfig = evolution.ShadowEvaluationConfig{
		Enabled:    true,
		MinSamples: defaultInt(shCfg.MinSamples, 20),
		MinWinRate: defaultFloat(shCfg.MinWinRate, 0.55),
	}
	// The replay evidence window width is YAML-configurable (duration
	// string). Zero/unset keeps the scorer's 10-minute default; an invalid
	// string is ignored, never fatal — the operator's typo must not take down
	// the evolution plane.
	//
	// The per-window query limit (evolution.shadow.replay_query_limit) is NOT
	// carried here: the ReplayScorer is constructed in the serve layer
	// (cmd/ares/agent_kernel.go), which reads the YAML directly. Duplicating it
	// into ShadowEvalConfig would create a second, unread copy of the same
	// knob.
	if raw := shCfg.ReplayWindowSpan; raw != "" {
		if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
			gaCfg.ShadowEvalConfig.ReplayWindowSpan = d
			// The sampler walks MinSamples windows BACKWARDS from now, so
			// span × MinSamples is the total history horizon. A wide span with
			// the default query limit (200 records/window, no server-side
			// strategy filter) makes truncation likely and pushes the oldest
			// window far out of date — stale evidence judged as current. Warn
			// rather than clamp: the operator may genuinely want a long
			// horizon on a low-traffic deployment.
			if horizon := d * time.Duration(gaCfg.ShadowEvalConfig.MinSamples); horizon > maxShadowReplayHorizon {
				log.WarnContext(ctx, "bootstrap: evolution.shadow replay horizon is very wide — oldest comparison reads stale evidence and wide windows are more likely to hit replay_query_limit",
					"replay_window_span", d, "min_samples", gaCfg.ShadowEvalConfig.MinSamples,
					"horizon", horizon, "recommended_max", maxShadowReplayHorizon)
			}
		} else {
			log.WarnContext(ctx, "bootstrap: ignoring invalid evolution.shadow.replay_window_span", "value", raw, "error", perr)
		}
	}
	// The lifecycle control plane (window/judge/gates/watch
	// interval) is YAML-configurable; the same config also feeds the G3
	// eval-gate MinScore further below.
	gaCfg.Lifecycle = lifecycleConfigFromYAML(cfg.Evolution.Lifecycle, cfg.Evolution.Gates, cfg.Evolution.ChannelFeedback)
	// The lifecycle must know whether the rollback net is armed (it owns
	// the watch loop) — the same tri-state decision as the ASM wiring above.
	gaCfg.Lifecycle.RollbackArmed = rollbackArmed
	// Wire the shared Prometheus metrics into the GA system so the
	// lifecycle counters (promote/rollback/gate-reject) are actually
	// incremented in production instead of registered-but-never-updated.
	// NewPrometheusMetrics is idempotent (AlreadyRegisteredError returns the
	// cached instance created by provide_llm), so this reuses the same
	// collector the /metrics endpoint serves.
	if m, merr := observability.NewPrometheusMetrics(); merr == nil {
		gaCfg.Metrics = m
	} else {
		log.WarnContext(ctx, "bootstrap: evolution metrics wiring skipped", "error", merr)
	}

	// Honor the YAML evolution tuning. Only fields with a matching
	// SystemConfig slot are wired; the rest of the YAML GA knobs
	// (TournamentSize/CrossoverType/SteadyState*) are registered as dead
	// config pending a SystemConfig slot.
	ec := &cfg.Evolution
	applyGATuning(&gaCfg, ec)
	// Construct the guardrails. Until now gaCfg.Guardrails was NEVER
	// assigned anywhere in this package, so WithAdapterGuardrails was skipped
	// and both runPreGuardrails and the legacy scheduler's checkGuardrails
	// short-circuited on nil — G1 was a gate that existed in code and did
	// nothing at runtime. The adapter layer — described as G1's "only real
	// defense" — was inert too.
	gaCfg.Guardrails = buildEvolutionGuardrails(ctx, ec, gaCfg.Metrics)
	// Feed distilled experiences back into the GA's
	// experience-guided mutation. guidanceProvider is non-nil only when
	// distillation was successfully wired above (PG + embedding configured).
	gaCfg.GuidanceProvider = guidanceProvider
	gaCfg.EnableExperienceGuidedMutation = guidanceProvider != nil

	return gaCfg
}

// wireScorerAndShadowGate wires the optional LLM-backed scorer (or the deterministic zero-LLM fallback) and decides the G2 shadow-gate posture before the system is constructed, so the lifecycle is built with the gate already suppressed or kept.

func wireScorerAndShadowGate(ctx context.Context, cfg *ares_config.Config, comp *Components, gaCfg *evolution.SystemConfig, rollbackArmed bool) {
	// Opt-in LLM-backed scorer. When enabled and an LLM
	// client is available, override the default constant baseline scorer
	// with the LLM scorer + deterministic heuristic fallback. When disabled
	// (the default), gaCfg.Scorer stays nil and buildAdapterOptions falls
	// back to ConstantScorer(50.0), preserving prior behavior.
	llmScorer, llmHeuristic, llmMaxCalls := wireLLMScorer(cfg, comp)
	if llmScorer != nil {
		gaCfg.Scorer = llmScorer
		gaCfg.HeuristicScorer = llmHeuristic
		if llmMaxCalls > 0 {
			gaCfg.MaxLLMCallsPerGeneration = llmMaxCalls
		}
		// A fixed seed forces the LLM to temperature 0 + prompt-embedded seed
		// → deterministic output. The sampler's comparisons are then identical
		// (MinSamples satisfied by repetition, not by independent evidence).
		// buildShadowEvaluator logs a warning when this is set.
		if cfg.Evolution.LLMScoring.Seed > 0 {
			gaCfg.ShadowEvalConfig.DeterministicScorer = true
		}
	}

	// When LLM scoring is off (the default), the zero-LLM deterministic
	// scorer takes over as the independent evidence source. The scorer is
	// wired at runtime by the serve layer (cmd/ares/agent_kernel.go) once the
	// ExecutionAttribution is created, but the shadow gate posture must be
	// decided HERE (before NewWiredEvolutionSystem). Setting
	// DeterministicScorerEnabled=true makes hasScorer pass without an LLM,
	// so the G2 gate stays registered and can produce shadow comparison
	// evidence from execution attribution alone.
	if llmScorer == nil {
		gaCfg.DeterministicScorerEnabled = true
	}

	// Decide the G2 shadow-gate posture BEFORE wiring the system, so the
	// lifecycle is constructed with the gate already suppressed (or kept)
	// instead of un-registering it afterwards. The invariant: skipping
	// PRE-deployment verification is allowed only when POST-deployment
	// verification is armed; with neither, G2 stays fail-closed.
	// hasScorer ⇔ gaCfg.Scorer != nil: buildShadowEvaluator sets an
	// independent scorer on the evaluator exactly when cfg.Scorer is wired
	// (the heuristic-only TieredScorer is NOT independent evidence).
	// When a deterministic (zero-LLM) scorer is wired, it counts as
	// independent evidence too — the attribution-derived score is a
	// legitimate comparison source. This breaks the "zero-token ⇒ no G2"
	// deadlock: with DeterministicScorerEnabled, the G2 gate stays
	// registered and produces shadow comparison evidence from execution
	// attribution alone, without any LLM call.
	hasScorer := gaCfg.Scorer != nil || gaCfg.DeterministicScorerEnabled
	register, gateReason, gateErr := shadowGateMode(hasScorer, rollbackArmed)
	if !register && errors.Is(gateErr, errShadowGateNotConfigured) {
		gaCfg.Lifecycle.DisableShadowGate = true
		gaCfg.Lifecycle.ShadowGateSkipReason = gateReason
		log.WarnContext(ctx, "evolution: pre-deployment shadow gate NOT registered",
			"reason", gateReason,
			"mitigation", "candidates promote directly; degradation triggers automatic rollback",
			"rollback_armed", rollbackArmed,
			"rollback_threshold", gaCfg.RollbackPolicyConfig.DegradationThreshold,
			"rollback_window", gaCfg.RollbackPolicyConfig.WindowSize,
			"rollback_min_samples", gaCfg.RollbackPolicyConfig.MinSamples,
		)
		// The absence must be meterable, not only logged once.
		if gaCfg.Metrics != nil {
			gaCfg.Metrics.RecordEvolutionGateSkipped("shadow", gateReason)
		}
	}
}

// wireLifecycleGates registers the G3 eval-suite gate and the arena regression gate on the lifecycle. An armed-but-broken gate fails bootstrap (fail-closed); an intentionally absent gate is an honest skip.

func wireLifecycleGates(ctx context.Context, cfg *ares_config.Config, comp *Components, wired *evolution.WiredEvolutionSystem, gaCfg evolution.SystemConfig) error {
	// Wire the G3 eval-suite gate so independently-built
	// evaluators participate in the promote/rollback decision instead of
	// sitting idle. The gate is built ONLY when a regression suite is
	// configured (evolution.gates.eval_suite file path); otherwise NO gate
	// is registered — honest absence, not a permanent pass-through pretending
	// to be verification.
	if wired.Lifecycle != nil && comp.Evolution != nil {
		// MinScore flows from the evolution.gates.eval_min_score YAML knob
		// via gaCfg.Lifecycle; 0 falls back to the gate's
		// own 0.7 default.
		var minScore float64
		if wiredLifecycleCfg := gaCfg.Lifecycle; wiredLifecycleCfg != nil {
			minScore = wiredLifecycleCfg.Gates.EvalMinScore
		}
		suitePath := ""
		strict := false
		if gaCfg.Lifecycle != nil {
			suitePath = cfg.Evolution.Gates.EvalSuite
			strict = cfg.Evolution.Gates.EvalStrict
		}
		evalGate, gerr := buildEvalGate(
			comp.Evolution.EvaluatorRegistry,
			comp.Evolution.EvalLLMClient,
			suitePath,
			minScore,
			strict,
		)
		if gerr != nil && !errors.Is(gerr, errEvalGateNotConfigured) {
			// An ARMED but broken/incomplete gate fails bootstrap (fail
			// closed); an intentionally absent gate just skips G3.
			return gerr
		}
		if evalGate != nil {
			evolution.WithLifecycleGates(evalGate)(wired.Lifecycle)
			log.InfoContext(ctx, "bootstrap: G3 eval gate wired",
				"suite", suitePath, "min_score", minScore, "strict_mode", evalGate.StrictModeEnabled(),
				"skipped_count", evalGate.SkippedCount())
		} else {
			log.WarnContext(ctx, "bootstrap: no eval_suite configured — promote gate chain degrades to G1+G2 (set evolution.gates.eval_suite to arm G3; evolution.gates.eval_strict makes absence fatal)")
		}

		// Arena regression gate (M4 wiring; AUTO-ARMED by default from M-G2): the
		// RELATIVE complement to the G3 absolute-score gate — candidate vs
		// active strategy A/B over the same preserved-case suite, rejecting
		// only a statistically significant drop. Defaults to armed whenever
		// the infrastructure (eval_suite + LLM client) exists;
		// regression_enabled=false is the documented opt-out (Warn below).
		regGate, rgerr := buildRegressionGate(
			cfg.Evolution.Gates.RegressionEnabled,
			comp.Evolution.EvalLLMClient,
			cfg.Evolution.Gates,
		)
		if rgerr != nil && !errors.Is(rgerr, errRegressionGateNotConfigured) {
			// An ARMED but broken gate fails bootstrap (fail closed); an
			// intentionally absent gate just skips the wiring.
			return rgerr
		}
		if regGate != nil {
			evolution.WithLifecycleGates(regGate)(wired.Lifecycle)
			mode := "auto"
			if cfg.Evolution.Gates.RegressionEnabled != nil {
				mode = "explicit"
			}
			log.InfoContext(ctx, "bootstrap: arena regression gate armed",
				"mode", mode,
				"suite", cfg.Evolution.Gates.EvalSuite,
				"runs", cfg.Evolution.Gates.RegressionRuns,
			)
		} else if cfg.Evolution.Gates.RegressionEnabled != nil && !*cfg.Evolution.Gates.RegressionEnabled {
			log.WarnContext(ctx, "bootstrap: arena regression gate DISABLED via evolution.gates.regression_enabled=false — promote gate chain runs without the preserved-case regression check",
				"suite_available", strings.TrimSpace(cfg.Evolution.Gates.EvalSuite) != "")
		}
	}

	return nil
}

// startLifecycleWatch wires the lifecycle's evidence store, starts its watch loop, and exposes the lifecycle + ASM + shadow evaluator for the introspect control plane.

func startLifecycleWatch(ctx context.Context, comp *Components, newEvol *NewEvolutionComponents, wired *evolution.WiredEvolutionSystem) {
	// Wire the lifecycle's evidence store and start its watch loop so
	// rollback detection runs against real runtime evidence.
	if wired.Lifecycle != nil {
		evolution.WithLifecycleEvidenceStore(newEvol.EvidenceStore)(wired.Lifecycle)
		wired.Lifecycle.Start(ctx)
		// The watch goroutine must not outlive
		// bootstrap — stop it (and wait) when the bootstrap context ends.
		comp.bgGroup.Go(func() error {
			<-ctx.Done()
			wired.Lifecycle.Stop()
			return nil
		})
		// Expose the lifecycle for the introspect control plane
		// so /api/evolution/lifecycle returns a state snapshot.
		newEvol.Lifecycle = wired.Lifecycle
		// Closure-assertion surfaces: the ASM (Previous/RollbackPolicy)
		// and the G2 shadow evaluator (the comparison feeder).
		newEvol.ActiveStrategyManager = wired.ActiveStrategyManager
		newEvol.ShadowEvaluator = wired.ShadowEvaluator
	}
}

// startEvolutionObserver wires the RuntimeObserver (the OBSERVE stage) that converts task events into normalized strategy samples and KindFitness evidence.

func startEvolutionObserver(ctx context.Context, comp *Components, newEvol *NewEvolutionComponents, wired *evolution.WiredEvolutionSystem) {
	// Wire the RuntimeObserver — the OBSERVE stage of the evolution
	// control plane. It converts task completed/failed events into
	// normalized [0,1] strategy samples and writes KindFitness evidence
	// (source "strategy"). Without it that source is empty, so the
	// rollback watch loop's Window() never reaches ok=true and the
	// staging score has no runtime fitness to read — the whole feedback
	// chain starves regardless of how well the decision side is wired.
	if comp.EventStore != nil && newEvol.EvidenceStore != nil {
		obsOpts := []evolution.ObserverOption{
			evolution.WithObserverEvidenceStore(newEvol.EvidenceStore),
		}
		if wired.ActiveStrategyManager != nil {
			obsOpts = append(obsOpts, evolution.WithObserverActiveIDFunc(activeStrategyIDFunc(wired.ActiveStrategyManager)))
		}
		observer := evolution.NewRuntimeObserver(comp.EventStore, obsOpts...)
		if err := observer.Start(ctx); err != nil {
			log.WarnContext(ctx, "bootstrap: runtime observer start failed", "error", err)
		} else {
			comp.bgGroup.Go(func() error {
				<-ctx.Done()
				observer.Stop()
				return nil
			})
		}
	}
}

// wireEvolutionScheduler attaches the GA adapter to the legacy scheduler when one exists, otherwise registers the wired scheduler (so Tick sees real scores) with a matching shutdown. It returns the legacy scheduler for the ticker to prefer.

func wireEvolutionScheduler(ctx context.Context, comp *Components, wired *evolution.WiredEvolutionSystem, popAdapter *evolution.GenomePopulationAdapter) *evolution.EvolutionScheduler {
	var legacySched *evolution.EvolutionScheduler
	if comp.Evolution != nil {
		// ok is used instead of relying on the nil-ness of the asserted
		// value: the assertion may legitimately miss (Scheduler is an
		// interface; other implementations exist), and the ok form makes
		// the "not the legacy scheduler" branch read as a deliberate
		// fallback rather than an accident of nil-ness.
		var isLegacy bool
		legacySched, isLegacy = comp.Evolution.Scheduler.(*evolution.EvolutionScheduler)
		if !isLegacy {
			legacySched = nil
		}
	}
	if legacySched != nil {
		legacySched.SetAdapter(popAdapter)
	} else if wired.Scheduler != nil && comp.EventStore != nil {
		wired.Scheduler.Register()
		// Register subscribes on its own context.Background() and parks a
		// goroutine on the event channel; without a matching Shutdown that
		// goroutine (and the EventStore subscriber feeding it) outlives the
		// bootstrap for the life of the process. goleak found this; a
		// goroutine count could not have.
		wiredSched := wired.Scheduler
		comp.bgGroup.Go(func() error {
			<-ctx.Done()
			wiredSched.Shutdown()
			return nil
		})
	}

	return legacySched
}

// runEvolutionTicker starts the background ticker that triggers evolution via the unified scheduler.Tick path (gated by shouldEvolve + guardrails + MinInterval).

func runEvolutionTicker(ctx context.Context, cfg *ares_config.Config, comp *Components, wired *evolution.WiredEvolutionSystem, legacySched *evolution.EvolutionScheduler, popAdapter *evolution.GenomePopulationAdapter) {
	comp.bgGroup.Go(func() error {
		// Honor evolution.min_interval from yaml (the 5-minute
		// ticker was hardcoded, leaving MinInterval dead config).
		tick := 5 * time.Minute
		if raw := cfg.Evolution.MinInterval; raw != "" {
			if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
				tick = d
			}
		}
		evoTicker := time.NewTicker(tick)
		defer evoTicker.Stop()
		for {
			select {
			case <-evoTicker.C:
				// Route through a scheduler Tick that actually sees
				// scores, so shouldEvolve + guardrails + MinInterval are
				// always applied. legacySched is preferred (it is Registered
				// and therefore receives score events); the wired scheduler
				// is Registered above when it is the only one. When neither
				// exists (no EventStore), keep the old unconditional Run so
				// minimal configs still evolve.
				switch {
				case legacySched != nil:
					legacySched.Tick(ctx)
				case wired.Scheduler != nil:
					wired.Scheduler.Tick(ctx)
				default:
					if err := popAdapter.Run(ctx); err != nil {
						log.WarnContext(ctx, "[bootstrap] ticker-triggered evolution failed",
							"error", err)
						continue
					}
				}
				// Record the generation trajectory into the shared tracer
				// so /evolution/trajectory returns live data
				// instead of an empty list. wired.Population exposes the
				// per-generation Stats after a run.
				if comp.Observability != nil && comp.Observability.EvolutionTracer != nil && wired.Population != nil {
					stats := wired.Population.Stats()
					comp.Observability.EvolutionTracer.Record(stats.Generation, stats.BestScore, nil, nil)
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

// runLLMSuggestions starts the LLM suggestion pipeline: periodically generate + parse evolution suggestions and submit them to the Coordinator.

func runLLMSuggestions(ctx context.Context, comp *Components, newEvol *NewEvolutionComponents) {
	// Wire the LLMAdapter into the Coordinator's suggestion pipeline.
	// When an LLM client is available, periodically generate and submit
	// evolution suggestions (LLM → Parse → PatchProposal → Coordinator.Evaluate).
	if newEvol.LLMAdapter != nil && comp.LLM != nil && comp.LLM.Client != nil {
		if llmClient, ok := comp.LLM.Client.(evoService.LLMClient); ok {
			comp.bgGroup.Go(func() error {
				suggestTicker := time.NewTicker(15 * time.Minute)
				defer suggestTicker.Stop()
				for {
					select {
					case <-suggestTicker.C:
						// Generate a suggestion prompt for the LLM based on
						// current evolution state and recent evidence.
						prompt := buildEvolutionSuggestionPrompt(ctx,
							newEvol.EvidenceStore, newEvol.StrategyStore)
						resp, err := llmClient.Generate(ctx, prompt)
						if err != nil {
							log.WarnContext(ctx, "[bootstrap] LLM suggestion generation failed",
								"error", err)
							continue
						}
						results, parseErr := newEvol.LLMAdapter.Parse(ctx, resp)
						if parseErr != nil {
							// Parsing failures are expected when the LLM response
							// doesn't match any known pattern — log and skip.
							log.DebugContext(ctx, "[bootstrap] LLM suggestion parse skipped",
								"error", parseErr)
							continue
						}
						for _, r := range results {
							newEvol.Coordinator.Submit(r.Proposal)
						}
						newEvol.Coordinator.Evaluate(ctx)
					case <-ctx.Done():
						return nil
					}
				}
			})
			log.InfoContext(ctx, "[bootstrap] LLM suggestion pipeline wired into Coordinator")
		}
	}
}

// wireGAEvolution assembles the GA evolution plane from the YAML evolution
// block: strategy store, SystemConfig, scorer/shadow-gate posture, lifecycle
// gates, runtime observer, channel feedback, scheduler, and the background
// ticker + LLM suggestion loops. Each phase is delegated to a helper above so
// this orchestration stays readable.
func wireGAEvolution(ctx context.Context, cfg *ares_config.Config, comp *Components, newEvol *NewEvolutionComponents, guidanceProvider evolution.GuidanceProvider, cleanups *[]func()) error {
	memStore := buildStrategyStore(ctx, cfg, cleanups)
	newEvol.StrategyStore = memStore

	// Close the "evolution context in the knowledge graph" loop: stream
	// active/historical strategies as decision-type knowledge objects so
	// server-side evolution queries can retrieve strategy decisions. The
	// StrategyStore only exists from this point on (the knowledge runtime is
	// built earlier, in BuildKnowledgeRuntime), hence the late registration.
	attachEvolutionKnowledgeProvider(ctx, comp.KnowledgeRuntime, memStore, comp.EvidenceStore)

	base := &mutation.Strategy{
		ID:     "bootstrap-root",
		Params: map[string]any{paramTemperature: 0.7, paramMaxTokens: 4096},
	}
	gaCfg := assembleGAConfig(ctx, cfg, comp, memStore, guidanceProvider)
	rollbackArmed := cfg.Evolution.Rollback.IsEnabled()
	wireScorerAndShadowGate(ctx, cfg, comp, &gaCfg, rollbackArmed)

	wired, wErr := evolution.NewWiredEvolutionSystem(base, gaCfg)
	if wErr != nil {
		return fmt.Errorf("wire GA population adapter: %w", wErr)
	}

	// Live-chaos GA quiet-window probe: expose whether a generation is
	// mid-flight so serve's chaos loop can defer injections.
	newEvol.GAGenerationActive = wired.GenerationActive

	// Attach the coordinator bridge to the population adapter.
	popAdapter := wired.PopAdapter
	evolution.WithAdapterCoordinator(
		newEvol.Coordinator,
		newEvol.DiffReg,
		newEvol.GenomeReg,
	)(popAdapter)

	if err := wireLifecycleGates(ctx, cfg, comp, wired, gaCfg); err != nil {
		return err
	}
	startLifecycleWatch(ctx, comp, newEvol, wired)
	startEvolutionObserver(ctx, comp, newEvol, wired)

	// The OBSERVE stage for the other two perception channels.
	newEvol.ChannelFeedback = startChannelFeedback(ctx, comp, newEvol, wired.ActiveStrategyManager, cfg.Evolution.ChannelFeedback)

	legacySched := wireEvolutionScheduler(ctx, comp, wired, popAdapter)
	runEvolutionTicker(ctx, cfg, comp, wired, legacySched, popAdapter)
	runLLMSuggestions(ctx, comp, newEvol)
	return nil
}

// wireLLMScorer constructs the opt-in LLM-backed scorer for the GA evolution
// system. It returns non-nil scorer functions
// only when all of the following hold:
//   - cfg.Evolution.LLMScoring.Enabled is true,
//   - comp.LLM and comp.LLM.Client are non-nil,
//   - comp.LLM.Client satisfies the evoService.LLMClient interface,
//   - evoService.NewLLMScorer succeeds.
//
// On any failure (disabled, missing client, type mismatch, construction
// error), the function logs a warning and returns nil scorers with a zero
// budget. The caller then leaves gaCfg.Scorer unset, causing
// buildAdapterOptions to fall back to ConstantScorer(50.0). This keeps
// scoring best-effort: bootstrap never fails due to scorer wiring.
// applyGATuning copies operator tuning from the YAML evolution section onto the
// GA engine config, so operators can tune the GA without touching code
// (previously these fields were dead config).
//
// A field overrides the engine default only when it differs from the
// config-layer default (ares_config.DefaultEvolution*): cfg arrives from
// LoadConfig with setDefaults already applied, so every field is non-zero and a
// plain `> 0` guard would always fire, silently replacing the GA engine's own
// tuned defaults from DefaultSystemConfig (e.g. EliteCount 3 with the
// config-layer default 2). The `> 0` half of each guard keeps programmatic
// configs that skip setDefaults from clobbering the engine with zero values.
//
// Known tradeoff: an operator who explicitly sets a field equal to the
// config-layer default is indistinguishable from an unset field and keeps the
// GA engine default (locked by TestApplyGATuningExplicitDefaultValues).
//
// Args:
//
//	gaCfg - the GA engine config to mutate; starts from DefaultSystemConfig.
//	ec    - the YAML evolution section of the loaded config.
func applyGATuning(gaCfg *evolution.SystemConfig, ec *ares_config.EvolutionConfig) {
	if ec.PopulationSize > 0 && ec.PopulationSize != ares_config.DefaultEvolutionPopulationSize {
		gaCfg.PopulationSize = ec.PopulationSize
	}
	if ec.EliteCount > 0 && ec.EliteCount != ares_config.DefaultEvolutionEliteCount {
		gaCfg.EliteCount = ec.EliteCount
	}
	if ec.SurvivalRate > 0 && ec.SurvivalRate != ares_config.DefaultEvolutionSurvivalRate {
		gaCfg.SurvivalRate = ec.SurvivalRate
	}
	if ec.MutationRate > 0 && ec.MutationRate != ares_config.DefaultEvolutionMutationRate {
		gaCfg.MutationRate = ec.MutationRate
	}
	if ec.MinMutationRate > 0 && ec.MinMutationRate != ares_config.DefaultEvolutionMinMutationRate {
		gaCfg.MinMutationRate = ec.MinMutationRate
	}
	if ec.MaxMutationRate > 0 && ec.MaxMutationRate != ares_config.DefaultEvolutionMaxMutationRate {
		gaCfg.MaxMutationRate = ec.MaxMutationRate
	}
	if ec.BreedingPoolRatio > 0 && ec.BreedingPoolRatio != ares_config.DefaultEvolutionBreedingPoolRatio {
		gaCfg.BreedingPoolRatio = ec.BreedingPoolRatio
	}
	if ec.SelectionStrategy != "" && ec.SelectionStrategy != ares_config.DefaultEvolutionSelectionStrategy {
		gaCfg.SelectionStrategy = ec.SelectionStrategy
	}
	// ToolPool: wire the deployment-configured tool-whitelist pool into the
	// mutator (single source for tool mutation vocabulary). Empty keeps tool
	// mutation disabled (guided mutation may still produce choices from hints).
	if len(ec.ToolPool) > 0 {
		gaCfg.ToolPool = ec.ToolPool
	}
}

// targetFitnessScale converts the YAML evolution.target_fitness (documented as
// a 0-100 scale in ares_config) to the [0,1] scale EvolutionGuardrails compares
// against, since its BaselineScore is measured against the same values fed to
// PostEvolveCheckForSource.
const targetFitnessScale = 100.0

// findUnknownPoolTools cross-checks the deployment-configured mutation pool
// (evolution.tool_pool, each entry a comma-separated whitelist) against the
// registered-tool vocabulary (evolution.guardrails.known_tools). It returns
// the offending entries mapped to their unknown names, or nil when everything
// resolves (or when either side is empty — no vocabulary means no judgment,
// same opt-in rule as the unknown-name guard itself).
//
// Parsing goes through agents.ToolNamesFromParams — the same single parser the
// executors and the guardrail use — so "a,a," counts as one name here exactly
// as it does at selection and execution time.
func findUnknownPoolTools(pool, known []string) map[string][]string {
	if len(pool) == 0 || len(known) == 0 {
		return nil
	}
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		if k = strings.TrimSpace(k); k != "" {
			knownSet[k] = true
		}
	}
	if len(knownSet) == 0 {
		return nil
	}
	var bad map[string][]string
	for _, entry := range pool {
		names := agents.ToolNamesFromParams(map[string]any{agents.ParamKeyTools: entry})
		var unknown []string
		for _, name := range names {
			if !knownSet[name] {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			if bad == nil {
				bad = make(map[string][]string)
			}
			bad[entry] = unknown
		}
	}
	return bad
}

// buildEvolutionGuardrails constructs the G1 population-level guardrails from
// the YAML evolution section.
//
// Previously this construction did not exist: gaCfg.Guardrails was never
// assigned, so GenomePopulationAdapter.runPreGuardrails / runPostGuardrails and
// EvolutionScheduler.checkGuardrails all short-circuited on nil and passed
// unconditionally. G1 was structurally present and operationally absent.
//
// Two YAML knobs that were previously dead config now drive it:
//   - Generations → MaxStagnantGenerations. Semantics line up: both bound "how
//     many generations may pass without progress".
//   - TargetFitness (0-100) → BaselineScore, rescaled to [0,1]. Left unset when
//     zero so the guardrail keeps its adaptive behavior (baseline = the best
//     score seen so far for that source).
//
// MaxLineageShare keeps the constructor default (0.8); no YAML key exists for
// it and inventing one would create fresh dead config.
//
// IMPORTANT — each caller must own its OWN instance, never share one.
// EvolutionGuardrails carries mutable state (stagnantCount, bestBySource) and
// the two driving paths (legacy scheduler ticker vs adapter population layer)
// run on different generation counters and score scales. Sharing an instance
// would let one path's stagnation count and baseline pollute the other's. The
// bestBySource source-keying inside guardrails.go exists for the same reason.
//
// Args:
//   - ctx: for the degradation log only.
//   - ec: the YAML evolution section (nil returns nil).
//   - metrics: optional Prometheus sink; when non-nil every guardrail event
//     increments ARES_evolution_guardrail_total{code}.
//
// Returns:
//   - *evolution.EvolutionGuardrails: nil on construction failure, which
//     degrades to the previous behavior (all checks pass) rather than blocking
//     bootstrap.
func buildEvolutionGuardrails(
	ctx context.Context,
	ec *ares_config.EvolutionConfig,
	metrics *observability.PrometheusMetrics,
) *evolution.EvolutionGuardrails {
	if ec == nil {
		return nil
	}
	opts := []evolution.GuardrailOption{
		evolution.WithMaxStagnantGenerations(
			defaultInt(ec.Generations, ares_config.DefaultEvolutionGenerations),
		),
	}
	if ec.TargetFitness > 0 {
		opts = append(opts, evolution.WithBaselineScore(ec.TargetFitness/targetFitnessScale))
	}
	// Tool-set selection guardrails from the evolution.guardrails YAML block.
	// All three are opt-in — zero-value disables, preserving prior behavior.
	if gr := ec.Guardrails; gr.MaxToolsEnabled > 0 {
		opts = append(opts, evolution.WithMaxToolsEnabled(gr.MaxToolsEnabled))
	}
	if ec.Guardrails.RequireAnyTool {
		opts = append(opts, evolution.WithRequireAnyTool(true))
	}
	if len(ec.Guardrails.KnownTools) > 0 {
		opts = append(opts, evolution.WithKnownTools(ec.Guardrails.KnownTools))
	}
	// Loud misconfiguration check — a tool_pool entry naming tools
	// outside the known vocabulary silently jails every candidate it produces
	// (unknown-name guard), so the generation burns with no promotion and
	// evolution looks stalled rather than misconfigured. Warn, don't
	// fail-closed: the pool only feeds the elite/random mutation path
	// (guided mutation still works), and blocking bootstrap on a soft
	// evolution knob would violate graceful degradation.
	for entry, unknown := range findUnknownPoolTools(ec.ToolPool, ec.Guardrails.KnownTools) {
		log.WarnContext(ctx, "bootstrap: evolution tool_pool entry names unregistered tools; candidates from this entry will be jailed by the tool-set guardrail",
			"entry", entry, "unknown", unknown, "known_count", len(ec.Guardrails.KnownTools))
	}
	if metrics != nil {
		opts = append(opts, evolution.WithGuardrailEventHandler(func(evt evolution.GuardrailEvent) {
			metrics.RecordEvolutionGuardrail(string(evt.ErrorCode))
		}))
	}
	g, err := evolution.NewEvolutionGuardrails(opts...)
	if err != nil {
		// Construction failure is fail-closed, not silent pass-through.
		// Previously this returned nil, which caused checkGuardrails to
		// short-circuit to true (all checks pass) — a guardrail that does
		// not exist cannot block anything. Now we return a guardrail that
		// always blocks (ShouldStop=true) so a broken G1 cannot silently
		// let bad candidates through.
		log.ErrorContext(ctx, "bootstrap: evolution guardrails construction failed, using fail-closed guardrail",
			"error", err)
		return evolution.NewFailClosedGuardrails()
	}
	return g
}

func wireLLMScorer(cfg *ares_config.Config, comp *Components) (genome.ScorerFunc, genome.ScorerFunc, int) {
	if cfg == nil || !cfg.Evolution.LLMScoring.Enabled {
		return nil, nil, 0
	}

	if comp == nil || comp.LLM == nil || comp.LLM.Client == nil {
		log.Warn("bootstrap: LLM scoring enabled but LLM client is nil, falling back to baseline scorer")
		return nil, nil, 0
	}

	llmClient, ok := comp.LLM.Client.(evoService.LLMClient)
	if !ok {
		log.Warn("bootstrap: LLM client does not satisfy LLMClient interface, falling back to baseline scorer",
			"client_type", fmt.Sprintf("%T", comp.LLM.Client))
		return nil, nil, 0
	}

	llmScorer, err := evoService.NewLLMScorer(evoService.LLMScorerConfig{
		Client:   llmClient,
		Seed:     cfg.Evolution.LLMScoring.Seed,
		Fallback: evoService.DeterministicScore,
	})
	if err != nil {
		log.Warn("bootstrap: failed to create LLM scorer, falling back to baseline scorer", "error", err)
		return nil, nil, 0
	}

	llmScorerFn := llmScorer.AsScorerFunc()
	scorer := genome.ScorerFunc(func(agent *mutation.Strategy) float64 {
		return llmScorerFn(evoService.ToAPIStrategy(agent))
	})
	heuristic := genome.ScorerFunc(func(agent *mutation.Strategy) float64 {
		return evoService.DeterministicScore(evoService.ToAPIStrategy(agent))
	})

	log.Info("bootstrap: LLM-backed scorer wired into GA evolution",
		"seed", cfg.Evolution.LLMScoring.Seed,
		"max_calls_per_generation", cfg.Evolution.LLMScoring.MaxCallsPerGeneration)

	return scorer, heuristic, cfg.Evolution.LLMScoring.MaxCallsPerGeneration
}

// attachEvolutionKnowledgeProvider registers the evolution StrategyStore as a
// knowledge graph provider on rt (best-effort: nil rt or a registration
// failure degrades to a warn log, never blocks bootstrap). Kept as its own
// function so the closure contract is directly testable without the full
// wireGAEvolution fixture.
//
// The evidence store (comp.EvidenceStore — wired long before this point,
// so the call-site timing is unaffected) lets the provider also stream the
// promote/rollback decision trail, closing the "wrote evidence nobody
// consumed" gap. A nil evStore degrades to lineage-only output.
func attachEvolutionKnowledgeProvider(
	ctx context.Context,
	rt *knowledgeruntime.KnowledgeRuntime,
	store evolution.StrategyStore,
	evStore evidence.Store,
) {
	if rt == nil || store == nil {
		return
	}
	evoProv := evoprovider.New("evolution", store)
	if evStore != nil {
		evoProv.WithEvidenceStore(evStore)
	}
	if err := rt.RegisterProvider(evoProv); err != nil {
		log.WarnContext(ctx, "bootstrap: register evolution provider for knowledge runtime", "error", err)
		return
	}
	log.InfoContext(ctx, "bootstrap: evolution provider wired for knowledge runtime",
		"decision_trail", evStore != nil)
}

// newPGStrategyStore creates a PostgreSQL-backed strategy store from config.
// Returns nil when the database connection cannot be established, so callers
// can fall back to the in-memory store gracefully. The returned close func
// releases the dedicated *sql.DB and MUST be registered by the caller in its
// cleanups (it is a no-op only on the error paths, where the db is already
// closed here).
func newPGStrategyStore(cfg *ares_config.Config) (evolution.StrategyStore, func(), error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Storage.Host, cfg.Storage.Port, cfg.Storage.Username,
		cfg.Storage.Password, cfg.Storage.Database, cfg.Storage.SSLMode)
	db, err := sql.Open(storageTypePostgres, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("pg strategy store: open db: %w", err)
	}
	// Verify the connection is alive.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Warn("pg strategy store: close db after ping failure", "error", closeErr)
		}
		return nil, nil, fmt.Errorf("pg strategy store: ping: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	store, err := evolution.NewPGStrategyStore(db, "evolution_strategies", 100)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Warn("pg strategy store: close db after init failure", "error", closeErr)
		}
		return nil, nil, fmt.Errorf("pg strategy store: init: %w", err)
	}
	return store, func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Warn("pg strategy store: close db", "error", closeErr)
		}
	}, nil
}
