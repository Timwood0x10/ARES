// Package ares_bootstrap orchestrates component assembly.
package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	ares_memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// Components holds all assembled system components.
type Components struct {
	MCP        *ares_mcp.MCPManager
	Dashboard  *DashboardComponents
	LLM        *LLMComponents
	Evolution  *EvolutionComponents
	Runtime    *ares_runtime.Manager
	Memory     ares_memory.MemoryManager
	EventStore ares_events.EventStore
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
func Bootstrap(ctx context.Context, cfg *ares_config.Config, deps *BootstrapDeps) (*Components, error) {
	var comp Components

	if deps == nil {
		deps = &BootstrapDeps{}
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
		return nil, err
	}
	comp.Runtime = rt

	// 3. Memory
	mem, err := ProvideMemory(nil)
	if err != nil {
		return nil, err
	}
	comp.Memory = mem

	// 4. MCP
	mcp, err := ProvideMCP(ctx, cfg.MCP)
	if err != nil {
		return nil, err
	}
	comp.MCP = mcp

	// 5. LLM — from config (for backward compat) or from deps
	if deps.LLMClient != nil {
		comp.LLM = &LLMComponents{Client: deps.LLMClient}
	} else {
		llm, err := ProvideLLM(cfg.LLM)
		if err != nil {
			return nil, err
		}
		comp.LLM = llm
	}

	// 6. Dashboard
	dash, err := ProvideDashboard(ctx, mcp)
	if err != nil {
		return nil, err
	}
	comp.Dashboard = dash

	// 7. Evolution — only if all required deps are wired
	if deps.EventStore != nil && deps.ExpRepo != nil {
		evol, err := ProvideEvolution(ctx, &cfg.Evolution,
			comp.EventStore, deps.ExpRepo,
			comp.LLM.CallbackReg,
			deps.LLMClient,
		)
		if err != nil {
			return nil, err
		}
		comp.Evolution = evol
	}

	return &comp, nil
}
