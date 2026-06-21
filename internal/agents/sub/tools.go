package sub

import (
	"context"
	"sync"

	"goagentx/internal/core/errors"
	"goagentx/internal/tools/resources/core"
)

// toolEntry wraps a tool function with its idempotency metadata.
type toolEntry struct {
	fn         func(ctx context.Context, args map[string]any) (any, error)
	idempotent bool
}

// toolBinder binds and calls tools.
type toolBinder struct {
	mu       sync.RWMutex
	tools    map[string]toolEntry
	registry *core.Registry
}

// NewToolBinder creates a new ToolBinder.
func NewToolBinder() ToolBinder {
	return &toolBinder{
		tools: make(map[string]toolEntry),
	}
}

// BindTool binds a tool function to the agent. Tools registered via this
// method are assumed non-idempotent by default — a retry after a partial
// failure may re-execute the tool. Use BindIdempotentTool for tools that
// are safe to retry.
func (b *toolBinder) BindTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error)) {
	if name == "" || toolFunc == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools[name] = toolEntry{fn: toolFunc, idempotent: false}
}

// BindIdempotentTool binds a tool known to be safe for retry.
func (b *toolBinder) BindIdempotentTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error)) {
	if name == "" || toolFunc == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools[name] = toolEntry{fn: toolFunc, idempotent: true}
}

// CallTool calls a bound tool by name.
// Returns ErrToolNotFound if the tool is not bound.
// Use IsToolIdempotent to check whether the tool is safe to retry.
func (b *toolBinder) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	b.mu.RLock()
	entry, ok := b.tools[name]
	b.mu.RUnlock()

	if !ok {
		return nil, errors.ErrToolNotFound
	}

	return entry.fn(ctx, args)
}

// IsToolIdempotent reports whether a tool is safe to retry on failure.
func (b *toolBinder) IsToolIdempotent(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.tools[name]
	return ok && entry.idempotent
}

// ListTools returns all available tool names.
func (b *toolBinder) ListTools() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.tools))
	for name := range b.tools {
		names = append(names, name)
	}
	return names
}

// ListIdempotentTools returns names of all tools marked as idempotent.
func (b *toolBinder) ListIdempotentTools() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.tools))
	for name, entry := range b.tools {
		if entry.idempotent {
			names = append(names, name)
		}
	}
	return names
}

// BridgeFromRegistry imports all tools from the given Registry into this ToolBinder.
// Tools already registered in the ToolBinder (by name) are not overwritten.
func (b *toolBinder) BridgeFromRegistry(registry *core.Registry) {
	if registry == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.registry = registry
	for _, name := range registry.List() {
		if _, exists := b.tools[name]; exists {
			continue
		}
		tool, ok := registry.Get(name)
		if !ok {
			continue
		}
		// capture tool for closure
		t := tool
		b.tools[name] = toolEntry{
			fn: func(ctx context.Context, args map[string]any) (any, error) {
				return t.Execute(ctx, args)
			},
			idempotent: false,
		}
	}
}

// GetTool retrieves a tool function by name.
// If not found locally, it falls back to the bridged registry (if any).
func (b *toolBinder) GetTool(name string) (func(ctx context.Context, args map[string]any) (any, error), bool) {
	b.mu.RLock()
	entry, ok := b.tools[name]
	b.mu.RUnlock()
	if ok {
		return entry.fn, true
	}
	if b.registry != nil {
		if t, found := b.registry.Get(name); found && t != nil {
			return func(ctx context.Context, args map[string]any) (any, error) {
				return t.Execute(ctx, args)
			}, true
		}
	}
	return nil, false
}

// ToolIdempotent reports whether a tool in the binder is marked idempotent.
func (b *toolBinder) ToolIdempotent(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.tools[name]
	return ok && entry.idempotent
}
