package sub

import (
	"context"
	"sync"

	"github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
)

// toolBinder binds and calls tools.
type toolBinder struct {
	mu       sync.RWMutex
	tools    map[string]func(ctx context.Context, args map[string]any) (any, error)
	registry *core.Registry
}

// NewToolBinder creates a new ToolBinder.
func NewToolBinder() ToolBinder {
	return &toolBinder{
		tools: make(map[string]func(ctx context.Context, args map[string]any) (any, error)),
	}
}

// BindTool binds a tool function to the agent.
func (b *toolBinder) BindTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error)) {
	if name == "" || toolFunc == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools[name] = toolFunc
}

// CallTool calls a bound tool by name.
func (b *toolBinder) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	b.mu.RLock()
	toolFunc, ok := b.tools[name]
	b.mu.RUnlock()

	if !ok {
		return nil, errors.ErrToolNotFound
	}

	return toolFunc(ctx, args)
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
		b.tools[name] = func(ctx context.Context, args map[string]any) (any, error) {
			return t.Execute(ctx, args)
		}
	}
}

// GetTool retrieves a tool function by name.
// If not found locally, it falls back to the bridged registry (if any).
func (b *toolBinder) GetTool(name string) (func(ctx context.Context, args map[string]any) (any, error), bool) {
	b.mu.RLock()
	tool, ok := b.tools[name]
	b.mu.RUnlock()
	if ok {
		return tool, true
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
