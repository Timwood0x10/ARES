package main

import (
	"context"
	"fmt"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	builtintools "github.com/Timwood0x10/ares/internal/tools/resources/builtin"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// setupMCP connects to MCP servers and registers their tools in the public registry.
// The builtin→public bridge ALWAYS runs so the dashboard sees builtin tools
// regardless of whether MCP servers are configured; MCP-specific setup only
// runs when at least one MCP server is configured.
func setupMCP(ctx context.Context, cfg *ares_config.Config, registry *api_tools.Registry) (*core.Registry, error) {
	internalReg := core.NewRegistry()

	// Register builtin general tools into the internal registry so sub-agents
	// receive them through the ToolBinder (closure of the tools module, P2.1).
	if err := builtintools.RegisterGeneralTools(internalReg); err != nil {
		return internalReg, fmt.Errorf("register general tools: %w", err)
	}

	// Conditionally connect MCP servers and register their tools into the
	// internal registry. Builtin tools remain available regardless of MCP
	// configuration.
	if len(cfg.MCP.Servers) > 0 {
		mcpMgr, err := ares_bootstrap.SetupMCP(ctx, &cfg.MCP, internalReg)
		if err != nil {
			return internalReg, fmt.Errorf("MCP setup: %w", err)
		}
		if mcpMgr != nil {
			fmt.Printf("MCP manager started: %d servers\n", len(cfg.MCP.Servers))
		}
	}

	// Bridge: register all internal tools (builtin + MCP) into the public
	// api/tools registry so the dashboard sees them regardless of whether MCP
	// servers are configured.
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
