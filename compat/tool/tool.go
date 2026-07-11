// Package tool is the tool-shell compatibility layer of ARES.
//
// It defines the unified Tool interface and a registry for builtin and
// third-party tool adapters. ARES officially maintains a small set of builtin
// tools; third-party tools register via compat.RegisterTool.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Tool is the unified interface for agent-callable tools.
//
// Implementations are registered via Register and looked up by name.
// ARES maintains a builtin set (filesystem, shell, http, …); third-party
// plugins may register additional tools (database, cloud APIs, …).
//
// A tool takes a JSON-encoded arguments blob, executes synchronously, and
// returns a JSON-encodable result. Tools are responsible for their own
// permission/authorization checks; ARES does not sandbox tool execution.
type Tool interface {
	// Execute runs the tool with the given JSON-encoded arguments.
	// Returns a JSON-encodable result or an error.
	Execute(ctx context.Context, args json.RawMessage) (any, error)

	// Name returns the canonical tool name (e.g. "fs.read", "http.get").
	Name() string

	// Description returns a human-readable summary of what the tool does.
	// Used by agent planners to select tools; should be concise.
	Description() string
}

// Factory constructs a Tool from a raw config map.
// The config schema is tool-specific; builtin tools document theirs.
type Factory func(config map[string]any) (Tool, error)

// Registry holds all registered tools by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Factory
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Factory)}
}

// Register registers a tool factory by name.
// Returns an error if name is empty, factory is nil, or name is already registered.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return errors.New("compat/tool: name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("compat/tool: factory must not be nil for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("compat/tool: %q already registered", name)
	}
	r.tools[name] = factory
	return nil
}

// Lookup returns the factory registered under name, or ErrNotFound.
func (r *Registry) Lookup(name string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return f, nil
}

// Names returns all registered tool names in arbitrary order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for k := range r.tools {
		names = append(names, k)
	}
	return names
}

// ErrNotFound is the sentinel returned by Lookup for unknown tools.
var ErrNotFound = errors.New("compat/tool: tool not found")
