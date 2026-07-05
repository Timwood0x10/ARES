// Package bootstrap provides factory functions for creating ARES modules.
// It delegates component wiring to internal/ares_bootstrap and extends with
// api/service/* wrappers to avoid direct internal imports.
package bootstrap

import (
	"context"
	"fmt"
	"log"
	"os"

	arenasvc "github.com/Timwood0x10/ares/api/service/arena"
	dashsvc "github.com/Timwood0x10/ares/api/service/dashboard"
	evosvc "github.com/Timwood0x10/ares/api/service/evolution"
	flightsvc "github.com/Timwood0x10/ares/api/service/flight"
	memsvc "github.com/Timwood0x10/ares/api/service/memory"
	arena "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	ares_mcp "github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// ARES is the top-level container for all ARES modules.
type ARES struct {
	Runtime    *ares_runtime.Manager
	Memory     *memsvc.Service
	Evolution  *evosvc.Service
	Arena      *arenasvc.Service
	MCP        *ares_mcp.MCPManager
	Dashboard  *dashsvc.Dashboard
	Flight     *flightsvc.Recorder
	EventStore ares_events.EventStore
}

// Config holds the configuration for creating an ARES instance.
type Config struct {
	Runtime       *ares_runtime.Config
	Evolution     *evosvc.Config
	Memory        *memsvc.Config
	ArenaInjector *arena.Injector
	MCP           *ares_mcp.MCPManagerConfig
	Dashboard     *DashboardConfig
	Flight        *flightsvc.Config
	AresConfig    *ares_config.Config
}

// DashboardConfig holds dashboard configuration.
type DashboardConfig struct {
	Enabled bool
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Runtime:   ares_runtime.DefaultConfig(),
		Evolution: &evosvc.Config{PopulationSize: 20, EliteCount: 3, MutationRate: 0.3, BaseStrategy: &evosvc.Strategy{ID: "base", Params: map[string]any{"model": "llama3.2"}}},
		Memory:    &memsvc.Config{MaxSessions: 100, MaxHistory: 10, MaxTasks: 500, MaxDistilledTasks: 100, SessionTTL: 24 * 60 * 60, TaskTTL: 7 * 24 * 60 * 60, DistilledTaskTTL: 24 * 60 * 60, VectorDim: 128},
		Dashboard: &DashboardConfig{Enabled: false},
	}
}

// New creates a new ARES instance, delegating core wiring to internal/ares_bootstrap.
func New(ctx context.Context, cfg *Config) (*ARES, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	var aresCfg *ares_config.Config
	if cfg.AresConfig != nil {
		aresCfg = cfg.AresConfig
	} else {
		aresCfg = &ares_config.Config{
			MCP: ares_config.MCPConfig{Servers: make([]ares_config.MCPServerEntry, 0)},
			LLM: ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
		}
	}

	comp, err := ares_bootstrap.Bootstrap(ctx, aresCfg, &ares_bootstrap.BootstrapDeps{
		EventStore: ares_events.NewMemoryEventStore(),
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: core infrastructure: %w", err)
	}

	// Wrap components through api/service/* layer.
	var memSvc *memsvc.Service
	if cfg.Memory != nil {
		memSvc, err = memsvc.New(cfg.Memory)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create memory service: %w", err)
		}
	} else {
		memSvc = &memsvc.Service{}
	}

	var evo *evosvc.Service
	if cfg.Evolution != nil {
		evo, err = evosvc.New(cfg.Evolution)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: create evolution service: %w", err)
		}
	}

	arenaSvc := arenasvc.New(cfg.ArenaInjector, comp.EventStore)

	// MCP manager — always use from Bootstrap to avoid double instances.
	mcpMgr := comp.MCP

	var dash *dashsvc.Dashboard
	if cfg.Dashboard != nil && cfg.Dashboard.Enabled {
		dash = dashsvc.New(nil, nil)
	}

	var flightRec *flightsvc.Recorder
	if cfg.Flight != nil {
		flightRec = flightsvc.New(nil)
	}

	return &ARES{
		Runtime:    comp.Runtime,
		Memory:     memSvc,
		Evolution:  evo,
		Arena:      arenaSvc,
		MCP:        mcpMgr,
		Dashboard:  dash,
		Flight:     flightRec,
		EventStore: comp.EventStore,
	}, nil
}

// Start starts all modules that need explicit startup.
func (a *ARES) Start(ctx context.Context) error {
	if a.Runtime != nil {
		return a.Runtime.Start(ctx)
	}
	return nil
}

// Stop gracefully stops all modules.
func (a *ARES) Stop() error {
	if a.Runtime != nil {
		if err := a.Runtime.Stop(); err != nil {
			log.Printf("bootstrap: runtime stop: %v", err)
		}
	}
	if a.MCP != nil {
		if err := a.MCP.Stop(context.Background()); err != nil {
			log.Printf("bootstrap: mcp stop: %v", err)
		}
	}
	if a.Flight != nil {
		a.Flight.Stop()
	}
	return nil
}

// RunEvolution runs the evolution for the specified generations.
func (a *ARES) RunEvolution(ctx context.Context, generations int) (*evosvc.EvolutionResult, error) {
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
	data, err := os.ReadFile(path) //nolint:gosec // reading evolution report from internal path
	if err != nil {
		return "", fmt.Errorf("bootstrap: read report: %w", err)
	}
	return string(data), nil
}

// ExecuteArenaAction executes a chaos engineering action.
func (a *ARES) ExecuteArenaAction(ctx context.Context, action arenasvc.Action) arenasvc.Result {
	if a.Arena == nil {
		return arenasvc.Result{Success: false, Error: "arena not initialized"}
	}
	return a.Arena.Execute(ctx, action)
}
