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
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	ares_memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
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
	// KnowledgeStore is the shared AKG knowledge sink. The opt-in Conversation
	// Compiler writes its projected KnowledgeObjects here so that other AKG
	// consumers (prompt injection, future runtime ingestion) read from a
	// single shared pool instead of an isolated per-build store. It is nil
	// when the compiler is disabled, preserving prior behavior.
	KnowledgeStore knowledge.KnowledgeStore
	// KnowledgeRetriever drives A2: leader-agent auto-retrieval from the
	// shared AKG pool. It is nil unless the Conversation Compiler is enabled
	// (wireKnowledgeCompiler), so the read path stays coupled to the compiler
	// write path — disabling the compiler leaves both off, preserving prior
	// behavior. The leader agent treats a nil retriever as "no enrichment".
	KnowledgeRetriever *retriever.Retriever
	// KnowledgeCompiler is the opt-in Conversation Compiler pipeline (design:
	// CONVERSATION_COMPILER.md). It is nil when cfg.KnowledgeCompiler.Enabled is
	// false (the default), preserving prior behavior. The pipeline is zero-LLM
	// and deeply binds AKG extraction + distillation classifier/scorer +
	// knowledge graph projection.
	KnowledgeCompiler *KnowledgeCompilerComponents
	wg                sync.WaitGroup
}

// KnowledgeCompilerComponents holds the assembled Conversation Compiler
// pipeline. Pipeline is the stateless one-shot orchestrator (Compile ->
// AKG project -> Distill-and-prune -> Render prompt -> Emit memory).
// Lifecycle is the stateful trigger layer that drives incremental compiles
// from a token-budget window. Both share the same Compiler (extractor +
// normalizer) and KMDistiller.
type KnowledgeCompilerComponents struct {
	Pipeline  *compiler.Pipeline
	Lifecycle *compiler.ContextLifecycle

	// AKGMetrics is the shared L3 quality-gate observability collector for the
	// compiler pipeline (nil when metrics were not configured). The serve /
	// metrics layer can read Snapshot() from it to expose what the AKG gate
	// dropped, and the 1.3 evaluation harness uses it for baseline reports.
	AKGMetrics *compiler.AKGMetrics
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
	mem, err := ProvideMemory(nil)
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
	guidanceProvider := wireDistillation(ctx, cfg, &comp, deps, &cleanups)
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

	// 7. Evolution — only if all required deps are wired
	if deps.EventStore != nil && deps.ExpRepo != nil {
		evol, err := ProvideEvolution(ctx, &cfg.Evolution,
			comp.EventStore, deps.ExpRepo,
			comp.LLM.CallbackReg,
			deps.LLMClient,
		)
		if err != nil {
			runCleanups()
			return nil, err
		}
		comp.Evolution = evol
	}

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
	// the actual runtime used by the agent's knowledge tools.
	knowRt := BuildKnowledgeRuntime()
	comp.KnowledgeRuntime = knowRt

	newEvol, err := ProvideNewEvolution(dag, knowRt, liveMemoryStore)
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.NewEvolution = newEvol

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

	// 11. Optional Conversation Compiler pipeline (opt-in via
	// cfg.KnowledgeCompiler.Enabled). When disabled, this is a no-op.
	// Failures are non-fatal: the pipeline is logged and skipped, preserving
	// prior behavior (graceful degradation, mirroring wireDistillation).
	wireKnowledgeCompiler(ctx, cfg, &comp, &cleanups)

	// 11b. Phase 3 — inject the compiler lifecycle into the live memory manager
	// so BuildPromptMessages can append the compressed context block. The
	// MemoryManager interface is intentionally unchanged (6 impls/mocks would
	// otherwise need edits); instead we probe for the injectable capability via
	// a local interface so non-injectable managers degrade gracefully.
	if comp.KnowledgeCompiler != nil && comp.KnowledgeCompiler.Lifecycle != nil {
		type knowledgeCompilerInjectable interface {
			SetKnowledgeCompiler(cl *compiler.ContextLifecycle)
		}
		if inj, ok := comp.Memory.(knowledgeCompilerInjectable); ok {
			inj.SetKnowledgeCompiler(comp.KnowledgeCompiler.Lifecycle)
		}
	}

	return &comp, nil
}
