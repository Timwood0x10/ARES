// Package ares_bootstrap — Evolution provider.
package ares_bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// EvolutionComponents holds evolution-related components.
type EvolutionComponents struct {
	Adapter           interface{}
	Scheduler         interface{}
	FeedbackService   *experience.FeedbackService
	EvaluatorRegistry *ares_eval.EvaluatorRegistry
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
	callbackReg *ares_callbacks.Registry,
	llmClient ares_eval.LLMClient,
	fr *flight.FlightRecorder,
) (*EvolutionComponents, error) {
	if eventStore == nil || expRepo == nil || callbackReg == nil {
		return nil, fmt.Errorf("bootstrap: evolution skipped (missing dependencies)")
	}

	// 1. Flight → Experience adapter (reuses the shared recorder — do NOT
	// construct a second one here, that would double-emit fitness evidence).
	flightWrapper := &flightRecorderWrapper{recorder: fr}
	expAdapter := &expRepoAdapter{inner: expRepo}
	adapter := evolution.NewFlightToExperienceAdapter(flightWrapper, expAdapter)

	// 2. Scheduler
	// The legacy scheduler must be gated by cfg.Evolution.Enabled (F02): when
	// evolution is disabled, the scheduler must not force itself on. Callers
	// that gate on Enabled (wireLegacyEvolution) pass true here; direct callers
	// get the config-honest value instead of a hardcoded true.
	var err error
	// Enabled only when the config explicitly turns evolution on (F02); a nil
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
	scheduler := evolution.NewEvolutionScheduler(callbackReg, adapter, opts...)
	scheduler.Register()

	// 3. Evaluators (optional — requires LLM client).
	var evalRegistry *ares_eval.EvaluatorRegistry
	if llmClient != nil {
		evalRegistry, err = setupEvaluators(llmClient)
		if err != nil {
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
		FlightRecorder:    fr,
	}, nil
}

func setupEvaluators(llmClient ares_eval.LLMClient) (*ares_eval.EvaluatorRegistry, error) {
	judge, err := ares_eval.NewLLMJudgeEvaluator(llmClient,
		ares_eval.WithChinesePrompt(),
		ares_eval.WithScale(ares_eval.ScaleOneToTen),
	)
	if err != nil {
		return nil, fmt.Errorf("create llm judge: %w", err)
	}
	registry := ares_eval.NewEvaluatorRegistry()
	if err := registry.Register("llm_judge", judge); err != nil {
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
