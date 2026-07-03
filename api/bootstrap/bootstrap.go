// Package bootstrap provides factory functions for creating ARES modules.
// It delegates component wiring to internal/ares_bootstrap and extends with
// api-specific components (Arena, Dashboard, Flight).
package bootstrap

import (
	"context"
	"fmt"
	"os"

	arena "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	mcp "github.com/Timwood0x10/ares/internal/ares_mcp"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	ares_runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// ARES is the top-level container for all ARES modules.
type ARES struct {
	// Runtime manages agent lifecycles.
	Runtime *ares_runtime.Manager
	// Memory provides memory management.
	Memory memory.MemoryManager
	// Evolution provides genetic algorithm evolution.
	Evolution *evolution.Service
	// Arena provides chaos engineering.
	Arena *arena.Service
	// MCP provides MCP client management.
	MCP *mcp.MCPManager
	// Dashboard provides web dashboard.
	Dashboard *dashboard.Orchestrator
	// Flight provides flight recording.
	Flight *flight.FlightRecorder
	// EventStore provides event sourcing.
	EventStore ares_events.EventStore
}

// Config holds the configuration for creating an ARES instance.
type Config struct {
	Runtime       *ares_runtime.Config
	Evolution     *evolution.SystemConfig
	Memory        *memory.MemoryConfig
	ArenaInjector *arena.Injector
	MCP           *mcp.MCPManagerConfig
	Dashboard     *DashboardConfig
	Flight        *flight.FlightRecorderConfig
	// AresConfig is the full ares_config.Config for use with internal/ares_bootstrap.
	// When set, the individual config fields above are ignored for MCP/LLM/Dashboard/Evolution.
	AresConfig *ares_config.Config
}

// DashboardConfig holds dashboard configuration.
type DashboardConfig struct {
	// Enabled enables the dashboard.
	Enabled bool
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Runtime:   ares_runtime.DefaultConfig(),
		Evolution: &evolution.SystemConfig{PopulationSize: 20, EliteCount: 3, MutationRate: 0.3},
		Memory:    memory.DefaultMemoryConfig(),
		Dashboard: &DashboardConfig{Enabled: false},
	}
}

// New creates a new ARES instance with all modules wired together.
// It uses internal/ares_bootstrap.Bootstrap() as the single wiring hub
// for MCP, Runtime, Memory, and EventStore, then extends with api-specific
// components (Arena, Dashboard, Flight).
func New(ctx context.Context, cfg *Config) (*ARES, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Use internal/ares_bootstrap for the core infrastructure.
	// If AresConfig is provided, use it; otherwise build a minimal one
	// from the individual config fields.
	var aresCfg *ares_config.Config
	if cfg.AresConfig != nil {
		aresCfg = cfg.AresConfig
	} else {
		aresCfg = &ares_config.Config{
			MCP: ares_config.MCPConfig{
				Servers: make([]ares_config.MCPServerEntry, 0),
			},
			LLM: ares_config.LLMConfig{
				Provider: "ollama",
				Model:    "llama3.2",
			},
		}
	}

	comp, err := ares_bootstrap.Bootstrap(ctx, aresCfg, &ares_bootstrap.BootstrapDeps{
		EventStore: ares_events.NewMemoryEventStore(),
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: core infrastructure: %w", err)
	}

	// Memory — use cfg.Memory if provided (overrides default from Bootstrap).
	var memMgr memory.MemoryManager
	if cfg.Memory != nil {
		memMgr, err = memory.NewMemoryManager(cfg.Memory)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create memory manager: %w", err)
		}
	} else {
		memMgr = comp.Memory
	}

	// Evolution service (optional).
	var evoSvc *evolution.Service
	if cfg.Evolution != nil {
		evoSvc, err = evolution.NewService(cfg.Evolution)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create evolution service: %w", err)
		}
	}

	// Arena (always created, may use nil injector).
	arenaSvc := arena.NewService(cfg.ArenaInjector, comp.EventStore)

	// MCP manager — use cfg.MCP if provided (overrides Bootstrap).
	var mcpMgr *mcp.MCPManager
	if cfg.MCP != nil {
		mcpMgr, err = mcp.NewMCPManager(cfg.MCP, nil)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create MCP manager: %w", err)
		}
	} else {
		mcpMgr = comp.MCP
	}

	// Dashboard orchestrator (optional).
	var dashOrch *dashboard.Orchestrator
	if cfg.Dashboard != nil && cfg.Dashboard.Enabled {
		dashOrch = dashboard.NewOrchestrator(nil, nil)
	}

	// Flight recorder (optional).
	var flightRec *flight.FlightRecorder
	if cfg.Flight != nil {
		flightRec = flight.NewFlightRecorder(*cfg.Flight)
	}

	return &ARES{
		Runtime:    comp.Runtime,
		Memory:     memMgr,
		Evolution:  evoSvc,
		Arena:      arenaSvc,
		MCP:        mcpMgr,
		Dashboard:  dashOrch,
		Flight:     flightRec,
		EventStore: comp.EventStore,
	}, nil
}

// Start starts all modules that need explicit startup.
func (a *ARES) Start(ctx context.Context) error {
	if a.Runtime != nil {
		if err := a.Runtime.Start(ctx); err != nil {
			return fmt.Errorf("bootstrap: start runtime: %w", err)
		}
	}
	return nil
}

// Stop gracefully stops all modules.
func (a *ARES) Stop() error {
	if a.Runtime != nil {
		if err := a.Runtime.Stop(); err != nil {
			return fmt.Errorf("bootstrap: stop runtime: %w", err)
		}
	}
	if a.Evolution != nil {
		a.Evolution.Shutdown()
	}
	if a.MCP != nil {
		_ = a.MCP.Stop(context.Background())
	}
	if a.Flight != nil {
		a.Flight.Stop()
	}
	return nil
}

// RunEvolution runs the evolution for the specified generations.
func (a *ARES) RunEvolution(ctx context.Context, generations int) (*evolution.EvolutionResult, error) {
	if a.Evolution == nil {
		return nil, fmt.Errorf("bootstrap: evolution not initialized")
	}
	return a.Evolution.Evolve(ctx, generations)
}

// RunIdleEvolution runs idle evolution with report generation.
func (a *ARES) RunIdleEvolution(ctx context.Context, generations int) error {
	if a.Evolution == nil {
		return fmt.Errorf("bootstrap: evolution not initialized")
	}
	return a.Evolution.RunIdleEvolution(ctx, generations)
}

// LatestReport reads and returns the content of the latest evolution report file.
func (a *ARES) LatestReport() (string, error) {
	if a.Evolution == nil {
		return "", fmt.Errorf("bootstrap: evolution not initialized")
	}
	path := a.Evolution.ReportPath()
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("bootstrap: read report: %w", err)
	}
	return string(data), nil
}

// ExecuteArenaAction executes a chaos engineering action.
func (a *ARES) ExecuteArenaAction(ctx context.Context, action arena.Action) arena.Result {
	if a.Arena == nil {
		return arena.Result{Success: false, Error: "arena not initialized"}
	}
	return a.Arena.Execute(ctx, action)
}
