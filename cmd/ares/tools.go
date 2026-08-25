package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	"github.com/Timwood0x10/ares/internal/tools/discovery"
	"github.com/Timwood0x10/ares/internal/tools/envcap"
	"github.com/Timwood0x10/ares/internal/tools/planner"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// nativeToolsEnvVar names the comma-separated allowlist of host commands to
// discover and register as tools (primitive 7: native command discovery).
// Empty disables discovery so hosts without the commands degrade gracefully.
const nativeToolsEnvVar = "ARES_NATIVE_TOOLS"

// nativeToolsAllowlist parses the ARES_NATIVE_TOOLS env var into a cleaned
// allowlist of host command names. Returns an empty slice when unset/blank so
// callers disable native discovery gracefully. This is the single security
// boundary: only listed commands are ever probed or executed.
func nativeToolsAllowlist() []string {
	raw := strings.TrimSpace(os.Getenv(nativeToolsEnvVar))
	if raw == "" {
		return nil
	}
	allowlist := make([]string, 0)
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allowlist = append(allowlist, name)
		}
	}
	return allowlist
}

// registerNativeTools probes the allowlisted host commands via `command -v` +
// `--help` and registers the ones present into the internal registry. Only
// commands explicitly listed in ARES_NATIVE_TOOLS are ever probed or executed
// (allowlist security boundary); non-existent commands are skipped.
func registerNativeTools(ctx context.Context, internalReg *core.Registry) error {
	allowlist := nativeToolsAllowlist()
	if len(allowlist) == 0 {
		return nil
	}

	d := discovery.NewDiscoverer(allowlist)
	tools, err := d.Discover(ctx)
	if err != nil {
		return fmt.Errorf("native tools: discover: %w", err)
	}
	registered := 0
	for _, t := range tools {
		if err := internalReg.Register(t); err != nil {
			fmt.Printf("native tool: failed to register %q: %v\n", t.Name(), err)
			continue
		}
		registered++
	}
	fmt.Printf("native tools registered: %d (allowlist: %v)\n", registered, allowlist)
	return nil
}

// newToolRegistry creates the public tool registry with built-in + custom tools.
// The file tool is sandboxed to ARES_WORKSPACE_DIR (or the current working
// directory if the env var is unset) to prevent path-traversal attacks.
func newToolRegistry() (*api_tools.Registry, error) {
	r := api_tools.NewRegistry()
	workspaceDir := os.Getenv("ARES_WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir, _ = os.Getwd()
	}
	if err := api_tools.RegisterBuiltinTools(r, api_tools.WithFileSandboxDir(workspaceDir)); err != nil {
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

// registerCapabilitySearch wires the environment-capability searcher (envcap)
// as the `search_capabilities` tool and registers it into the internal
// registry (REVIEW #11: envcap.NewSearcher was constructed nowhere in serve).
// This completes the SKILLS progressive-disclosure story: the memory manager
// surfaces a resident skill block, and this tool lets the agent actively search
// across the environment's capabilities, returning name + one-line description
// with details loaded on demand.
//
// Two sources are wired: registered tools (the registry itself) and skills (the
// bootstrap-seeded registry). Native commands are deliberately NOT wired as a
// separate discovery source here because registerNativeTools has already
// registered each allowlisted command as a CommandTool in the same registry —
// so they surface through the registry (KindTool). Wiring a second Discoverer
// would double-list every command and re-probe the host (command -v + --help)
// on every search call.
//
// skillReg may be nil (skills disabled) — the searcher simply skips that source.
func registerCapabilitySearch(internalReg *core.Registry, skillReg *skills.Registry) error {
	searcher := envcap.NewSearcher(envcap.NewRegistryLister(internalReg), skillReg, nil)
	tool := envcap.NewSearchTool(searcher)
	if err := internalReg.Register(tool); err != nil {
		return fmt.Errorf("register capability search tool: %w", err)
	}
	return nil
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
