// Package ares_bootstrap orchestrates component assembly.
package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// Components holds all assembled system components.
type Components struct {
	MCP       *ares_mcp.MCPManager
	Dashboard *DashboardComponents
	LLM       *LLMComponents
	Evolution *EvolutionComponents
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
func Bootstrap(ctx context.Context, cfg *ares_config.Config, deps *BootstrapDeps) (*Components, error) {
	var comp Components

	if deps == nil {
		deps = &BootstrapDeps{}
	}

	// 1. MCP
	mcp, err := ProvideMCP(ctx, cfg.MCP)
	if err != nil {
		return nil, err
	}
	comp.MCP = mcp

	// 2. LLM — from config (for backward compat) or from deps
	if deps.LLMClient != nil {
		comp.LLM = &LLMComponents{Client: deps.LLMClient}
	} else {
		llm, err := ProvideLLM(cfg.LLM)
		if err != nil {
			return nil, err
		}
		comp.LLM = llm
	}

	// 3. Dashboard
	dash, err := ProvideDashboard(ctx, mcp)
	if err != nil {
		return nil, err
	}
	comp.Dashboard = dash

	// 4. Evolution — only if all required deps are wired
	if deps.EventStore != nil && deps.ExpRepo != nil {
		// Create flight recorder from event store
		flightRecorder := flight.NewFlightRecorder(flight.FlightRecorderConfig{
			EventStore: deps.EventStore,
		})

		evol, err := ProvideEvolution(ctx, &cfg.Evolution,
			flightRecorder, deps.ExpRepo,
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
