// Package tools provides the public API for tool registration and execution.
//
// External projects can use this package to:
//   - Register custom tools
//   - Call built-in tools (web_search, http_request, etc.)
//   - Create tool registries for agent systems
//
// Usage:
//
//	import "github.com/Timwood0x10/ares/api/tools"
//
//	// Create a registry
//	registry := tools.NewRegistry()
//
//	// Register built-in tools
//	tools.RegisterBuiltinTools(registry)
//
//	// Register custom tool
//	registry.Register(tools.ToolFunc{
//	    ToolName: "my_tool",
//	    ToolDesc: "My custom tool",
//	    Fn: func(ctx context.Context, params map[string]any) (any, error) {
//	        return "result", nil
//	    },
//	})
//
//	// Call a tool
//	result, err := registry.Execute(ctx, "web_search", map[string]any{"query": "hello"})
package tools

import (
	"context"
	"errors"
	"sync"
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
}

// ToolFunc is a convenience type for creating tools from functions.
type ToolFunc struct {
	ToolName string
	ToolDesc string
	Fn       func(ctx context.Context, params map[string]any) (any, error)
}

func (f ToolFunc) Name() string        { return f.ToolName }
func (f ToolFunc) Description() string { return f.ToolDesc }
func (f ToolFunc) Execute(ctx context.Context, params map[string]any) (Result, error) {
	data, err := f.Fn(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: true, Data: data}, nil
}

// Registry manages tool registration and execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return errors.New("tool is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
	return nil
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
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

// ToolInfo is a summary of a tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
