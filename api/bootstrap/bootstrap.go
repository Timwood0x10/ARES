// Package bootstrap provides factory functions for creating ARES modules.
// It delegates component wiring to internal/ares_bootstrap and extends with
// api/service/* wrappers to avoid direct internal imports.
package bootstrap

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Timwood0x10/ares/api/core"
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
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution"
	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/diff"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
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

	// RuntimeEvolution is the new genome/diff/coordinator/patch system.
	RuntimeEvolution core.RuntimeEvolution
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
		// NOTE: wiring real MCP/LLM executors requires interface adaptation from
		// comp.MCP / comp.LLM — skipped for now to avoid nil-dependent panic.
		// TODO: wire dashboard with actual MCP/LLM executors (expected by 2026-09-30).
		log.Println("bootstrap: dashboard enabled but MCP/LLM executors not wired — skipping")
	}

	var flightRec *flightsvc.Recorder
	if cfg.Flight != nil {
		flightRec = flightsvc.New(comp.EventStore)
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
		RuntimeEvolution: &runtimeEvoService{
			components: comp.NewEvolution,
		},
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
	if a.Evolution != nil {
		a.Evolution.Shutdown()
	}
	if a.Dashboard != nil {
		a.Dashboard.Stop()
	}
	if a.Memory != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		if err := a.Memory.Stop(stopCtx); err != nil {
			log.Printf("bootstrap: memory stop: %v", err)
		}
	}
	if a.MCP != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		if err := a.MCP.Stop(stopCtx); err != nil {
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

// runtimeEvoService implements core.RuntimeEvolution by wrapping
// the bootstrap's NewEvolutionComponents.
type runtimeEvoService struct {
	components *ares_bootstrap.NewEvolutionComponents
}

func (s *runtimeEvoService) RunCycle(ctx context.Context) (*core.RuntimeCycleResult, error) {
	return runEvolutionCycle(ctx, s.components)
}

func (s *runtimeEvoService) Status() (*core.RuntimeEvolutionStatus, error) {
	return getEvolutionStatus(s.components)
}

func (s *runtimeEvoService) Propose(ctx context.Context, proposal core.RuntimeProposal) error {
	return submitProposal(ctx, proposal, s.components)
}

func (s *runtimeEvoService) QueryEvidence(ctx context.Context, filter core.EvidenceFilter) ([]core.Evidence, error) {
	return queryEvidence(ctx, s.components, filter)
}

func (s *runtimeEvoService) RegisterComponent(ctx context.Context, comp core.RuntimeComponent) error {
	return registerComponent(ctx, s.components, comp)
}

// runEvolutionCycle runs one iteration of Mutate → Diff → Submit → Evaluate.
func runEvolutionCycle(ctx context.Context, c *ares_bootstrap.NewEvolutionComponents) (*core.RuntimeCycleResult, error) {
	snapshots := make(map[string]diff.SnapshotPair)
	var failures []string
	for _, name := range c.GenomeReg.List() {
		gm, err := c.GenomeReg.Get(name)
		if err != nil {
			continue
		}
		snap, err := gm.Snapshot(ctx)
		if err != nil {
			failures = append(failures, name+": snapshot failed")
			continue
		}
		snapshots[name] = diff.SnapshotPair{Old: snap}
	}

	var changes []core.GenomeChange
	totalProposed := 0

	for _, name := range c.GenomeReg.List() {
		gm, err := c.GenomeReg.Get(name)
		if err != nil {
			continue
		}
		children, err := gm.Mutate(ctx, 3)
		if err != nil || len(children) == 0 {
			continue
		}
		best := children[0]
		newSnap, err := best.Snapshot(ctx)
		if err != nil {
			failures = append(failures, name+": child snapshot failed")
			continue
		}
		pair := snapshots[name]
		pair.New = newSnap
		patches, err := c.DiffReg.DiffAll(ctx, map[string]diff.SnapshotPair{name: pair})
		if err != nil {
			failures = append(failures, name+": diff failed")
			continue
		}
		if len(patches) > 0 {
			gc := core.GenomeChange{Name: name, Patches: len(patches), FirstType: patches[0].Type.String()}
			changes = append(changes, gc)
			totalProposed += len(patches)
			for _, p := range patches {
				c.Coordinator.Submit(coordinator.PatchProposal{
					Patch: p, Source: coordinator.SourceGA,
					Reason: "evolution cycle", Priority: 5, Timestamp: time.Now(),
				})
			}
		}
	}

	c.Coordinator.Evaluate(ctx)
	history := c.Coordinator.PatchHistory()
	applied := 0
	for _, r := range history {
		if r.Error == nil {
			applied++
		}
	}

	return &core.RuntimeCycleResult{
		GenomesEvaluated: len(c.GenomeReg.List()),
		GenomesChanged:   len(changes),
		PatchesProposed:  totalProposed,
		PatchesApplied:   applied,
		Failures:         failures,
		Details:          changes,
	}, nil
}

func getEvolutionStatus(c *ares_bootstrap.NewEvolutionComponents) (*core.RuntimeEvolutionStatus, error) {
	evs, _ := c.EvidenceStore.Query(context.Background(), evidence.Filter{Limit: 1000})
	return &core.RuntimeEvolutionStatus{
		Genomes:          c.GenomeReg.List(),
		Differs:          c.DiffReg.List(),
		PendingProposals: c.Coordinator.PendingCount(),
		DecisionsMade:    len(c.Coordinator.DecisionHistory()),
		PatchesApplied:   len(c.Coordinator.PatchHistory()),
		EvidenceEntries:  len(evs),
	}, nil
}

func submitProposal(ctx context.Context, p core.RuntimeProposal, c *ares_bootstrap.NewEvolutionComponents) error {
	adapter := evolution.NewLLMAdapter()
	results, err := adapter.Parse(ctx, p.Text)
	if err != nil {
		return fmt.Errorf("bootstrap: parse proposal: %w", err)
	}
	for _, r := range results {
		prop := r.Proposal
		if p.Priority > 0 {
			prop.Priority = p.Priority
		}
		c.Coordinator.Submit(prop)
	}
	c.Coordinator.Evaluate(ctx)
	return nil
}

func queryEvidence(ctx context.Context, c *ares_bootstrap.NewEvolutionComponents, filter core.EvidenceFilter) ([]core.Evidence, error) {
	internalFilter := evidence.Filter{
		Source: filter.Source,
		Kind:   evidence.EvidenceKind(filter.Kind),
		Since:  filter.Since,
		Until:  filter.Until,
		Limit:  filter.Limit,
	}
	results, err := c.EvidenceStore.Query(ctx, internalFilter)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: query evidence: %w", err)
	}

	out := make([]core.Evidence, len(results))
	for i, e := range results {
		out[i] = core.Evidence{
			ID:        e.ID,
			Source:    e.Source,
			Kind:      core.EvidenceKind(e.Kind),
			Payload:   e.Payload,
			Metadata:  e.Metadata,
			Timestamp: e.Timestamp,
		}
	}
	return out, nil
}

func registerComponent(ctx context.Context, c *ares_bootstrap.NewEvolutionComponents, comp core.RuntimeComponent) error {
	adapter := &componentExecutorAdapter{comp: comp}
	return c.PatchReg.RegisterComponent(adapter)
}

// componentExecutorAdapter adapts a core.RuntimeComponent to patch.RuntimeComponent.
type componentExecutorAdapter struct {
	comp core.RuntimeComponent
}

func (a *componentExecutorAdapter) Name() string { return a.comp.Name() }

func (a *componentExecutorAdapter) Snapshot(ctx context.Context) (any, error) {
	return a.comp.Snapshot(ctx)
}

func (a *componentExecutorAdapter) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	coreResult, err := a.comp.Apply(ctx, core.RuntimePatch{
		Type:   core.PatchType(p.Type),
		Target: p.Target,
		Value:  p.Value,
		Reason: p.Reason,
		Source: p.Source,
	})
	if err != nil {
		return nil, err
	}
	if coreResult == nil {
		return nil, fmt.Errorf("no patch result from component %q", p.Target)
	}
	return &patch.RuntimePatch{
		Type:   patch.PatchType(coreResult.Type),
		Target: coreResult.Target,
		Value:  coreResult.Value,
		Reason: coreResult.Reason,
		Source: coreResult.Source,
	}, nil
}

func (a *componentExecutorAdapter) CanApply(ctx context.Context, p patch.RuntimePatch) error {
	return a.comp.CanApply(ctx, core.RuntimePatch{
		Type:   core.PatchType(p.Type),
		Target: p.Target,
		Value:  p.Value,
	})
}

// Ensure componentExecutorAdapter implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*componentExecutorAdapter)(nil)
