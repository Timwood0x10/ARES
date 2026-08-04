package ares_bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	evoService "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	_ "github.com/lib/pq"
	"golang.org/x/sync/errgroup"
)

// wireDistillation conditionally wires experience distillation (Track A) and
// returns a GuidanceProvider consumed by the GA, plus the embedding client
// used by the distillation pipeline. Both return values are nil when
// distillation is not configured/wired. Failures are non-fatal: they are
// logged and skipped, leaving the system running without distillation
// (graceful degradation). The returned embedding client is reused by
// wireRetrievers to build the MemoryRetriever, avoiding a second client.
func wireDistillation(ctx context.Context, cfg *ares_config.Config, comp *Components, deps *BootstrapDeps, cleanups *[]func()) (evolution.GuidanceProvider, *embedding.EmbeddingClient) {
	var guidanceProvider evolution.GuidanceProvider
	var embClient *embedding.EmbeddingClient
	if cfg.Storage.Enabled && cfg.Storage.Type == storageTypePostgres && cfg.Embedding.Enabled {
		pool, client, expRepo, distSvc, guidProv, wireErr := provideDistillation(ctx, cfg, comp.LLM.Client)
		if wireErr != nil {
			log.Warn("bootstrap: experience distillation not wired", "error", wireErr)
		} else {
			guidanceProvider = guidProv
			embClient = client
			comp.Distillation = distSvc
			// Feed the experience repo into the old evolution system if present.
			if deps.ExpRepo == nil {
				deps.ExpRepo = expRepo
			}
			// Back the knowledge runtime's VectorProvider with the same PG
			// pool, so AKF vector search reads the same embedded corpus the
			// distillation path writes. Best-effort: nil embedding config uses
			// defaults.
			comp.VectorStore = postgres.NewVectorSearcher(pool, nil)
			// The postgres pool must be closed if bootstrap fails later.
			*cleanups = append(*cleanups, func() { _ = pool.Close() })
			log.Info("bootstrap: experience distillation wired",
				"embedding_model", cfg.Embedding.Model)
		}
	}
	return guidanceProvider, embClient
}

// subscribeDistillationEvents starts the background distillation loop that
// turns task-completed/failed events into experiences (experience
// distillation) and, when the AKG DistillBridge is wired, into AKG
// knowledge facts (write side of the AKG loop). It is a no-op when the
// experience distillation service or the event store is unavailable.
func subscribeDistillationEvents(ctx context.Context, comp *Components) {
	if comp.Distillation == nil || comp.EventStore == nil {
		return
	}
	comp.bgGroup.Go(func() error {
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
			return nil
		}
		// akgEg runs AKG distillations off the subscriber loop so a slow
		// bridge call (LLM/embedding) cannot block experience distillation.
		akgEg, akgCtx := errgroup.WithContext(ctx)
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					// Channel closed: stop the loop and join in-flight AKG
					// distillations so no goroutine is abandoned on shutdown.
					// Each distillation is bounded by akgBridgeTimeout (30s),
					// and cancel() makes them return promptly.
					cancel()
					if waitErr := akgEg.Wait(); waitErr != nil {
						log.Warn("bootstrap: AKG distillation group error during shutdown", "error", waitErr)
					}
					return nil
				}
				HandleTaskCompletedForDistillation(ctx, comp.Distillation, ev)
				if comp.AKGBridge != nil {
					triggerAKGBridge(akgCtx, akgEg, ev, comp.AKGBridge)
				}
			case <-ctx.Done():
				// Context cancelled: join in-flight AKG distillations before
				// exiting so the subscriber goroutine does not leak them.
				if waitErr := akgEg.Wait(); waitErr != nil {
					log.Warn("bootstrap: AKG distillation group error during shutdown", "error", waitErr)
				}
				return nil
			}
		}
	})
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
	if cfg.Storage.Enabled && cfg.Storage.Type == storageTypePostgres && cfg.Storage.Host != "" {
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
	comp.bgGroup.Go(func() error {
		evoTicker := time.NewTicker(5 * time.Minute)
		defer evoTicker.Stop()
		for {
			select {
			case <-evoTicker.C:
				if err := popAdapter.Run(ctx); err != nil {
					log.WarnContext(ctx, "[bootstrap] ticker-triggered evolution failed",
						"error", err)
				}
			case <-ctx.Done():
				return nil
			}
		}
	})

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
	db, err := sql.Open(storageTypePostgres, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg strategy store: open db: %w", err)
	}
	// Verify the connection is alive.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Warn("pg strategy store: close db after ping failure", "error", closeErr)
		}
		return nil, fmt.Errorf("pg strategy store: ping: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	store, err := evolution.NewPGStrategyStore(db, "evolution_strategies", 100)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Warn("pg strategy store: close db after init failure", "error", closeErr)
		}
		return nil, fmt.Errorf("pg strategy store: init: %w", err)
	}
	return store, nil
}

// fitnessSourceKnowledge is the AKG genome source name used in fitness
// evidence summaries (shared with the knowledge runtime vector provider).
const fitnessSourceKnowledge = "knowledge"

// fitnessSourceMemory is the memory genome source name used in fitness
// evidence summaries (shared with the memory retriever emitter).
const fitnessSourceMemory = "memory"

// fitnessSourceOrder is the stable ordering of GA genome sources whose recent
// fitness evidence is summarized into the LLM suggestion prompt.
var fitnessSourceOrder = []string{"workflow", "scheduler", "recovery", fitnessSourceMemory, fitnessSourceKnowledge}

// buildEvolutionSuggestionPrompt builds an LLM suggestion prompt grounded in
// the current evolution state: the mean fitness value of the most recent
// evidence per genome source plus the currently deployed strategy. When no
// evidence or strategy exists yet, it falls back to the generic prompt so the
// LLM still has the instruction it needs. Returns the prompt string.
//
// The summary makes the LLM's suggestions state-aware instead of blind: it can
// see which genome has low fitness (and thus deserves a patch) and which
// strategy is live (and thus should be mutated with care).
func buildEvolutionSuggestionPrompt(
	ctx context.Context,
	evStore *evidence.MemoryStore,
	strategyStore evolution.StrategyStore,
) string {
	base := "Examine the current system state and suggest one evolution improvement. " +
		"Use one of: insert node, remove node, replace node, add edge, remove edge, " +
		"change scheduler, change topk, change reducer, change planner, change recovery."

	var sb strings.Builder
	sb.WriteString(base)

	if evStore != nil {
		var lines []string
		for _, src := range fitnessSourceOrder {
			mean, count, ok := recentFitnessSummary(ctx, evStore, src, fitnessWindowSize)
			if !ok {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: mean fitness %.2f over %d evidence records", src, mean, count))
		}
		if len(lines) > 0 {
			sb.WriteString("\n\nCurrent evolution state (recent fitness evidence):\n")
			sb.WriteString(strings.Join(lines, "\n"))
		}
	}

	if strategyStore != nil {
		if st, err := strategyStore.GetActive(ctx); err == nil && st != nil {
			sb.WriteString("\n\nCurrently deployed strategy: ")
			fmt.Fprintf(&sb, "id=%s version=%d", st.ID, st.Version)
			if st.Score >= 0 {
				fmt.Fprintf(&sb, " score=%.2f", st.Score)
			}
			if st.MutationDesc != "" {
				fmt.Fprintf(&sb, " mutation=%q", st.MutationDesc)
			}
		}
	}

	sb.WriteString("\n\nRespond with exactly one suggestion in the allowed format.")
	return sb.String()
}

// fitnessWindowSize bounds how many evidence records are summarized per genome
// source so a long-running process does not read the whole store each cycle.
const fitnessWindowSize = 50

// recentFitnessSummary computes the mean fitness value over the most recent
// fitness evidence records for one genome source. It returns ok=false when
// the store is nil or no usable numeric record exists in the window.
func recentFitnessSummary(ctx context.Context, store *evidence.MemoryStore, source string, limit int) (mean float64, count int, ok bool) {
	if store == nil {
		return 0, 0, false
	}
	evs, err := store.Query(ctx, evidence.Filter{
		Source: source,
		Kind:   evidence.KindFitness,
		Limit:  limit,
	})
	if err != nil {
		return 0, 0, false
	}
	var sum float64
	for _, ev := range evs {
		if len(ev.Payload) == 0 {
			continue
		}
		var fe struct {
			Value float64 `json:"value"`
		}
		if err := json.Unmarshal(ev.Payload, &fe); err != nil {
			continue
		}
		if fe.Value < 0 || fe.Value > 1 {
			continue
		}
		sum += fe.Value
		count++
	}
	if count == 0 {
		return 0, 0, false
	}
	return sum / float64(count), count, true
}
