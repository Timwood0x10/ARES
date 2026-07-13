// Package tools provides the public API for tool registration and execution.
//
// == Built-in tools (auto-registered) ==
//
// NewRegistry() automatically registers all built-in tools. You do NOT need
// to call RegisterBuiltinTools() manually — tools are ready on creation:
//
//	registry := tools.NewRegistry()
//	result, _ := registry.Execute(ctx, "calculator", map[string]any{"expression": "1+1"})
//
// Built-in tools include: calculator, hash_tool, string_utils, pdf_tool,
// id_generator, regex, json_tools, web_search, file_tools.
//
// == Custom tools ==
//
// You can register additional tools using registry.Register():
//
//	registry.Register(tools.ToolFunc{
//	    ToolName: "my_tool",
//	    ToolDesc: "My custom tool",
//	    Fn: func(ctx context.Context, params map[string]any) (any, error) {
//	        return "result", nil
//	    },
//	})
//
// == Starting from scratch ==
//
// If you want an empty registry without any built-in tools (e.g., for a
// custom environment), use NewEmptyRegistry():
//
//	registry := tools.NewEmptyRegistry()
//	registry.Register(myTool)
package tools

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/tools/planner"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Result represents the outcome of a tool execution.
type Result struct {
	Success bool `json:"success"`
	Data    any  `json:"data,omitempty"`
}

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the tool name.
	Name() string
	// Description returns a human-readable description.
	Description() string
	// Execute runs the tool with the given parameters.
	Execute(ctx context.Context, params map[string]any) (Result, error)
	// Capabilities returns the capabilities this tool provides.
	// Return nil if the tool does not declare capabilities.
	// The planner uses this for dynamic capability discovery.
	Capabilities() []string
}

// ToolFunc is a convenience type for creating tools from functions.
type ToolFunc struct {
	ToolName string
	ToolDesc string
	Fn       func(ctx context.Context, params map[string]any) (any, error)
}

func (f ToolFunc) Name() string           { return f.ToolName }
func (f ToolFunc) Description() string    { return f.ToolDesc }
func (f ToolFunc) Capabilities() []string { return nil }
func (f ToolFunc) Execute(ctx context.Context, params map[string]any) (Result, error) {
	data, err := f.Fn(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: true, Data: data}, nil
}

// Registry manages tool registration and execution.
type Registry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	coreReg *core.Registry // cached bridge to internal registry (lazy)
}

// NewRegistry creates a new Registry pre-populated with all built-in tools.
// External callers get ALL built-in tools automatically — no manual registration needed.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	// Auto-populate with built-in tools on creation.
	_ = RegisterBuiltinTools(r)
	return r
}

// NewEmptyRegistry creates a new empty Registry with no built-in tools.
// Use this if you want to start from scratch and only add custom tools.
func NewEmptyRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry and syncs to the internal cache.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return errors.New("tool is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
	// Sync to cached core.Registry if it exists.
	if r.coreReg != nil {
		if err := r.coreReg.Register(&toolAdapter{tool: tool, name: tool.Name()}); err != nil {
			return err
		}
	}
	return nil
}

// Unregister removes a tool from the registry and syncs to the internal cache.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	if r.coreReg != nil {
		return r.coreReg.Unregister(name)
	}
	return nil
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Execute runs a tool by name.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any) (Result, error) {
	tool, ok := r.Get(name)
	if !ok {
		return Result{}, errors.New("tool not found: " + name)
	}
	return tool.Execute(ctx, params)
}

// List returns all registered tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// ListTools returns all registered tools with their descriptions.
func (r *Registry) ListTools() []ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]ToolInfo, 0, len(r.tools))
	for _, t := range r.tools {
		infos = append(infos, ToolInfo{Name: t.Name(), Description: t.Description()})
	}
	return infos
}

// ListToolNames returns all registered tool names as strings.
// This satisfies the planner.ToolProvider.ListTools() interface for
// direct use with the capability planner:
//
//	resolver, err := planner.NewToolResolver(registry)
func (r *Registry) ListToolNames() []string {
	return r.List()
}

// ToolInfo is a summary of a tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// GetToolCapabilities returns the capabilities declared by a tool.
// This method, together with List(), makes the Registry compatible with
// the planner.ToolProvider interface via PlannerProvider():
//
//	reg := tools.NewRegistry()
//	provider := reg.PlannerProvider()   // returns planner-compatible adapter
//	resolver, _ := planner.NewToolResolver(provider)
func (r *Registry) GetToolCapabilities(name string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return tool.Capabilities(), nil
}

// PlannerProvider returns a tool provider adapter suitable for the
// capability planner. The returned value satisfies planner.ToolProvider
// via structural typing (it has ListTools() and GetToolCapabilities()).
//
// Usage:
//
//	import "github.com/Timwood0x10/ares/internal/tools/planner"
//
//	reg := tools.NewRegistry()
//	resolver, err := planner.NewToolResolver(reg.PlannerProvider())
func (r *Registry) PlannerProvider() *RegistryPlannerProvider {
	return &RegistryPlannerProvider{reg: r}
}

// RegistryPlannerProvider adapts a Registry for use with the capability planner.
// It satisfies planner.ToolProvider via structural typing.
type RegistryPlannerProvider struct {
	reg *Registry
}

// ListTools returns all registered tool names.
func (p *RegistryPlannerProvider) ListTools() []string {
	return p.reg.List()
}

// GetToolCapabilities returns the capabilities declared by a tool.
func (p *RegistryPlannerProvider) GetToolCapabilities(name string) ([]string, error) {
	return p.reg.GetToolCapabilities(name)
}

// ── Planner (public API) ────────────────────────────────────────────────

// Planner is a re-export of planner.Planner for public use.
// See https://pkg.go.dev/github.com/Timwood0x10/ares/internal/tools/planner
type Planner = planner.Planner

// ExecutionPlan is a re-export of planner.ExecutionPlan for public use.
type ExecutionPlan = planner.ExecutionPlan

// Bridge is a re-export of planner.ToolExecutionBridge for public use.
type Bridge = planner.ToolExecutionBridge

// NewPlanner creates a capability planner from a tool Registry.
//
// The planner provides intent-based tool resolution:
//  1. Analyze user request → structured intent
//  2. Decompose intent → capability requirements
//  3. Resolve requirements → candidate tools
//  4. Score candidates by metadata + evidence
//  5. Build execution plan (single-step or multi-step DAG)
//
// Usage:
//
//	reg := tools.NewRegistry()
//	p, err := tools.NewPlanner(reg)
//	if err != nil { ... }
//	plan, err := p.Plan(ctx, "计算1+1")
func NewPlanner(r *Registry) (*Planner, error) {
	provider := r.PlannerProvider()
	resolver, err := planner.NewToolResolver(provider)
	if err != nil {
		return nil, fmt.Errorf("planner: resolver: %w", err)
	}
	store := planner.NewMemoryEvidenceStore()
	p, err := planner.NewPlanner(
		planner.NewRuleBasedAnalyzer(),
		planner.NewCapabilityPlanner(),
		resolver,
		planner.NewEvidenceScorer(store),
		planner.NewExecutionPlanner(),
		store,
	)
	if err != nil {
		return nil, fmt.Errorf("planner: new: %w", err)
	}
	return p, nil
}

// NewBridge creates a ToolExecutionBridge with planner fallback.
//
// The bridge wraps a tool registry with planner-based intent resolution:
//   - Named tool found → execute directly
//   - Named tool not found → planner resolves intent, auto-selects tool
//   - Every execution saves evidence for future scoring
//
// Usage:
//
//	reg := tools.NewRegistry()
//	p, _ := tools.NewPlanner(reg)
//	bridge, _ := tools.NewBridge(reg, p)
//	result, err := bridge.Execute(ctx, "", nil, "计算1+1")
func NewBridge(r *Registry, p *Planner) (*Bridge, error) {
	store := planner.NewMemoryEvidenceStore()
	coreReg, err := r.coreRegistry()
	if err != nil {
		return nil, fmt.Errorf("core registry: %w", err)
	}
	bridge, err := planner.NewToolExecutionBridge(coreReg, p, store)
	if err != nil {
		return nil, fmt.Errorf("bridge: %w", err)
	}
	return bridge, nil
}

// coreRegistry returns the cached core.Registry, lazily building it from
// the public Registry's tools on first access.
func (r *Registry) coreRegistry() (*core.Registry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.coreReg != nil {
		return r.coreReg, nil
	}
	r.coreReg = core.NewRegistry()
	for name, tool := range r.tools {
		if err := r.coreReg.Register(&toolAdapter{tool: tool, name: name}); err != nil {
			return nil, err
		}
	}
	return r.coreReg, nil
}

// toolAdapter wraps an api/tools.Tool so it implements core.Tool.
type toolAdapter struct {
	tool Tool
	name string
}

func (a *toolAdapter) Name() string                { return a.name }
func (a *toolAdapter) Description() string         { return a.tool.Description() }
func (a *toolAdapter) Category() core.ToolCategory { return core.CategoryCore }
func (a *toolAdapter) Capabilities() []core.Capability {
	caps := a.tool.Capabilities()
	if len(caps) == 0 {
		return nil
	}
	result := make([]core.Capability, len(caps))
	for i, c := range caps {
		result[i] = core.Capability(c)
	}
	return result
}
func (a *toolAdapter) Parameters() *core.ParameterSchema { return nil }
func (a *toolAdapter) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	res, err := a.tool.Execute(ctx, params)
	if err != nil {
		return core.Result{Success: false, Error: err.Error()}, nil
	}
	return core.Result{Success: res.Success, Data: res.Data}, nil
}
