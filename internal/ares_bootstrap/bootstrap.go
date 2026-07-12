// Package ares_bootstrap orchestrates component assembly.
package ares_bootstrap

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	ares_memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
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
	wg           sync.WaitGroup
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
	newEvol, err := ProvideNewEvolution(dag, buildKnowledgeRuntime(), buildMemoryManager())
	if err != nil {
		runCleanups()
		return nil, err
	}
	comp.NewEvolution = newEvol

	// 9. Wire the GA population adapter so evolution actually runs and its
	// best strategy is deployed to the runtime. The GA engine is built
	// independently of the old evolution system: in the default configuration
	// (no EventStore/ExpRepo, so comp.Evolution is nil) it builds its own
	// scheduler driven by the always-present LLM callback registry, so the
	// bridge no longer depends on the old system. When the old system exists,
	// the GA adapter is attached to its scheduler instead (preserving prior
	// behavior). The deployed strategy is persisted to an in-memory store so
	// the live agent can consume it.
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

	wired, wErr := evolution.NewWiredEvolutionSystem(base, gaCfg)
	if wErr != nil {
		runCleanups()
		return nil, fmt.Errorf("wire GA population adapter: %w", wErr)
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
	{
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
	}

	return &comp, nil
}
