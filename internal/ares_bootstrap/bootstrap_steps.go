package ares_bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	evoService "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	_ "github.com/lib/pq"
)

// wireDistillation conditionally wires experience distillation (Track A) and
// returns a GuidanceProvider consumed by the GA, or nil when distillation is
// not configured/wired. Failures are non-fatal: they are logged and skipped,
// leaving the system running without distillation (graceful degradation).
func wireDistillation(ctx context.Context, cfg *ares_config.Config, comp *Components, deps *BootstrapDeps, cleanups *[]func()) evolution.GuidanceProvider {
	var guidanceProvider evolution.GuidanceProvider
	if cfg.Storage.Enabled && cfg.Storage.Type == "postgres" && cfg.Embedding.Enabled {
		pool, _, expRepo, distSvc, guidProv, wireErr := provideDistillation(ctx, cfg, comp.LLM.Client)
		if wireErr != nil {
			log.Warn("bootstrap: experience distillation not wired", "error", wireErr)
		} else {
			guidanceProvider = guidProv
			comp.Distillation = distSvc
			// Feed the experience repo into the old evolution system if present.
			if deps.ExpRepo == nil {
				deps.ExpRepo = expRepo
			}
			// The postgres pool must be closed if bootstrap fails later.
			*cleanups = append(*cleanups, func() { _ = pool.Close() })
			log.Info("bootstrap: experience distillation wired",
				"embedding_model", cfg.Embedding.Model)
		}
	}
	return guidanceProvider
}

// subscribeDistillationEvents starts the background distillation loop that
// turns task-completed/failed events into experiences. It is a no-op when
// distillation or the event store is unavailable.
func subscribeDistillationEvents(ctx context.Context, comp *Components) {
	if comp.Distillation == nil || comp.EventStore == nil {
		return
	}
	comp.wg.Add(1)
	go func() {
		defer comp.wg.Done()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		ch, err := comp.EventStore.Subscribe(ctx, ares_events.EventFilter{
			Types: []ares_events.EventType{
				ares_events.EventTaskCompleted,
				ares_events.EventTaskFailed,
			},
		})
		if err != nil {
			log.Warn("bootstrap: distillation event subscription failed", "error", err)
			return
		}
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				handleTaskCompletedForDistillation(ctx, comp.Distillation, ev)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Parameter keys used in evolution strategy configurations.
const (
	paramTemperature = "temperature"
	paramMaxTokens   = "max_tokens"
)

// wireGAEvolution wires the GA population adapter (step 9 of Bootstrap): it
// builds the GA system, attaches the coordinator bridge to the population
// adapter, and starts the background evolution ticker. Extracted from Bootstrap
// to keep its cyclomatic complexity within lint limits.
func wireGAEvolution(ctx context.Context, cfg *ares_config.Config, comp *Components, newEvol *NewEvolutionComponents, guidanceProvider evolution.GuidanceProvider) error {
	// Create a persistent strategy store when PostgreSQL is configured,
	// falling back to the in-memory store when no database is available.
	// The PG store ensures evolution results survive process restarts.
	var memStore evolution.StrategyStore
	if cfg.Storage.Enabled && cfg.Storage.Type == "postgres" && cfg.Storage.Host != "" {
		pgStore, err := newPGStrategyStore(cfg)
		if err != nil {
			log.WarnContext(ctx, "bootstrap: PG strategy store init failed, falling back to in-memory", "error", err)
			memStore = evolution.NewMemoryStrategyStore(0)
		} else {
			memStore = pgStore
			log.InfoContext(ctx, "bootstrap: PG strategy store wired (persistent)")
		}
	} else {
		memStore = evolution.NewMemoryStrategyStore(0)
	}
	newEvol.StrategyStore = memStore

	base := &mutation.Strategy{
		ID:     "bootstrap-root",
		Params: map[string]any{paramTemperature: 0.7, paramMaxTokens: 4096},
	}
	gaCfg := evolution.DefaultSystemConfig()
	gaCfg.EnableDreamCycle = false
	gaCfg.EnableScheduler = comp.Evolution == nil
	gaCfg.Callbacks = comp.LLM.CallbackReg
	gaCfg.StrategyStore = memStore
	gaCfg.RollbackPolicyConfig = evolution.RollbackPolicyConfig{Enabled: true}
	// Track A closure: feed distilled experiences back into the GA's
	// experience-guided mutation. guidanceProvider is non-nil only when
	// distillation was successfully wired above (PG + embedding configured).
	gaCfg.GuidanceProvider = guidanceProvider
	gaCfg.EnableExperienceGuidedMutation = guidanceProvider != nil

	// Track B closure: opt-in LLM-backed scorer. When enabled and an LLM
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
	}

	wired, wErr := evolution.NewWiredEvolutionSystem(base, gaCfg)
	if wErr != nil {
		return fmt.Errorf("wire GA population adapter: %w", wErr)
	}

	// Attach the coordinator bridge to the population adapter.
	popAdapter := wired.PopAdapter
	evolution.WithAdapterCoordinator(
		newEvol.Coordinator,
		newEvol.DiffReg,
		newEvol.GenomeReg,
	)(popAdapter)

	// In the full configuration, attach the GA adapter to the existing
	// old-system scheduler; otherwise the GA system's own scheduler
	// (registered above on the LLM callback registry) drives it.
	if comp.Evolution != nil && comp.Evolution.Scheduler != nil {
		if sched, ok := comp.Evolution.Scheduler.(*evolution.EvolutionScheduler); ok {
			sched.SetAdapter(popAdapter)
		}
	}

	// Start a background ticker that triggers evolution even when no
	// agents are running (event-driven scheduler won't fire without agents).
	// This ensures the GA continuously evolves over time.
	comp.wg.Add(1)
	go func() {
		ctx := ctx
		evoTicker := time.NewTicker(5 * time.Minute)
		defer evoTicker.Stop()
		defer comp.wg.Done()
		for {
			select {
			case <-evoTicker.C:
				if err := popAdapter.Run(ctx); err != nil {
					log.WarnContext(ctx, "[bootstrap] ticker-triggered evolution failed",
						"error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wire the LLMAdapter into the Coordinator's suggestion pipeline.
	// When an LLM client is available, periodically generate and submit
	// evolution suggestions (LLM → Parse → PatchProposal → Coordinator.Evaluate).
	if newEvol.LLMAdapter != nil && comp.LLM != nil && comp.LLM.Client != nil {
		if llmClient, ok := comp.LLM.Client.(evoService.LLMClient); ok {
			comp.wg.Add(1)
			go func() {
				suggestTicker := time.NewTicker(15 * time.Minute)
				defer suggestTicker.Stop()
				defer comp.wg.Done()
				for {
					select {
					case <-suggestTicker.C:
						// Generate a suggestion prompt for the LLM based on
						// current evolution state and recent evidence.
						prompt := "Examine the current system state and suggest one evolution improvement. " +
							"Use one of: insert node, remove node, replace node, add edge, remove edge, " +
							"change scheduler, change topk, change reducer, change planner, change recovery."
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
						return
					}
				}
			}()
			log.InfoContext(ctx, "[bootstrap] LLM suggestion pipeline wired into Coordinator")
		}
	}
	return nil
}

// wireLLMScorer constructs the opt-in LLM-backed scorer for the GA evolution
// system (Track B from the closure plan). It returns non-nil scorer functions
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

// newPGStrategyStore creates a PostgreSQL-backed strategy store from config.
// Returns nil when the database connection cannot be established, so callers
// can fall back to the in-memory store gracefully.
func newPGStrategyStore(cfg *ares_config.Config) (evolution.StrategyStore, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Storage.Host, cfg.Storage.Port, cfg.Storage.Username,
		cfg.Storage.Password, cfg.Storage.Database, cfg.Storage.SSLMode)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("pg strategy store: open db: %w", err)
	}
	// Verify the connection is alive.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pg strategy store: ping: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	store, err := evolution.NewPGStrategyStore(db, "evolution_strategies", 100)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("pg strategy store: init: %w", err)
	}
	return store, nil
}
