package ares_bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
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

// wireGAEvolution wires the GA population adapter (step 9 of Bootstrap): it
// builds the GA system, attaches the coordinator bridge to the population
// adapter, and starts the background evolution ticker. Extracted from Bootstrap
// to keep its cyclomatic complexity within lint limits.
func wireGAEvolution(ctx context.Context, cfg *ares_config.Config, comp *Components, newEvol *NewEvolutionComponents, guidanceProvider evolution.GuidanceProvider) error {
	memStore := evolution.NewMemoryStrategyStore(0)
	newEvol.StrategyStore = memStore

	base := &mutation.Strategy{
		ID:     "bootstrap-root",
		Params: map[string]any{"temperature": 0.7, "max_tokens": 4096},
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
	return nil
}
