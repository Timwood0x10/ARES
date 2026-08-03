// Package ares_bootstrap orchestrates component assembly.
package ares_bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	ares_memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/storage"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
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
	wg             sync.WaitGroup
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

	// 3. Memory
	// Build the memory config from defaults, then propagate RAG settings from
	// the YAML config so the closed-loop (compression + AKG + memory distill)
	// activates when the operator opts in via memory.enable_rag. RAGTopK /
	// RAGMinScore keep their DefaultMemoryConfig values when the YAML leaves
	// them zero, satisfying validate()'s positive-RAGTopK invariant.
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
		runCleanups()
		return nil, err
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

	// 8. New Evolution — runtime-evolution system (Genome + Diff + Coordinator)
	// Always created; uses a minimal MutableDAG so workflow/scheduler/recovery
	// genomes have something to evolve (not an empty graph).
	//
	// Closure fix (Step 2): pass the LIVE memory manager (comp.Memory) so
	// evolution patches mutate the real agent's config, not an isolated
	// Minimal copy. comp.Memory is a *memoryManager which implements
	// MemoryConfigStore (GetConfig/Lock/Unlock).
	dagSteps := []*engine.Step{
		{ID: "input", Name: "Input", AgentType: "parser", Input: "parse input"},
		{ID: dagStepProcess, Name: "Process", AgentType: "processor", Input: dagStepProcess, DependsOn: []string{"input"}},
		{ID: "output", Name: "Output", AgentType: "formatter", Input: "format", DependsOn: []string{dagStepProcess}},
	}
	dag, dagErr := engine.NewMutableDAG(dagSteps)
	if dagErr != nil {
		runCleanups()
		return nil, fmt.Errorf("create mutable dag: %w", dagErr)
	}

	// Type-assert comp.Memory to MemoryConfigStore. Both *memoryManager and
	// *ProductionMemoryManager implement MemoryConfigStore. If the assertion
	// fails (should not happen), fall back to the minimal manager.
	var liveMemoryStore ares_memory.MemoryConfigStore
	if store, ok := comp.Memory.(ares_memory.MemoryConfigStore); ok {
		liveMemoryStore = store
	} else {
		// Defensive fallback — preserves prior behavior if a future
		// custom MemoryManager does not implement MemoryConfigStore.
		liveMemoryStore = buildMemoryManager()
	}

	// Create the KnowledgeRuntime once and share it between the evolution
	// system and the agent's AKF tools so knowledge genome patches affect
	// the actual runtime used by the agent's knowledge tools. The vector
	// provider is registered when postgres vector storage + embedding are
	// wired (comp.VectorStore / embClient); otherwise the runtime uses only
	// the memory/code providers.
	knowRt := BuildKnowledgeRuntime(comp.VectorStore, embClient, knowStore)
	comp.KnowledgeRuntime = knowRt

	newEvol, err := ProvideNewEvolution(dag, knowRt, liveMemoryStore)
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.NewEvolution = newEvol

	// Single shared flight recorder — created and started here, independent
	// of the legacy evolution deps (ExpRepo). Its collector subscribes to
	// comp.EventStore and emits workflow/scheduler/recovery fitness evidence
	// into newEvol.EvidenceStore (the same store the GA genomes read), so the
	// fitness write loop works on every production path (ares serve / ares
	// start) even when ProvideEvolution is skipped. ProvideEvolution and the
	// api_impl launcher reuse this instance instead of building their own.
	if comp.EventStore != nil {
		comp.FlightRecorder = flight.NewFlightRecorder(flight.FlightRecorderConfig{
			EventStore:    comp.EventStore,
			EvidenceStore: newEvol.EvidenceStore,
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
	// constructing a second recorder.
	if deps.EventStore != nil && deps.ExpRepo != nil {
		evol, err := ProvideEvolution(ctx, &cfg.Evolution,
			comp.EventStore, deps.ExpRepo,
			comp.LLM.CallbackReg,
			deps.LLMClient,
			comp.FlightRecorder,
		)
		if err != nil {
			runCleanups()
			return nil, err
		}
		comp.Evolution = evol
	}

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
	wireRetrievers(ctx, cfg, comp.Memory, embClient, deps.ExpRepo, knowRt, knowStore, newEvol.EvidenceStore)

	// Track C (C-Safe): wire the DeploymentPipeline into the Coordinator so
	// generated patches are safely promoted to the live runtime. Gated by
	// cfg.Evolution.Deployment.Enabled — when disabled, the Coordinator falls
	// back to applying patches directly (pre-deployment behavior). The live
	// runtime is the real executor registry, so memory patches are written to
	// the live comp.Memory; workflow/scheduler/recovery/knowledge patches hit
	// their (still synthetic) executors — closing those requires a live DAG
	// supply chain (Track C-Risky, deferred).
	if cfg.Evolution.Deployment.Enabled {
		dp := deployment.NewDeploymentPipeline(
			cfg.Evolution.Deployment,
			&deploymentStagingRuntime{reg: newEvol.PatchReg},
			&deploymentLiveRuntime{reg: newEvol.PatchReg},
		)
		newEvol.Coordinator.SetDeployer(&deploymentAdapter{dp: dp})
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
	if err := wireGAEvolution(ctx, cfg, &comp, newEvol, guidanceProvider); err != nil {
		runCleanups()
		return nil, err
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

	return &comp, nil
}
