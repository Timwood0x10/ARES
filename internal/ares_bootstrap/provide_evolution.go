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
	DreamCycle        interface{}
	FeedbackService   *experience.FeedbackService
	EvaluatorRegistry *ares_eval.EvaluatorRegistry
}

// ProvideEvolution wires the full evolution system: adapter, scheduler, dream cycle,
// feedback service, and evaluators.
func ProvideEvolution(
	ctx context.Context,
	cfg *ares_config.EvolutionConfig,
	eventStore ares_events.EventStore,
	expRepo repositories.ExperienceRepositoryInterface,
	callbackReg *ares_callbacks.Registry,
	llmClient ares_eval.LLMClient,
) (*EvolutionComponents, error) {
	if eventStore == nil || expRepo == nil || callbackReg == nil {
		return nil, fmt.Errorf("bootstrap: evolution skipped (missing dependencies)")
	}

	// Create flight recorder from event store
	flightRecorder := flight.NewFlightRecorder(flight.FlightRecorderConfig{
		EventStore: eventStore,
	})

	// 1. Flight → Experience adapter
	flightWrapper := &flightRecorderWrapper{recorder: flightRecorder}
	expAdapter := &expRepoAdapter{inner: expRepo}
	adapter := evolution.NewFlightToExperienceAdapter(flightWrapper, expAdapter)

	// 2. Scheduler
	opts := []evolution.SchedulerOption{evolution.WithEnabled(true)}
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

	// 3. Dream cycle (optional — requires mutator + tester wired externally)
	var dreamCycle *evolution.DreamCycle

	// 4. Evaluators
	evalRegistry, err := setupEvaluators(llmClient)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: setup evaluators: %w", err)
	}

	// 5. Feedback service (best-effort)
	feedbackSvc := setupFeedbackService(expRepo)

	return &EvolutionComponents{
		Adapter:           adapter,
		Scheduler:         scheduler,
		DreamCycle:        dreamCycle,
		FeedbackService:   feedbackSvc,
		EvaluatorRegistry: evalRegistry,
	}, nil
}

func setupEvaluators(llmClient ares_eval.LLMClient) (*ares_eval.EvaluatorRegistry, error) {
	if llmClient == nil {
		return nil, nil
	}
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
