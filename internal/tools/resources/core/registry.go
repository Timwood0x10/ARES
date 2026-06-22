package core

import (
	"context"
	"errors"
	"sync"

	gerr "goagentx/internal/errors"
)

// Registry manages tool registration and lookup.
type Registry struct {
	tools       map[string]Tool
	mu          sync.RWMutex
	schemaCache []ToolSchema
	schemaDirty bool
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:       make(map[string]Tool),
		schemaDirty: true,
	}
}

// Register registers a tool.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tool == nil {
		return ErrNilTool
	}

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return gerr.Wrap(ErrToolAlreadyRegistered, name)
	}

	r.tools[name] = tool
	r.schemaDirty = true
	return nil
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return gerr.Wrap(ErrToolNotFound, name)
	}

	delete(r.tools, name)
	r.schemaDirty = true
	return nil
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	return tool, exists
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

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.tools)
}

// Execute executes a tool by name.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (Result, error) {
	tool, exists := r.Get(name)
	if !exists {
		return Result{}, gerr.Wrap(ErrToolNotFound, name)
	}

	return tool.Execute(ctx, params)
}

// Clear removes all tools.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tools = make(map[string]Tool)
	r.schemaCache = nil
	r.schemaDirty = true
}

// Filter returns tools that match the given filter criteria.
func (r *Registry) Filter(filter *ToolFilter) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// If filter is nil, return all tools
	if filter == nil {
		return &Registry{
			tools: r.tools,
		}
	}

	filtered := NewRegistry()

	for name, tool := range r.tools {
		// Check if tool is in enabled list
		if len(filter.Enabled) > 0 && !containsString(filter.Enabled, name) {
			continue
		}

		// Check if tool is in disabled list - if so, exclude it
		if len(filter.Disabled) > 0 && containsString(filter.Disabled, name) {
			continue
		}

		// Check category filter
		if len(filter.Categories) > 0 && !containsCategory(filter.Categories, tool.Category()) {
			continue
		}

		// Register tool in filtered registry
		filtered.tools[name] = tool
	}

	return filtered
}

// FilterByCategory returns tools of a specific category.
func (r *Registry) FilterByCategory(category ToolCategory) *Registry {
	return r.Filter(&ToolFilter{
		Categories: []ToolCategory{category},
	})
}

// GetSchemas returns schema information for all tools in the registry.
func (r *Registry) GetSchemas() []ToolSchema {
	r.mu.RLock()
	if !r.schemaDirty && r.schemaCache != nil {
		defer r.mu.RUnlock()
		return r.schemaCache
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if !r.schemaDirty && r.schemaCache != nil {
		return r.schemaCache
	}

	schemas := make([]ToolSchema, 0, len(r.tools))
	for _, tool := range r.tools {
		schemas = append(schemas, ToolSchema{
			Name:        tool.Name(),
			Description: tool.Description(),
			Category:    tool.Category(),
			Parameters:  tool.Parameters(),
		})
	}

	r.schemaCache = schemas
	r.schemaDirty = false
	return schemas
}

// ToolFilter defines filter criteria for tools.
type ToolFilter struct {
	Enabled    []string       // List of enabled tool names (if not empty, only these tools are included)
	Disabled   []string       // List of disabled tool names (these tools are excluded)
	Categories []ToolCategory // List of allowed categories (if not empty, only these categories are included)
}

// containsString checks if a string is in a slice.
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// containsCategory checks if a category is in a slice.
func containsCategory(slice []ToolCategory, item ToolCategory) bool {
	for _, c := range slice {
		if c == item {
			return true
		}
	}
	return false
}

// Registry errors.
var (
	ErrNilTool               = errors.New("tool is nil")
	ErrToolNotFound          = errors.New("tool not found")
	ErrToolAlreadyRegistered = errors.New("tool already registered")
)

// GlobalRegistry is the default tool registry.
var GlobalRegistry = NewRegistry()

// Register registers a tool in the global registry.
func Register(tool Tool) error {
	return GlobalRegistry.Register(tool)
}

// Get retrieves a tool from the global registry.
func Get(name string) (Tool, bool) {
	return GlobalRegistry.Get(name)
}

// List returns all tools from the global registry.
func List() []string {
	return GlobalRegistry.List()
}

// Execute executes a tool from the global registry.
func Execute(ctx context.Context, name string, params map[string]interface{}) (Result, error) {
	return GlobalRegistry.Execute(ctx, name, params)
}

// ToolGroup groups related tools.
type ToolGroup struct {
	name        string
	description string
	registry    *Registry
}

// NewToolGroup creates a new ToolGroup.
func NewToolGroup(name, description string) *ToolGroup {
	return &ToolGroup{
		name:        name,
		description: description,
		registry:    NewRegistry(),
	}
}

// Register registers a tool in the group.
func (g *ToolGroup) Register(tool Tool) error {
	return g.registry.Register(tool)
}

// Get retrieves a tool from the group.
func (g *ToolGroup) Get(name string) (Tool, bool) {
	return g.registry.Get(name)
}

// List returns all tool names in the group.
func (g *ToolGroup) List() []string {
	return g.registry.List()
}

// Name returns the group name.
func (g *ToolGroup) Name() string {
	return g.name
}

// Description returns the group description.
func (g *ToolGroup) Description() string {
	return g.description
}
