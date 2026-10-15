// Package ares_bootstrap — Evolution provider.
package ares_bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
	experience "github.com/Timwood0x10/ares/internal/runtime/memory/experience"
	flight "github.com/Timwood0x10/ares/internal/runtime/observability/flight"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// EvolutionComponents holds evolution-related components.
type EvolutionComponents struct {
	Adapter           interface{}
	Scheduler         interface{}
	FeedbackService   *experience.FeedbackService
	EvaluatorRegistry *eval.EvaluatorRegistry
	// EvalLLMClient is the LLM client the evaluators were built with. The
	// G3 eval gate needs it to score candidate strategies through the
	// AgentTestRunner at promote time. Nil when no LLM client was available.
	EvalLLMClient eval.LLMClient
	// FlightRecorder is the recorder created for the Flight→Experience
	// adapter. It is exposed so Bootstrap can start/stop it explicitly:
	// without Start the collector never subscribes to events and the GA
	// workflow/scheduler/recovery fitness evidence is never emitted.
	FlightRecorder *flight.FlightRecorder
}

// ProvideEvolution wires the full evolution system: adapter, scheduler, dream cycle,
// feedback service, and evaluators.
//
// fr is the shared flight recorder built and started by Bootstrap
// (comp.FlightRecorder). It is reused here — not constructed — so there is
// exactly one recorder per process: its collector subscribes to the event
// store and emits workflow/scheduler/recovery fitness evidence into the
// shared evidence store (the same store the GA genomes read). May be nil
// when Bootstrap could not build one (no event store); the Flight→Experience
// adapter then degrades gracefully.
func ProvideEvolution(
	ctx context.Context,
	cfg *ares_config.EvolutionConfig,
	eventStore ares_events.EventStore,
	expRepo repositories.ExperienceRepositoryInterface,
	llmClient eval.LLMClient,
	fr *flight.FlightRecorder,
	options ...EvolutionOption,
) (*EvolutionComponents, error) {
	if eventStore == nil || expRepo == nil {
		return nil, errors.New("bootstrap: evolution skipped (missing dependencies)")
	}
	var o evolutionOptions
	for _, opt := range options {
		if opt != nil {
			opt(&o)
		}
	}

	// 1. Flight → Experience adapter (reuses the shared recorder — do NOT
	// construct a second one here, that would double-emit fitness evidence).
	flightWrapper := &flightRecorderWrapper{recorder: fr}
	expAdapter := &expRepoAdapter{inner: expRepo}
	adapter := evolution.NewFlightToExperienceAdapter(flightWrapper, expAdapter)

	// 2. Scheduler
	// The legacy scheduler must be gated by cfg.Evolution.Enabled: when
	// evolution is disabled, the scheduler must not force itself on. Callers
	// that gate on Enabled (wireLegacyEvolution) pass true here; direct callers
	// get the config-honest value instead of a hardcoded true.
	var err error
	// Enabled only when the config explicitly turns evolution on; a nil
	// config keeps the legacy default (enabled) for direct callers.
	schedulerEnabled := cfg == nil || cfg.Enabled
	opts := []evolution.SchedulerOption{evolution.WithEnabled(schedulerEnabled)}
	if cfg != nil && cfg.MinInterval != "" {
		if d, err := time.ParseDuration(cfg.MinInterval); err == nil {
			opts = append(opts, evolution.WithMinInterval(d))
		} else {
			opts = append(opts, evolution.WithMinInterval(5*time.Minute))
		}
	} else {
		opts = append(opts, evolution.WithMinInterval(5*time.Minute))
	}
	// The legacy scheduler previously got no guardrails at all, so
	// EvolutionScheduler.checkGuardrails short-circuited on nil and every
	// ticker-driven cycle ran unchecked. This instance is deliberately
	// SEPARATE from the adapter-layer one built in wireGAEvolution: guardrails
	// carry mutable stagnation/baseline state, and the two paths count
	// generations on different clocks and score scales — sharing one would
	// cross-contaminate both. Do not "simplify" this into a single instance.
	//
	// metrics is nil here: the Prometheus collector is owned by wireGAEvolution
	// (idempotent registration), and the legacy path's guardrail events are
	// already visible through its own logs. Passing a second collector would
	// double-count nothing but adds a construction-order dependency.
	if g := buildEvolutionGuardrails(ctx, cfg, nil); g != nil {
		opts = append(opts, evolution.WithSchedulerGuardrails(g))
	}
	scheduler := evolution.NewEvolutionScheduler(eventStore, adapter, opts...)
	scheduler.Register()
	// 3. Evaluators (optional — requires LLM client).
	var evalRegistry *eval.EvaluatorRegistry
	if llmClient != nil {
		evalRegistry, err = setupEvaluators(llmClient, o.evalEvidenceStore)
		if err != nil {
			// Register already subscribed to the EventStore on its own
			// background goroutine; without Shutdown that goroutine leaks
			// for the process lifetime.
			scheduler.Shutdown()
			return nil, fmt.Errorf("bootstrap: setup evaluators: %w", err)
		}
	}

	// 4. Feedback service (best-effort)
	feedbackSvc := setupFeedbackService(expRepo)

	return &EvolutionComponents{
		Adapter:           adapter,
		Scheduler:         scheduler,
		FeedbackService:   feedbackSvc,
		EvaluatorRegistry: evalRegistry,
		EvalLLMClient:     llmClient,
		FlightRecorder:    fr,
	}, nil
}

// EvolutionOption configures ProvideEvolution. Variadic so an added optional
// dependency does not breach the parameter-count rule and does not churn every
// call site.
type EvolutionOption func(*evolutionOptions)

// evolutionOptions holds the resolved options of one ProvideEvolution call.
type evolutionOptions struct {
	// evalEvidenceStore is the shared evidence store handed to the eval
	// evaluators. Wired, golden-replay judgments are persisted as dimension_eval
	// evidence — the only correctness signal the runtime path can produce for GA
	// fitness (defect G1 / P0-1). Left nil, evaluation behaves exactly as before
	// and nothing is persisted.
	evalEvidenceStore evidence.Store
}

// WithEvalEvidenceStore wires store into the evaluators built by
// ProvideEvolution, so that judging the golden suite leaves behind the accuracy
// evidence the fitness aggregator reads.
func WithEvalEvidenceStore(store evidence.Store) EvolutionOption {
	return func(o *evolutionOptions) { o.evalEvidenceStore = store }
}

// setupEvaluators builds the production evaluator registry.
//
// The judge runs the DIMENSION path: its four-rubric judgment is the only
// producer of dimension_eval evidence (dimension_judge.go), so leaving it off —
// which is what this wiring did — is why the fitness aggregator's DimensionEval
// weight (0.25) had nothing to weigh and GA optimised on outcome + cost/latency
// alone.
//
// Recorded, deliberate consequence: the eval gate's score becomes the mean of
// four rubric scores instead of one overall judgment. Both normalize to [0,1] so
// min_score keeps its scale, but the quantity measured changes — an operator
// comparing historical gate verdicts should know that.
func setupEvaluators(llmClient eval.LLMClient, evStore evidence.Store) (*eval.EvaluatorRegistry, error) {
	judgeOpts := []eval.LLMJudgeOption{
		eval.WithChinesePrompt(),
		eval.WithScale(eval.ScaleOneToTen),
		eval.WithDimensionAveraging(),
	}
	if evStore != nil {
		judgeOpts = append(judgeOpts, eval.WithEvidenceStore(evStore))
	}
	judge, err := eval.NewLLMJudgeEvaluator(llmClient, judgeOpts...)
	if err != nil {
		return nil, fmt.Errorf("create llm judge: %w", err)
	}
	registry := eval.NewEvaluatorRegistry()
	// Registered through the reporting decorator: the judge's own counters are the
	// only measurement of how much of the accuracy signal deterministic checks
	// anchor, and until something reads them at runtime the number is invisible
	// while the scalar it qualifies keeps being produced (P0-1(ii)).
	if err := registry.Register("llm_judge", eval.NewAgreementReportingEvaluator(judge)); err != nil {
		return nil, fmt.Errorf("register llm judge: %w", err)
	}
	return registry, nil
}

func setupFeedbackService(expRepo repositories.ExperienceRepositoryInterface) *experience.FeedbackService {
	if expRepo == nil {
		return nil
	}
	return experience.NewFeedbackService(expRepo)
}
