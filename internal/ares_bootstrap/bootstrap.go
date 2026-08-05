// Package ares_bootstrap orchestrates component assembly.
package ares_bootstrap

import (
	"context"
	"errors"
	"fmt"

	apiembed "github.com/Timwood0x10/ares/api/embedding"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	ares_memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/storage"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/Timwood0x10/ares/internal/system_runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"

	"golang.org/x/sync/errgroup"
)

// DAG step identifiers used in the minimal evolution graph.
const dagStepProcess = "process"

// Components holds all assembled system components.
type Components struct {
	MCP          *ares_mcp.MCPManager
	Dashboard    *DashboardComponents
	LLM          *LLMComponents
	Evolution    *EvolutionComponents
	NewEvolution *NewEvolutionComponents
	Runtime      *ares_runtime.Manager
	Memory       ares_memory.MemoryManager
	EventStore   ares_events.EventStore
	Distillation *aresexp.DistillationService
	// Discovery holds the optional service discovery engine. It is nil when
	// cfg.Discovery.Enabled is false (the default), preserving prior behavior.
	Discovery *DiscoveryComponents
	// KnowledgeRuntime is the shared knowledge runtime used by the evolution
	// system's KnowledgePatchExecutor and the agent's AKF tools. It is
	// created once during bootstrap and reused so that knowledge genome
	// patches (ChangeBudget/ChangePlanner/ChangeReducer) affect the actual
	// runtime used by the agent's knowledge tools.
	KnowledgeRuntime *knowledgeruntime.KnowledgeRuntime
	// VectorStore backs the knowledge runtime's VectorProvider (semantic
	// search over embedded documents). It is nil when distillation/vector
	// storage is not wired, in which case the runtime skips the vector
	// provider entirely.
	VectorStore storage.VectorStore
	// KnowledgeStore backs the AKG read/write loop: the DistillBridge
	// (write side) persists AKG facts here and the knowledge runtime's
	// StoreProvider / the leader's KnowledgeRetriever (read side) recall
	// them. In-memory by default; PostgreSQL when storage is configured.
	// Nil when AKG is not enabled (cfg.Knowledge.RetrievalEnabled).
	KnowledgeStore knowledge.KnowledgeStore
	// AKGBridge distills conversations into KnowledgeStore on task
	// lifecycle events (write side of the AKG loop). Nil when AKG or its
	// write dependencies (embedding client, experience repo) are
	// unavailable.
	AKGBridge *adapter.DistillBridge
	// FlightRecorder is the single shared flight recorder (collector
	// subscribes to comp.EventStore and emits workflow/scheduler/recovery
	// fitness evidence into the shared evidence store). It is created and
	// started by Bootstrap independently of ProvideEvolution so the fitness
	// write loop works even when the legacy evolution deps (ExpRepo) are
	// absent; ProvideEvolution and the api_impl launcher reuse it instead of
	// building their own. Nil when the event store is unavailable.
	FlightRecorder *flight.FlightRecorder
	// EvidenceStore is the shared evidence store used by the flight recorder
	// and (when enabled) the GA genomes. Always set, even when evolution is
	// disabled, so downstream consumers (api/bootstrap, integration) can
	// reference it without nil guards.
	EvidenceStore evidence.Store
	// SystemRuntime is the system-level control plane (Stage 1): an
	// orchestrator that observes the assembled component graph and provides
	// lifecycle states, a shared root context, and status snapshots. It is
	// created at the end of Bootstrap; nil when wiring is skipped on failure.
	SystemRuntime *system_runtime.Orchestrator
	// SystemRegistry backs SystemRuntime with one entry per constructed
	// component, enabling dependency-aware lookup and snapshot queries.
	SystemRegistry *system_runtime.Registry
	// bgGroup manages all Bootstrap background goroutines (distillation
	// subscriber, GA evolution ticker, LLM suggestion ticker) via errgroup
	// (F06: no bare goroutines). WaitBackground blocks on it during shutdown.
	bgGroup errgroup.Group
}

// WaitBackground blocks until all background goroutines started by Bootstrap
// (distillation event subscriber, GA evolution ticker, LLM suggestion ticker)
// have exited. It must be called after the bootstrap context is cancelled;
// each goroutine exits on ctx.Done() and this ensures no goroutine is left
// running across a graceful shutdown.
func (c *Components) WaitBackground() {
	if c == nil {
		return
	}
	if err := c.bgGroup.Wait(); err != nil {
		log.Warn("bootstrap: background group error during shutdown", "error", err)
	}
}

// Snapshot returns the system-level component status snapshot (Stage 1
// observability). It returns an empty snapshot when the System Runtime
// registry is not wired (Bootstrap failed before wiring completed), so
// callers can always consume a valid value without nil guards.
func (c *Components) Snapshot() system_runtime.Snapshot {
	if c == nil || c.SystemRegistry == nil {
		return system_runtime.Snapshot{}
	}
	return c.SystemRegistry.Snapshot()
}

// ComponentStatus returns the status of one managed component by name.
// The bool is false when the component is not registered.
func (c *Components) ComponentStatus(name string) (system_runtime.ComponentStatus, bool) {
	if c == nil || c.SystemRegistry == nil {
		return system_runtime.ComponentStatus{}, false
	}
	return c.SystemRegistry.GetStatus(name)
}

// IsSystemReady reports whether all Required components reached Ready and no
// component is Failed. Returns false when the registry is not wired.
func (c *Components) IsSystemReady() bool {
	if c == nil || c.SystemRegistry == nil {
		return false
	}
	return c.SystemRegistry.IsReady()
}

// LLMComponents holds LLM client and callback registry.
type LLMComponents struct {
	Client      interface{}
	CallbackReg *ares_callbacks.Registry
}

// BootstrapDeps holds optional external dependencies for full wiring.
type BootstrapDeps struct {
	EventStore ares_events.EventStore
	ExpRepo    repositories.ExperienceRepositoryInterface
	LLMClient  ares_eval.LLMClient
}

// Bootstrap assembles all components from config and optional dependencies.
// It is the single wiring hub — used by api/bootstrap, cmd/ares serve, and tests.
// On partial failure, already-created components are cleaned up in reverse
// order before returning the error.
// extracted for each major component group (wireMemory, wireNewEvolution, etc.)
// and the remaining complexity is inherent to the assembly orchestration.
//
//nolint:gocyclo // Bootstrap is a complex wiring hub; sub-functions are
func Bootstrap(ctx context.Context, cfg *ares_config.Config, deps *BootstrapDeps) (*Components, error) {
	var comp Components

	if deps == nil {
		deps = &BootstrapDeps{}
	}

	// Track cleanup functions for components created during bootstrap.
	// On error, they are executed in reverse order of creation.
	var cleanups []func()

	// runCleanups executes all cleanup functions in reverse order.
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 1. EventStore — from deps or create in-memory default
	if deps.EventStore != nil {
		comp.EventStore = deps.EventStore
	} else {
		comp.EventStore = ares_events.NewMemoryEventStore()
	}

	// 2. Runtime — always created (accepts nil eventStore)
	rt, err := ProvideRuntime(comp.EventStore)
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.Runtime = rt

	// 3. Memory — only construct when cfg.Memory.Enabled is true.
	// Stage 2 fix (F01): respect the config gate so disabled = no goroutine,
	// no event subscription, no store writes.
	mem, memErr := wireMemory(cfg, comp.EventStore)
	if memErr != nil {
		runCleanups()
		return nil, memErr
	}
	comp.Memory = mem

	// 4. MCP
	mcp, err := ProvideMCP(ctx, cfg.MCP)
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.MCP = mcp
	cleanups = append(cleanups, func() {
		if err := mcp.Stop(ctx); err != nil {
			log.Warn("bootstrap: cleanup MCP stop error", "error", err)
		}
	})

	// 5. LLM — from config (for backward compat) or from deps
	if deps.LLMClient != nil {
		comp.LLM = &LLMComponents{Client: deps.LLMClient}
	} else {
		llm, err := ProvideLLM(cfg.LLM)
		if err != nil {
			runCleanups()
			return nil, err
		}
		comp.LLM = llm
	}

	// 5b + 5c. Experience distillation + auto-distill on task completion
	// (Track A). Wired conditionally (PG + embedding); failures are non-fatal.
	// embClient is reused by wireRetrievers to build the MemoryRetriever, so
	// the distillation and RAG retrieval paths share one embedding client.
	guidanceProvider, embClient := wireDistillation(ctx, cfg, &comp, deps, &cleanups)

	// AKG closed loop (0.2.9): build the KnowledgeStore (in-memory default,
	// PG optional) and the write-side DistillBridge, gated on
	// cfg.Knowledge.RetrievalEnabled. Best-effort: when AKG or its deps are
	// unavailable the loop is skipped with a warning, leaving the system
	// fully functional (read-only mode keeps the store when write deps
	// are missing). The store is shared by the knowledge runtime's
	// StoreProvider (read side) and the leader's KnowledgeRetriever.
	knowStore, akgBridge := wireAKGLoop(cfg, deps, embClient)
	comp.KnowledgeStore = knowStore
	comp.AKGBridge = akgBridge

	subscribeDistillationEvents(ctx, &comp)

	// 6. Dashboard
	dash, err := ProvideDashboard(ctx, mcp, cfg.Dashboard.Addr)
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.Dashboard = dash
	cleanups = append(cleanups, func() {
		if err := dash.Stop(ctx); err != nil {
			log.Warn("bootstrap: cleanup dashboard stop error", "error", err)
		}
	})

	// 7+8. Evolution wiring order matters: ProvideNewEvolution (below) creates
	// the shared evidence store (newEvol.EvidenceStore); ProvideEvolution's
	// flight recorder must be built AFTER it so the flight collector's
	// workflow/scheduler/recovery fitness evidence lands in the same store
	// the GA genomes read (previously the recorder got a nil EvidenceStore
	// and those three fitness signals were silently dropped).

	// 8. New Evolution — runtime-evolution system (Genome + Diff + Coordinator).
	// Stage 2 fix (F02): only construct when cfg.Evolution.Enabled is true.
	// When disabled, no NewEvolution, no GA ticker, no LLM suggestion ticker.
	if !cfg.Evolution.Enabled {
		log.Info("bootstrap: evolution disabled (cfg.Evolution.Enabled=false), " +
			"skipping NewEvolution and background tickers")
	}
	dag, dagErr := buildEvolutionDAG(cfg.Evolution.Enabled)
	if dagErr != nil {
		runCleanups()
		return nil, dagErr
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

	// T1 (evidence persistence): when PostgreSQL is configured, use a
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
			runCleanups()
			return nil, fmt.Errorf("evidence: create postgres pool: %w", pgErr)
		}
		pgStore, storeErr := evidence.NewPostgresStore(pgPool)
		if storeErr != nil {
			runCleanups()
			return nil, fmt.Errorf("evidence: create postgres store: %w", storeErr)
		}
		evidenceStore = pgStore
		cleanups = append(cleanups, func() { _ = pgPool.Close() })
	}

	newEvol, evStore, evErr := wireNewEvolution(cfg.Evolution.Enabled, dag, knowRt, liveMemoryStore, evidenceStore)
	if evErr != nil {
		runCleanups()
		return nil, evErr
	}
	comp.NewEvolution = newEvol
	comp.EvidenceStore = evStore

	// Single shared flight recorder — created and started here, independent
	// of the legacy evolution deps (ExpRepo). Its collector subscribes to
	// comp.EventStore and emits workflow/scheduler/recovery fitness evidence
	// into the shared evidence store (the same store the GA genomes read when
	// evolution is enabled), so the fitness write loop works on every
	// production path (ares serve / ares start) even when ProvideEvolution is
	// skipped. ProvideEvolution and the api_impl launcher reuse this instance.
	if comp.EventStore != nil {
		comp.FlightRecorder = flight.NewFlightRecorder(flight.FlightRecorderConfig{
			EventStore:    comp.EventStore,
			EvidenceStore: evStore,
		})
		if err := comp.FlightRecorder.Start(ctx); err != nil {
			log.WarnContext(ctx, "bootstrap: flight recorder start failed (fitness evidence disabled)",
				"error", err)
		}
		cleanups = append(cleanups, comp.FlightRecorder.Stop)
	}

	// 7. Evolution (legacy system) — only if all required deps are wired.
	// Built after the shared recorder so it reuses comp.FlightRecorder
	// (which shares the evidence store with the GA genomes) instead of
	// constructing a second recorder. Fully gated by cfg.Evolution.Enabled
	// (F02) so the legacy scheduler/dream cycle cannot start behind the
	// config's back.
	evol, err := wireLegacyEvolution(ctx, cfg, deps, &comp)
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.Evolution = evol

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

	// Track C (C-Safe): wire the DeploymentPipeline into the Coordinator so
	// generated patches are safely promoted to the live runtime. Gated by
	// cfg.Evolution.Deployment.Enabled — when disabled, the Coordinator falls
	// back to applying patches directly (pre-deployment behavior). The live
	// runtime is the real executor registry, so memory patches are written to
	// the live comp.Memory; workflow/scheduler/recovery/knowledge patches hit
	// their (still synthetic) executors — closing those requires a live DAG
	// supply chain (Track C-Risky, deferred).
	if cfg.Evolution.Enabled && cfg.Evolution.Deployment.Enabled && comp.NewEvolution != nil {
		dp := deployment.NewDeploymentPipeline(
			cfg.Evolution.Deployment,
			&deploymentStagingRuntime{reg: comp.NewEvolution.PatchReg, evidenceStore: comp.EvidenceStore},
			&deploymentLiveRuntime{reg: comp.NewEvolution.PatchReg},
		)
		comp.NewEvolution.Coordinator.SetDeployer(&deploymentAdapter{dp: dp})
		log.Info("bootstrap: deployment pipeline wired into coordinator", "enabled", true)
	}

	// Register the minimal DAG with the runtime manager so the evolution
	// system can apply workflow patches to the live DAG (v0.5.0 DAG reflux).
	// When a real agent DAG is registered later, it replaces this minimal one.
	if comp.Runtime != nil && dag != nil {
		comp.Runtime.RegisterAgentDAG("evolution", dag)
	}

	// 9. Wire the GA population adapter, coordinator bridge, and background
	// evolution ticker (extracted to wireGAEvolution to keep Bootstrap's
	// cyclomatic complexity within lint limits).
	if cfg.Evolution.Enabled && comp.NewEvolution != nil {
		if err := wireGAEvolution(ctx, cfg, &comp, comp.NewEvolution, guidanceProvider); err != nil {
			runCleanups()
			return nil, err
		}
	}

	// 10. Optional service discovery (opt-in via config.Discovery.Enabled).
	// When disabled, ProvideDiscovery returns ErrDiscoveryDisabled and the
	// discovery packages remain unused, preserving prior behavior.
	discoveryComp, err := ProvideDiscovery(ctx, &cfg.Discovery)
	switch {
	case errors.Is(err, ErrDiscoveryDisabled):
		// Discovery is disabled — not an error, just no-op.
		comp.Discovery = nil
	case err != nil:
		runCleanups()
		return nil, fmt.Errorf("bootstrap: wire discovery: %w", err)
	default:
		comp.Discovery = discoveryComp
	}

	// 11. System Runtime (Stage 1): register the assembled component graph
	// with the system-level control plane so entry points observe a uniform
	// component list, lifecycle state, and readiness snapshot. Observational
	// only — construction and startup stay with Bootstrap.
	orch, sysReg, sysErr := wireSystemRuntime(ctx, cfg, &comp)
	if sysErr != nil {
		runCleanups()
		return nil, sysErr
	}
	comp.SystemRuntime = orch
	comp.SystemRegistry = sysReg

	return &comp, nil
}

// wireMemory constructs the memory manager when cfg.Memory.Enabled is true.
// Stage 2 fix (F01): disabled = no goroutine, no event subscription, no store
// writes, so the gate is honored here instead of constructing unconditionally.
// Stage 3 fix (B01): the event store is wired during construction, eliminating
// the post-Bootstrap SetEventStore bypass in serve.go. Returns nil when disabled.
//
//nolint:nilnil // nil manager + nil error is the documented "disabled" contract.
func wireMemory(cfg *ares_config.Config, eventStore ares_events.EventStore) (ares_memory.MemoryManager, error) {
	if !cfg.Memory.Enabled {
		log.Info("bootstrap: memory disabled (cfg.Memory.Enabled=false), skipping construction")
		return nil, nil
	}
	memCfg := ares_memory.DefaultMemoryConfig()
	if cfg.Memory.EnableRAG {
		memCfg.EnableRAG = true
		if cfg.Memory.RAGTopK > 0 {
			memCfg.RAGTopK = cfg.Memory.RAGTopK
		}
		if cfg.Memory.RAGMinScore > 0 {
			memCfg.RAGMinScore = cfg.Memory.RAGMinScore
		}
	}
	mem, err := ProvideMemory(memCfg)
	if err != nil {
		return nil, err
	}
	if eventStore != nil {
		mem.SetEventStore(eventStore, "memory")
	}
	return mem, nil
}

// buildEvolutionDAG builds the minimal mutable DAG used by the evolution system
// (workflow/scheduler/recovery genomes evolve against it). Returns nil when
// evolution is disabled so no graph is constructed behind the config's back.
//
//nolint:nilnil // nil DAG + nil error is the documented "disabled" contract.
func buildEvolutionDAG(enabled bool) (*engine.MutableDAG, error) {
	if !enabled {
		return nil, nil
	}
	dagSteps := []*engine.Step{
		{ID: "input", Name: "Input", AgentType: "parser", Input: "parse input"},
		{ID: dagStepProcess, Name: "Process", AgentType: "processor", Input: dagStepProcess, DependsOn: []string{"input"}},
		{ID: "output", Name: "Output", AgentType: "formatter", Input: "format", DependsOn: []string{dagStepProcess}},
	}
	dag, err := engine.NewMutableDAG(dagSteps)
	if err != nil {
		return nil, fmt.Errorf("create mutable dag: %w", err)
	}
	return dag, nil
}

// resolveLiveMemoryStore returns the live memory config store from the
// constructed memory manager. Both *memoryManager and *ProductionMemoryManager
// implement MemoryConfigStore; when memory is disabled or the type assertion
// fails, the minimal manager is used so evolution still has a config store.
func resolveLiveMemoryStore(mem ares_memory.MemoryManager) ares_memory.MemoryConfigStore {
	if mem != nil {
		if store, ok := mem.(ares_memory.MemoryConfigStore); ok {
			return store
		}
	}
	return buildMemoryManager()
}

// wireNewEvolution constructs the runtime evolution system (Genome + Diff +
// Coordinator) when evolution is enabled, and always returns the shared
// evidence store: when disabled, a standalone store keeps the flight recorder's
// fitness evidence flowing without a NewEvolution instance.
//
//nolint:nilnil // nil components + nil error is the documented "disabled" contract.
func wireNewEvolution(enabled bool, dag *engine.MutableDAG, rt *knowledgeruntime.KnowledgeRuntime, memoryStore ares_memory.MemoryConfigStore, evStore evidence.Store) (*NewEvolutionComponents, evidence.Store, error) {
	if !enabled {
		return nil, evidence.NewMemoryStore(), nil
	}
	newEvol, err := ProvideNewEvolution(dag, rt, memoryStore, evStore)
	if err != nil {
		return nil, nil, err
	}
	return newEvol, newEvol.EvidenceStore, nil
}

// wireLegacyEvolution wires the legacy evolution system when it is enabled and
// all required deps are present; otherwise it is skipped (nil), preserving
// prior behavior. Gated by cfg.Evolution.Enabled (F02) so the legacy scheduler
// cannot start behind the config's back.
//
//nolint:nilnil // nil components + nil error is the documented "disabled" contract.
func wireLegacyEvolution(ctx context.Context, cfg *ares_config.Config, deps *BootstrapDeps, comp *Components) (*EvolutionComponents, error) {
	if !cfg.Evolution.Enabled || deps.EventStore == nil || deps.ExpRepo == nil {
		return nil, nil
	}
	return ProvideEvolution(ctx, &cfg.Evolution,
		comp.EventStore, deps.ExpRepo,
		comp.LLM.CallbackReg,
		deps.LLMClient,
		comp.FlightRecorder,
	)
}
