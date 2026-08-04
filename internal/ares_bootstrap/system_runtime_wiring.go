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
// to the System Runtime Component interface. Identity and dependency metadata
// drive registry ordering; optional stop/wait hooks let the orchestrator's
// Shutdown drive real teardown in reverse topological order (Stage 9) instead
// of leaving teardown only to entry-point shutdown managers. Nil hooks are
// safe no-ops, so components without a dedicated teardown still transition.
type runtimeComponentAdapter struct {
	name   string
	deps   []string
	stopFn func(ctx context.Context) error
	waitFn func() error
}

// Name returns the stable component identifier.
func (a *runtimeComponentAdapter) Name() string { return a.name }

// Dependencies returns the names of components that must be Ready first.
func (a *runtimeComponentAdapter) Dependencies() []string { return a.deps }

// Stop delegates to the optional teardown hook; nil hook is a no-op.
func (a *runtimeComponentAdapter) Stop(ctx context.Context) error {
	if a.stopFn == nil {
		return nil
	}
	return a.stopFn(ctx)
}

// Wait delegates to the optional wait hook; nil hook is a no-op.
func (a *runtimeComponentAdapter) Wait() error {
	if a.waitFn == nil {
		return nil
	}
	return a.waitFn()
}

// registerSystemComponent registers one component when it was actually
// constructed (present == true), attaching optional teardown hooks so the
// orchestrator's Shutdown drives real Stop/Wait in reverse topological order.
// Registration failures are logged, never fatal: the registry is observational
// and a metadata problem must not block Bootstrap on an otherwise healthy
// assembly.
func registerSystemComponent(reg *system_runtime.Registry, name string, present bool, deps []string, stopFn func(ctx context.Context) error, waitFn func() error) {
	if !present {
		return
	}
	adapter := &runtimeComponentAdapter{name: name, deps: deps, stopFn: stopFn, waitFn: waitFn}
	if err := reg.Register(adapter, system_runtime.ModeRequired); err != nil {
		log.Warn("system_runtime: component registration skipped",
			"component", name, "error", err)
	}
}

// wireSystemRuntime registers every constructed component with the System
// Runtime registry and creates the orchestrator that observes their states.
// It runs after construction completes so the full component graph is known.
// Teardown hooks (Stage 9) let Orchestrator.Shutdown own real Stop/Wait in
// reverse topological order, so entry points no longer duplicate teardown.
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

	registerSystemComponent(reg, sysCompEventStore, comp.EventStore != nil, nil, nil, nil)
	registerSystemComponent(reg, sysCompRuntime, comp.Runtime != nil, []string{sysCompEventStore},
		func(ctx context.Context) error { return comp.Runtime.Stop() }, nil)
	registerSystemComponent(reg, sysCompMemory, comp.Memory != nil, []string{sysCompEventStore},
		func(ctx context.Context) error { return comp.Memory.Stop(ctx) }, nil)
	registerSystemComponent(reg, sysCompMCP, comp.MCP != nil, nil,
		func(ctx context.Context) error { return comp.MCP.Stop(ctx) }, nil)
	registerSystemComponent(reg, sysCompLLM, comp.LLM != nil, nil, nil, nil)
	registerSystemComponent(reg, sysCompDashboard, comp.Dashboard != nil, []string{sysCompMCP},
		func(ctx context.Context) error { return comp.Dashboard.Stop(ctx) }, nil)
	registerSystemComponent(reg, sysCompEvidenceStore, comp.EvidenceStore != nil, nil, nil, nil)
	registerSystemComponent(reg, sysCompFlightRecorder, comp.FlightRecorder != nil, []string{sysCompEventStore, sysCompEvidenceStore},
		func(ctx context.Context) error { comp.FlightRecorder.Stop(); return nil }, nil)
	registerSystemComponent(reg, sysCompKnowledge, comp.KnowledgeRuntime != nil, nil, nil, nil)
	registerSystemComponent(reg, sysCompNewEvolution, comp.NewEvolution != nil, []string{sysCompEvidenceStore}, nil, nil)
	registerSystemComponent(reg, sysCompDiscovery, comp.Discovery != nil, nil, nil, nil)

	orch := system_runtime.NewOrchestrator(reg, ctx)
	if err := orch.Start(ctx); err != nil {
		return orch, reg, fmt.Errorf("system_runtime: observe startup: %w", err)
	}
	return orch, reg, nil
}
