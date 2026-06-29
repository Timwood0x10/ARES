package main

import (
	"context"
	"log/slog"

	api_tools "github.com/Timwood0x10/ares/api/tools"
)

// newToolRegistry creates the public tool registry with built-in + custom tools.
func newToolRegistry() (*api_tools.Registry, error) {
	r := api_tools.NewRegistry()
	if err := api_tools.RegisterBuiltinTools(r); err != nil {
		return nil, err
	}
	return r, nil
}

// registryToolBinder adapts api/tools.Registry to sub.ToolBinder interface.
type registryToolBinder struct {
	registry *api_tools.Registry
}

func (b *registryToolBinder) BindTool(name string, fn func(ctx context.Context, args map[string]any) (any, error)) {
	if err := b.registry.Register(api_tools.ToolFunc{
		ToolName: name,
		ToolDesc: "",
		Fn:       fn,
	}); err != nil {
		slog.Warn("bind tool failed", "name", name, "error", err)
	}
}

func (b *registryToolBinder) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	result, err := b.registry.Execute(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (b *registryToolBinder) ListTools() []string {
	return b.registry.List()
}

func (b *registryToolBinder) IsToolIdempotent(_ string) bool { return false }
func (b *registryToolBinder) ListIdempotentTools() []string  { return nil }
