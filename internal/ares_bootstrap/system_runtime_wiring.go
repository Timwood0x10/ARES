// Package ares_bootstrap — System Runtime wiring (Stage 1).
//
// This file bridges the system-level control plane (internal/system_runtime)
// into the Bootstrap assembly: after all components are constructed, they are
// registered with the System Runtime registry so entry points (serve, start,
// SDK) observe a uniform component graph, lifecycle state, and readiness
// snapshot. Registration is observational: Bootstrap keeps owning construction
// and startup; the orchestrator records component states and provides the
// shared root context and status snapshot API.
package ares_bootstrap

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/system_runtime"
)

// System Runtime component names — stable identifiers used by the registry.
const (
	sysCompEventStore     = "eventstore"
	sysCompRuntime        = "runtime"
	sysCompMemory         = "memory"
	sysCompMCP            = "mcp"
	sysCompLLM            = "llm"
	sysCompDashboard      = "dashboard"
	sysCompEvidenceStore  = "evidence"
	sysCompFlightRecorder = "flight"
	sysCompKnowledge      = "knowledge"
	sysCompNewEvolution   = "newevolution"
	sysCompDiscovery      = "discovery"
)

// runtimeComponentAdapter adapts an already-constructed Bootstrap component
// to the System Runtime Component interface for registry and observability.
// It intentionally does not implement Binder/Starter/ReadinessChecker/Stopper:
// Bootstrap owns construction and startup; the adapter exposes only identity
// and dependency metadata so the registry can order and report them.
type runtimeComponentAdapter struct {
	name string
	deps []string
}

// Name returns the stable component identifier.
func (a *runtimeComponentAdapter) Name() string { return a.name }

// Dependencies returns the names of components that must be Ready first.
func (a *runtimeComponentAdapter) Dependencies() []string { return a.deps }

// registerSystemComponent registers one component when it was actually
// constructed (present == true). Registration failures are logged, never
// fatal: the registry is observational and a metadata problem must not block
// Bootstrap on an otherwise healthy assembly.
func registerSystemComponent(reg *system_runtime.Registry, name string, present bool, deps []string) {
	if !present {
		return
	}
	adapter := &runtimeComponentAdapter{name: name, deps: deps}
	if err := reg.Register(adapter, system_runtime.ModeRequired); err != nil {
		log.Warn("system_runtime: component registration skipped",
			"component", name, "error", err)
	}
}

// wireSystemRuntime registers every constructed component with the System
// Runtime registry and creates the orchestrator that observes their states.
// It runs after construction completes so the full component graph is known.
//
// Args:
// ctx - bootstrap context used as the orchestrator's root context.
// cfg - resolved configuration (used for future per-component mode mapping).
// comp - the fully assembled Components instance.
//
// Returns:
// orch - the System Runtime orchestrator, or nil on error.
// reg - the backing registry (same instance the orchestrator observes).
// err - error when the orchestrator fails to observe startup.
func wireSystemRuntime(ctx context.Context, cfg *ares_config.Config, comp *Components) (*system_runtime.Orchestrator, *system_runtime.Registry, error) {
	reg := system_runtime.NewRegistry()

	registerSystemComponent(reg, sysCompEventStore, comp.EventStore != nil, nil)
	registerSystemComponent(reg, sysCompRuntime, comp.Runtime != nil, []string{sysCompEventStore})
	registerSystemComponent(reg, sysCompMemory, comp.Memory != nil, []string{sysCompEventStore})
	registerSystemComponent(reg, sysCompMCP, comp.MCP != nil, nil)
	registerSystemComponent(reg, sysCompLLM, comp.LLM != nil, nil)
	registerSystemComponent(reg, sysCompDashboard, comp.Dashboard != nil, []string{sysCompMCP})
	registerSystemComponent(reg, sysCompEvidenceStore, comp.EvidenceStore != nil, nil)
	registerSystemComponent(reg, sysCompFlightRecorder, comp.FlightRecorder != nil, []string{sysCompEventStore, sysCompEvidenceStore})
	registerSystemComponent(reg, sysCompKnowledge, comp.KnowledgeRuntime != nil, nil)
	registerSystemComponent(reg, sysCompNewEvolution, comp.NewEvolution != nil, []string{sysCompEvidenceStore})
	registerSystemComponent(reg, sysCompDiscovery, comp.Discovery != nil, nil)

	orch := system_runtime.NewOrchestrator(reg, ctx)
	if err := orch.Start(ctx); err != nil {
		return orch, reg, fmt.Errorf("system_runtime: observe startup: %w", err)
	}
	return orch, reg, nil
}
