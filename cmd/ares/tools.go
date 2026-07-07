package main

import (
	"fmt"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/tools/planner"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// newToolRegistry creates the public tool registry with built-in + custom tools.
func newToolRegistry() (*api_tools.Registry, error) {
	r := api_tools.NewRegistry()
	if err := api_tools.RegisterBuiltinTools(r); err != nil {
		return nil, err
	}
	return r, nil
}

// newToolBinder creates a sub.ToolBinder bridged from the internal core.Registry.
func newToolBinder(internalReg *core.Registry) sub.ToolBinder {
	binder := sub.NewToolBinder()
	binder.BridgeFromRegistry(internalReg)
	return binder
}

// newPlannerBridge wires the capability planner into a ToolExecutionBridge.
// The bridge provides intent-based tool fallback when agents call unknown tools.
// If planner dependencies are missing, it returns nil (no bridge) gracefully.
func newPlannerBridge(internalReg *core.Registry) *planner.ToolExecutionBridge {
	// Create a tool provider from the registry and build the planner.
	provider := planner.NewRegistryProvider(internalReg)
	resolver, err := planner.NewToolResolver(provider)
	if err != nil {
		fmt.Printf("planner: resolver: %v\n", err)
		return nil
	}

	evStore := planner.NewMemoryEvidenceStore()
	p, err := planner.NewPlanner(
		planner.NewRuleBasedAnalyzer(),
		planner.NewCapabilityPlanner(),
		resolver,
		planner.NewEvidenceScorer(evStore),
		planner.NewExecutionPlanner(),
		evStore,
	)
	if err != nil {
		fmt.Printf("planner: new: %v\n", err)
		return nil
	}

	bridge, err := planner.NewToolExecutionBridge(internalReg, p, evStore)
	if err != nil {
		fmt.Printf("planner: bridge: %v\n", err)
		return nil
	}
	return bridge
}
