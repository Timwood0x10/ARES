package ares_bootstrap

import (
	"context"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// wireDistillation conditionally wires experience distillation and
// returns a GuidanceProvider consumed by the GA, plus the embedding client
// used by the distillation pipeline. Both return values are nil when
// distillation is not configured/wired. Failures are non-fatal: they are
// logged and skipped, leaving the system running without distillation
// (graceful degradation). The returned embedding client is reused by
// wireRetrievers to build the MemoryRetriever, avoiding a second client.
func wireDistillation(ctx context.Context, cfg *ares_config.Config, comp *Components, deps *BootstrapDeps, cleanups *[]func()) (evolution.GuidanceProvider, *embedding.EmbeddingClient) {
	var guidanceProvider evolution.GuidanceProvider
	var embClient *embedding.EmbeddingClient
	// Honor memory.enable_distillation — tri-state gate (nil defaults to
	// true), so deployments relying on Storage+Embedding keep
	// distillation; only an explicit YAML false disables the wiring.
	if !cfg.Memory.DistillationEnabled() {
		return nil, nil
	}
	if cfg.Storage.Enabled && cfg.Storage.Type == storageTypePostgres && cfg.Embedding.Enabled {
		wiring, wireErr := provideDistillation(ctx, cfg, comp.LLM.Client)
		if wireErr != nil {
			log.Warn("bootstrap: experience distillation not wired", "error", wireErr)
		} else {
			pool, expRepo := wiring.pool, wiring.experienceRepo
			guidanceProvider = wiring.guidanceProvider
			embClient = wiring.embeddingClient
			comp.Distillation = wiring.service
			// Feed the experience repo into the old evolution system if present.
			if deps.ExpRepo == nil {
				deps.ExpRepo = expRepo
			}
			// Register the repo's decay purge with the maintenance
			// worker so decayed experience rows are deleted, not just filtered
			// on read. The concrete *ExperienceRepository implements
			// CleanupExpired; the fat interface intentionally stays untouched.
			if cleaner, ok := expRepo.(ExpiryCleaner); ok {
				comp.ExpiryCleaners = append(comp.ExpiryCleaners,
					NamedExpiryCleaner{Name: storage_models.ExperiencesTable, Cleaner: cleaner})
			}
			// Register the other retention-managed
			// tables (sessions, conversations, secrets, knowledge_chunks) so
			// their expired/decayed rows are purged too, not just experiences.
			// They share the distillation pool (already open for the process
			// lifetime) instead of opening a second pool — minimal wiring.
			wireExpiryCleaners(comp, pool.GetDB(), cfg)
			// The embedding queue worker + reconciler consume pending tasks and
			// write vectors back to knowledge_chunks_1024 and experiences_1024
			// (both repos share the same pool). The producer side is wired in
			// provide_distillation: the distillation path persists an experience
			// row without a vector and enqueues a backfill task so the async
			// worker writes the vector back instead of blocking the event
			// subscriber loop on a synchronous embed. The LLM extraction call
			// (30s) still runs in the subscriber loop; only the embed was
			// deferred to the worker. The queue instance is shared with the
			// producer rather than rebuilt here.
			knowRepo := repositories.NewKnowledgeRepository(pool.GetDB(), pool.GetDB())
			var expConcreteRepo *repositories.ExperienceRepository
			if r, ok := expRepo.(*repositories.ExperienceRepository); ok {
				expConcreteRepo = r
			}
			wireEmbeddingWorker(ctx, comp, pool, embClient,
				wiring.embeddingQueue, wiring.embeddingConfig, knowRepo, expConcreteRepo)
			// Back the knowledge runtime's VectorProvider with the same PG
			// pool, so AKF vector search reads the same embedded corpus the
			// distillation path writes. Best-effort: nil embedding config uses
			// defaults.
			comp.VectorStore = postgres.NewVectorSearcher(pool, nil)
			// The postgres pool must be closed if bootstrap fails later.
			*cleanups = append(*cleanups, func() {
				if cerr := pool.Close(); cerr != nil {
					log.Warn("bootstrap: close distillation postgres pool",
						"error", cerr)
				}
			})
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

// maxShadowReplayHorizon is the widest total replay history (window span ×
// MinSamples) the shadow sampler is expected to walk backwards before the
// oldest comparison stops describing current behaviour. Purely advisory: the
// bootstrap warns above it and never clamps, because a low-traffic deployment
// may legitimately need a longer horizon to find any evidence at all.
const maxShadowReplayHorizon = 24 * time.Hour
