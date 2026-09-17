package ares_bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	apiembed "github.com/Timwood0x10/ares/internal/embedding"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/runtime"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/deployment"
	ares_memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	flight "github.com/Timwood0x10/ares/internal/runtime/observability/flight"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	pgembedding "github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// bootstrapBuilder carries the mutable wiring state threaded through Bootstrap.
// It exists so each assembly phase is a method (receiver supplies state, so
// signatures stay within the ≤5-param rule) instead of a free function that
// would have to thread ctx/cfg/deps/comp/cleanups/bctx plus cross-phase locals.
// Method bodies are the original Bootstrap blocks moved verbatim; each starts by
// aliasing the fields it reads under their original local names so bodies stay
// unchanged. runCleanups is a method; on any phase error Bootstrap returns the
// phase's error after the builder has already run cleanups inside the method.
type bootstrapBuilder struct {
	ctx  context.Context
	cfg  *ares_config.Config
	deps *BootstrapDeps
	comp *Components

	bctx     context.Context
	bcancel  context.CancelFunc
	cleanups []func()

	// Cross-phase outputs not stored on comp.
	guidanceProvider evolution.GuidanceProvider
	embClient        *pgembedding.EmbeddingClient
	dag              *engine.MutableDAG
	liveMemoryStore  ares_memory.MemoryConfigStore
	evidenceStore    evidence.Store
}

// runCleanups executes all cleanup functions in reverse order. It is only ever
// invoked on bootstrap FAILURE paths (every success return skips it), so
// cancelling bctx here cannot kill workers of a healthy system.
func (b *bootstrapBuilder) runCleanups() {
	// Stop bgGroup workers first: they watch bctx, and without this cancel
	// they would keep running (event subscribers park on the store's channel;
	// the evolution scheduler's shutdown watcher parks on ctx.Done) until the
	// caller's own ctx dies — the "failed Bootstrap leaves live workers" leak.
	// No Wait() here: workers exit asynchronously and some join in-flight work
	// with their own timeouts; blocking the failure return on them would trade
	// a goroutine leak for a shutdown stall.
	b.bcancel()
	for i := len(b.cleanups) - 1; i >= 0; i-- {
		b.cleanups[i]()
	}
}

// assembleCore builds the foundation pillar: EventStore, retention, Runtime,
// Memory, MCP, and skills catalog.
func (b *bootstrapBuilder) assembleCore() error {
	ctx := b.ctx
	cfg := b.cfg
	deps := b.deps
	bctx := b.bctx
	comp := b.comp
	// 1. EventStore — from deps or create in-memory default
	if deps.EventStore != nil {
		comp.EventStore = deps.EventStore
	} else {
		comp.EventStore = ares_events.NewMemoryEventStore()
	}

	// 1b. Events-table retention (PG mode only): the serve PG event store
	// is the durable history — no round_N.json archive, no compaction trim
	// — so an explicitly configured storage.events_retention_days is the
	// only bound on its growth. Opt-in (0 = keep forever): deleting events
	// narrows the task fabric's cross-restart restore window, so the
	// default must stay "never delete". A non-PG store or a zero retention
	// simply registers no cleaner.
	if cleaner, ok := eventsRetentionCleanerFor(comp.EventStore, cfg.Storage.EventsRetentionDays); ok {
		comp.ExpiryCleaners = append(comp.ExpiryCleaners, cleaner)
		log.Info("bootstrap: events retention cleaner wired",
			"retention_days", cfg.Storage.EventsRetentionDays,
			"warning", "events older than the retention are deleted; the restore window shrinks accordingly",
		)
	}

	// 2. Runtime — always created (accepts nil eventStore)
	rt, err := ProvideRuntime(comp.EventStore)
	if err != nil {
		b.runCleanups()
		return err
	}
	comp.Runtime = rt

	// 3. Memory — only construct when cfg.Memory.IsEnabled() is true.
	// Respect the config gate so disabled = no goroutine,
	// no event subscription, no store writes.
	mem, memErr := wireMemory(cfg, comp.EventStore)
	if memErr != nil {
		b.runCleanups()
		return memErr
	}
	comp.Memory = mem

	// 4. MCP
	mcp, err := ProvideMCP(ctx, cfg.MCP)
	if err != nil {
		b.runCleanups()
		return err
	}
	comp.MCP = mcp
	b.cleanups = append(b.cleanups, func() {
		if err := mcp.Stop(ctx); err != nil {
			log.Warn("bootstrap: cleanup MCP stop error", "error", err)
		}
	})

	// 4b. SKILLS progressive disclosure: assemble the
	// skill catalog once and seed it into the memory manager so the resident
	// "Available skills" block is populated in serve (previously only the
	// `ares status` CLI constructed the catalog). The seeded registry is also
	// stored on comp.SkillsRegistry so the serve launcher can feed it to the
	// environment-capability searcher (envcap), completing the second half of
	// progressive disclosure: skills become searchable tool capabilities, not
	// just a resident prompt block. Best-effort: skipped when memory is
	// disabled or the manager does not expose SetSkillsRegistry.
	if comp.Memory != nil {
		if catalog, reg := wireSkills(ctx, comp.Memory, mcp); catalog != nil {
			comp.SkillsRegistry = reg
			comp.SkillCatalog = catalog
			b.cleanups = append(b.cleanups, func() {
				if err := catalog.Close(); err != nil {
					log.Warn("bootstrap: cleanup skills catalog close error", "error", err)
				}
			})
			// M4.4 experience WRITE side: record taskfabric terminal
			// outcomes (task.completed/failed, capability-stamped by
			// recordLocked) as {capability → success-rate} priors on the
			// catalog's Experience store. The READ side (cmd/ares
			// resolveExperienceConfidence → Fabric.Schedule fills
			// history-less candidates) closes the loop: the retired
			// recorder starved on sub_task.result (no emitter, mismatched
			// payload shape, pattern key the scheduler never queried);
			// this writer consumes the production terminal-event stream
			// at the join key the scheduler provably uses.
			b.cleanups = append(b.cleanups, startSkillOutcomeWriter(bctx, comp.EventStore, catalog.Experience()))
		}
	}
	return nil
}

// assembleExperience builds the LLM pillar plus experience distillation, the AKG loop, and the observability surfaces.
func (b *bootstrapBuilder) assembleExperience() error {
	cfg := b.cfg
	deps := b.deps
	bctx := b.bctx
	comp := b.comp
	// 5. LLM — from config (for backward compat) or from deps
	if deps.LLMClient != nil {
		comp.LLM = &LLMComponents{Client: deps.LLMClient}
	} else {
		llm, err := ProvideLLM(cfg.LLM)
		if err != nil {
			b.runCleanups()
			return err
		}
		comp.LLM = llm
	}

	// 5b + 5c. Experience distillation + auto-distill on task completion.
	// Wired conditionally (PG + embedding); failures are non-fatal.
	// embClient is reused by wireRetrievers to build the MemoryRetriever, so
	// the distillation and RAG retrieval paths share one embedding client.
	guidanceProvider, embClient := wireDistillation(bctx, cfg, comp, deps, &b.cleanups)
	// Expose the experience repository (deps-provided or distillation-
	// created) so consumers can query distilled experiences — e.g. the Agent
	// Fabric spawn path injects the latest experience as the spawn prior.
	comp.ExpRepo = deps.ExpRepo

	// AKG closed loop (0.2.9): build the KnowledgeStore (in-memory default,
	// PG optional) and the write-side DistillBridge, gated on
	// cfg.Knowledge.RetrievalEnabled. Best-effort: when AKG or its deps are
	// unavailable the loop is skipped with a warning, leaving the system
	// fully functional (read-only mode keeps the store when write deps
	// are missing). The store is shared by the knowledge runtime's
	// StoreProvider (read side) and the leader's KnowledgeRetriever.
	knowStore, akgBridge := wireAKGLoop(cfg, deps, embClient, &b.cleanups)
	comp.KnowledgeStore = knowStore
	comp.AKGBridge = akgBridge

	subscribeDistillationEvents(bctx, comp)

	// 6. Dashboard
	// The observability components (trajectory tracer, feedback
	// store, global tracer) are created ONCE here and shared: the dashboard
	// reads them via the provider adapters, and the runtime write hooks (GA
	// generation recording, task/agent lifecycle tracing) write into the same
	// instances — so the dashboard endpoints show live data, not empty lists.
	comp.Observability = &ObservabilityComponents{
		// Cap the three registries that would otherwise grow without bound
		// for the process lifetime (each has a WithMax* builder that was
		// defined but never called in production).
		EvolutionTracer: aresrecovery.NewEvolutionTracer().WithMaxGenerations(2000),
		FeedbackStore:   aresrecovery.NewFeedbackStore().WithMaxEntries(5000),
		GlobalTracer:    aresrecovery.NewGlobalTracer().WithMaxSpans(10000),
	}
	// Runtime observability providers: these surfaces now feed
	// introspect.ControlServer directly; the standalone
	// :8090 dashboard server was removed.
	comp.Dashboard = ProvideObservability(
		comp.Observability.EvolutionTracer,
		comp.Observability.FeedbackStore,
		comp.Observability.GlobalTracer,
	)
	b.guidanceProvider = guidanceProvider
	b.embClient = embClient
	return nil
}

// assembleEvolutionDAG builds the evolution DAG, live memory store, knowledge
// runtime, and evidence store.
func (b *bootstrapBuilder) assembleEvolutionDAG() error {
	cfg := b.cfg
	comp := b.comp
	embClient := b.embClient
	knowStore := comp.KnowledgeStore
	// 7+8. Evolution wiring order matters: ProvideNewEvolution (below) creates
	// the shared evidence store (newEvol.EvidenceStore); ProvideEvolution's
	// flight recorder must be built AFTER it so the flight collector's
	// workflow/scheduler/recovery fitness evidence lands in the same store
	// the GA genomes read (previously the recorder got a nil EvidenceStore
	// and those three fitness signals were silently dropped).

	// 8. New Evolution — runtime-evolution system (Genome + Diff + Coordinator).
	// Only construct when cfg.Evolution.Enabled is true.
	// When disabled, no NewEvolution, no GA ticker, no LLM suggestion ticker.
	if !cfg.Evolution.Enabled {
		log.Info("bootstrap: evolution disabled (cfg.Evolution.Enabled=false), " +
			"skipping NewEvolution and background tickers")
	}
	dag, dagErr := buildEvolutionDAG(cfg.Evolution.Enabled)
	if dagErr != nil {
		b.runCleanups()
		return dagErr
	}

	// Type-assert comp.Memory to MemoryConfigStore. Both *memoryManager and
	// *ProductionMemoryManager implement MemoryConfigStore. When Memory is
	// disabled (comp.Memory is nil), fall back to the minimal manager so
	// the evolution system still has a MemoryConfigStore to write patches to.
	liveMemoryStore := resolveLiveMemoryStore(comp.Memory)

	// Create the KnowledgeRuntime once and share it between the evolution
	// system and the agent's AKF tools so knowledge genome patches affect
	// the actual runtime used by the agent's knowledge tools. The vector
	// provider is registered when postgres vector storage + embedding are
	// wired (comp.VectorStore / embClient); otherwise the runtime uses only
	// the memory/code providers.
	// Convert nil *EmbeddingClient to nil EmbeddingService interface to avoid
	// the Go nil-interface-trap: a nil typed pointer wrapped in a non-nil
	// interface passes nil checks but panics on method calls (e.g. GetModel).
	var embForRuntime apiembed.EmbeddingService
	if embClient != nil {
		embForRuntime = embClient
	}
	knowRt := BuildKnowledgeRuntime(comp.VectorStore, embForRuntime, knowStore)
	comp.KnowledgeRuntime = knowRt

	// When PostgreSQL is configured, use a
	// persistent evidence store instead of the default in-memory one.
	// Fail-loud: configured Postgres that cannot connect blocks startup.
	var evidenceStore evidence.Store
	if cfg.Storage.Enabled && cfg.Storage.Host != "" {
		pgCfg := &postgres.Config{
			Host:     cfg.Storage.Host,
			Port:     cfg.Storage.Port,
			User:     cfg.Storage.Username,
			Password: cfg.Storage.Password,
			Database: cfg.Storage.Database,
			SSLMode:  cfg.Storage.SSLMode,
		}
		pgPool, pgErr := postgres.NewPool(pgCfg)
		if pgErr != nil {
			b.runCleanups()
			return fmt.Errorf("evidence: create postgres pool: %w", pgErr)
		}
		pgStore, storeErr := evidence.NewPostgresStore(pgPool)
		if storeErr != nil {
			b.runCleanups()
			return fmt.Errorf("evidence: create postgres store: %w", storeErr)
		}
		evidenceStore = pgStore
		// Register the evidence store with the maintenance worker
		// so TTL-expired rows are purged on the same hourly schedule as the
		// other retention-managed tables. Query already hides expired rows;
		// this stops the table growing unboundedly with dead ones.
		comp.ExpiryCleaners = append(comp.ExpiryCleaners,
			NamedExpiryCleaner{Name: "evidence_records", Cleaner: pgStore})
		b.cleanups = append(b.cleanups, func() {
			if cerr := pgPool.Close(); cerr != nil {
				log.Warn("bootstrap: close evidence postgres pool",
					"error", cerr)
			}
		})
	}
	b.dag = dag
	b.liveMemoryStore = liveMemoryStore
	b.evidenceStore = evidenceStore
	return nil
}

// assembleNewEvolution wires the shared evidence store into NewEvolution and starts the flight recorder.
func (b *bootstrapBuilder) assembleNewEvolution() error {
	ctx := b.ctx
	cfg := b.cfg
	bctx := b.bctx
	comp := b.comp
	dag := b.dag
	liveMemoryStore := b.liveMemoryStore
	evidenceStore := b.evidenceStore
	knowRt := comp.KnowledgeRuntime
	newEvol, evStore, evErr := wireNewEvolution(cfg.Evolution.Enabled, dag, knowRt, liveMemoryStore, evidenceStore)
	if evErr != nil {
		b.runCleanups()
		return evErr
	}
	comp.NewEvolution = newEvol
	comp.EvidenceStore = evStore

	// Single shared flight recorder — created and started here, independent
	// of the legacy evolution deps (ExpRepo). Its collector subscribes to
	// comp.EventStore and emits workflow/scheduler/recovery fitness evidence
	// into the shared evidence store (the same store the GA genomes read when
	// evolution is enabled), so the fitness write loop works on every
	// production path (ares serve / ares start) even when ProvideEvolution is
	// skipped. ProvideEvolution and the serve launcher reuse this instance.
	if comp.EventStore != nil {
		comp.FlightRecorder = flight.NewFlightRecorder(flight.FlightRecorderConfig{
			EventStore:    comp.EventStore,
			EvidenceStore: evStore,
		})
		if err := comp.FlightRecorder.Start(bctx); err != nil {
			log.WarnContext(ctx, "bootstrap: flight recorder start failed (fitness evidence disabled)",
				"error", err)
		}
		b.cleanups = append(b.cleanups, comp.FlightRecorder.Stop)
	}
	return nil
}

// assembleLegacyEvolution wires the legacy evolution system and its scheduler
// shutdown watcher.
func (b *bootstrapBuilder) assembleLegacyEvolution() error {
	cfg := b.cfg
	deps := b.deps
	bctx := b.bctx
	comp := b.comp
	// 7. Evolution (legacy system) — only if all required deps are wired.
	// Built after the shared recorder so it reuses comp.FlightRecorder
	// (which shares the evidence store with the GA genomes) instead of
	// constructing a second recorder. Fully gated by cfg.Evolution.Enabled
	// so the legacy scheduler/dream cycle cannot start behind the
	// config's back.
	evol, err := wireLegacyEvolution(bctx, cfg, deps, comp)
	if err != nil {
		b.runCleanups()
		return err
	}
	comp.Evolution = evol
	// ProvideEvolution calls scheduler.Register(), which subscribes to the
	// EventStore on a context.Background() of its own and parks a goroutine on
	// the event channel. Nothing used to call Shutdown(), so that goroutine —
	// plus the EventStore subscriber goroutine feeding it — outlived every
	// Runtime and Bootstrap for the life of the process. goleak surfaced it;
	// counting goroutines by hand never would have.
	if evol != nil {
		if sched, ok := evol.Scheduler.(*evolution.EvolutionScheduler); ok && sched != nil {
			comp.bgGroup.Go(func() error {
				// bctx: on bootstrap failure runCleanups cancels this
				// watcher, which shuts the scheduler's subscription down —
				// the caller's ctx may outlive the failed Bootstrap.
				<-bctx.Done()
				sched.Shutdown()
				return nil
			})
		}
	}
	return nil
}

// wireEvolutionWiring injects retrievers, wires the deployment pipeline, and
// registers the minimal DAG.
func (b *bootstrapBuilder) wireEvolutionWiring() {
	ctx := b.ctx
	cfg := b.cfg
	deps := b.deps
	comp := b.comp
	embClient := b.embClient
	knowRt := comp.KnowledgeRuntime
	knowStore := comp.KnowledgeStore
	evStore := comp.EvidenceStore
	dag := b.dag
	// Closed-loop wiring: inject MemoryRetriever (distilled experiences) and
	// KnowledgeRetriever (AKG entries) into the MemoryManager so every
	// BuildContext / BuildPromptMessages call augments the prompt with
	// retrieved context when config.EnableRAG is true. Best-effort: skips
	// retrievers whose dependencies (embedding client, experience repo, AKG
	// runtime) are unavailable, so minimal configs are unaffected.
	//
	// Runs after ProvideNewEvolution so the retriever can emit retrieval
	// evidence to the shared evidence store (Source "memory") consumed by the
	// GA MemoryGenome.
	wireRetrievers(ctx, cfg, comp.Memory, embClient, deps.ExpRepo, knowRt, knowStore, evStore)

	// Wire the DeploymentPipeline into the Coordinator so
	// generated patches are safely promoted to the live runtime. Gated by
	// cfg.Evolution.Deployment.Enabled — when disabled, the Coordinator falls
	// back to applying patches directly (pre-deployment behavior). The live
	// runtime is the real executor registry, so memory patches are written to
	// the live comp.Memory; workflow/scheduler/recovery/knowledge patches hit
	// their (still synthetic) executors — closing those requires a live DAG
	// supply chain (deferred).
	if cfg.Evolution.Enabled && cfg.Evolution.Deployment.Enabled && comp.NewEvolution != nil {
		staging := &deploymentStagingRuntime{
			reg: comp.NewEvolution.PatchReg,
			// Shared scoring backend (same weights/filter as the
			// lifecycle's rollback window) + explicit cold-start score so
			// patches without evidence get a conservative 0.5 instead of a
			// universal 0.0 reject.
			agg:            evolution.NewRuntimeFitnessAggregator(comp.EvidenceStore, evolution.DefaultAggregatorConfig()),
			coldStartScore: 0.5,
			// Baseline scoring resolves the active strategy live at Evaluate
			// time via the ASM, so a strategy switched mid-run is reflected
			// in the comparison instead of a stale construction-time ID.
			asm: comp.NewEvolution.ActiveStrategyManager,
		}
		dp := deployment.NewDeploymentPipeline(
			cfg.Evolution.Deployment,
			staging,
			&deploymentLiveRuntime{reg: comp.NewEvolution.PatchReg},
		)
		comp.NewEvolution.Coordinator.SetDeployer(&deploymentAdapter{dp: dp})
		log.Info("bootstrap: deployment pipeline wired into coordinator", "enabled", true)
	}

	// Register the minimal DAG with the runtime manager so the evolution
	// system can apply workflow patches to the live DAG.
	// When a real agent DAG is registered later, it replaces this minimal one.
	if comp.Runtime != nil && dag != nil {
		comp.Runtime.RegisterAgentDAG(runtime.AgentDAGEvolutionKey, dag)
	}

	// A standalone Bootstrap has no agent
	// population, so no live agent DAG exists at this point — evolution
	// verdicts are available but have no live topology to act on. Say so
	// explicitly instead of letting the synthetic graph silently take
	// promotions. The serve entry (buildLiveAgentDAG + UpdateLiveDAG) is the
	// only live-DAG supplier and supersedes this placeholder afterwards.
	if cfg.Evolution.Enabled {
		log.InfoContext(ctx, "bootstrap: evolution verdicts available but no live agent topology to act on",
			"live_dag_registered", false,
			"synthetic_dag_key", runtime.AgentDAGEvolutionKey,
		)
	}
}

// wirePlatform wires GA evolution, service discovery, the system runtime, and the expiry-cleanup worker.
func (b *bootstrapBuilder) wirePlatform() error {
	ctx := b.ctx
	cfg := b.cfg
	bctx := b.bctx
	comp := b.comp
	guidanceProvider := b.guidanceProvider
	// 9. Wire the GA population adapter, coordinator bridge, and background
	// evolution ticker (extracted to wireGAEvolution to keep Bootstrap's
	// cyclomatic complexity within lint limits).
	if cfg.Evolution.Enabled && comp.NewEvolution != nil {
		if err := wireGAEvolution(bctx, cfg, comp, comp.NewEvolution, guidanceProvider, &b.cleanups); err != nil {
			b.runCleanups()
			return err
		}
	}

	// 10. Optional service discovery (opt-in via config.Discovery.Enabled).
	// When disabled, ProvideDiscovery returns ErrDiscoveryDisabled and the
	// discovery packages remain unused, preserving prior behavior.
	// Pass bctx (not the caller's ctx) so runCleanups cancels the
	// auto-discovery loop when a later bootstrap step fails.
	discoveryComp, err := ProvideDiscovery(bctx, &cfg.Discovery, comp.EventStore)
	switch {
	case errors.Is(err, ErrDiscoveryDisabled):
		// Discovery is disabled — not an error, just no-op.
		comp.Discovery = nil
	case err != nil:
		b.runCleanups()
		return fmt.Errorf("bootstrap: wire discovery: %w", err)
	default:
		comp.Discovery = discoveryComp
	}

	// 11. System Runtime: register the assembled component graph
	// with the system-level control plane so entry points observe a uniform
	// component list, lifecycle state, and readiness snapshot. Observational
	// only — construction and startup stay with Bootstrap.
	orch, sysReg, sysErr := wireSystemRuntime(ctx, cfg, comp)
	if sysErr != nil {
		b.runCleanups()
		return sysErr
	}
	comp.SystemRuntime = orch
	comp.SystemRegistry = sysReg

	// Purge expired/decayed rows on a schedule instead
	// of letting retention-managed tables grow unboundedly. Started last so it
	// sees every cleaner registered above (sessions, conversations, knowledge,
	// secrets, and the evidence store). No-op when no cleaners were wired
	// (e.g. storage disabled).
	startExpiryCleanupWorker(bctx, comp)
	return nil
}
