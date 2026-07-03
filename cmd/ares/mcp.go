package main

import (
	"context"
	"fmt"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// setupMCP connects to MCP servers and registers their tools in the public registry.
func setupMCP(ctx context.Context, cfg *ares_config.Config, registry *api_tools.Registry) (*core.Registry, error) {
	internalReg := core.NewRegistry()

	if len(cfg.MCP.Servers) == 0 {
		return internalReg, nil
	}

	mcpMgr, err := ares_bootstrap.SetupMCP(ctx, &cfg.MCP, internalReg)
	if err != nil {
		return internalReg, fmt.Errorf("MCP setup: %w", err)
	}
	if mcpMgr != nil {
		fmt.Printf("MCP manager started: %d servers\n", len(cfg.MCP.Servers))
	}

	// Bridge: register MCP tools into the public api/tools registry
	for _, name := range internalReg.List() {
		tool, ok := internalReg.Get(name)
		if !ok || tool == nil {
			continue
		}
		t := tool
	if err := registry.Register(api_tools.ToolFunc{
			ToolName: t.Name(),
			ToolDesc: t.Description(),
			Fn: func(ctx context.Context, params map[string]any) (any, error) {
				res, err := t.Execute(ctx, params)
				if err != nil {
					return nil, err
				}
				return res.Data, nil
			},
		}); err != nil {
			fmt.Printf("MCP bridge: failed to register tool %s: %v\n", t.Name(), err)
		}
	}

	return internalReg, nil
}
