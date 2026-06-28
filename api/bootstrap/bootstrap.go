// Package bootstrap provides factory functions for creating ARES modules.
// It wires internal implementations to the abstract interfaces defined in api/core/.
package bootstrap

import (
	"context"
	"fmt"

	arena "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	mcp "github.com/Timwood0x10/ares/internal/ares_mcp"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
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
func New(ctx context.Context, cfg *Config) (*ARES, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	eventStore := ares_events.NewMemoryEventStore()
	rt := ares_runtime.New(cfg.Runtime, eventStore, nil)

	memMgr, err := memory.NewMemoryManager(cfg.Memory)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: create memory manager: %w", err)
	}

	var evoSvc *evolution.Service
	if cfg.Evolution != nil {
		evoSvc, err = evolution.NewService(cfg.Evolution)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create evolution service: %w", err)
		}
	}

	arenaSvc := arena.NewService(cfg.ArenaInjector, eventStore)

	// MCP manager (optional).
	var mcpMgr *mcp.MCPManager
	if cfg.MCP != nil {
		mcpMgr, err = mcp.NewMCPManager(cfg.MCP, nil)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create MCP manager: %w", err)
		}
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
		Runtime:    rt,
		Memory:     memMgr,
		Evolution:  evoSvc,
		Arena:      arenaSvc,
		MCP:        mcpMgr,
		Dashboard:  dashOrch,
		Flight:     flightRec,
		EventStore: eventStore,
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

// ExecuteArenaAction executes a chaos engineering action.
func (a *ARES) ExecuteArenaAction(ctx context.Context, action arena.Action) arena.Result {
	if a.Arena == nil {
		return arena.Result{Success: false, Error: "arena not initialized"}
	}
	return a.Arena.Execute(ctx, action)
}
