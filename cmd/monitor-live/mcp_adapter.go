package main

import (
	"context"

	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// mcpAdapter implements monitoring.MCPManager by delegating to a core.Registry.
// This bridges the gap between ares_mcp.MCPManager (which manages server connections)
// and the monitoring plugin's MCPManager interface (which lists and calls tools).
type mcpAdapter struct {
	registry *core.Registry
}

func newMCPAdapter(registry *core.Registry) *mcpAdapter {
	return &mcpAdapter{registry: registry}
}

func (a *mcpAdapter) ListTools(_ context.Context) ([]monitoring.MCPToolInfo, error) {
	if a.registry == nil {
		return nil, nil
	}
	names := a.registry.List()
	infos := make([]monitoring.MCPToolInfo, 0, len(names))
	for _, name := range names {
		tool, ok := a.registry.Get(name)
		if !ok || tool == nil {
			continue
		}
		infos = append(infos, monitoring.MCPToolInfo{
			Name:        name,
			Description: tool.Description(),
		})
	}
	return infos, nil
}

func (a *mcpAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*monitoring.MCPToolResult, error) {
	if a.registry == nil {
		return &monitoring.MCPToolResult{ToolName: name, IsError: true, Error: "no registry"}, nil
	}
	tool, ok := a.registry.Get(name)
	if !ok || tool == nil {
		return &monitoring.MCPToolResult{ToolName: name, IsError: true, Error: "tool not found: " + name}, nil
	}
	result, err := tool.Execute(ctx, args)
	if err != nil {
		return &monitoring.MCPToolResult{ToolName: name, IsError: true, Error: err.Error()}, nil
	}
	// Convert core.Result to monitoring.MCPToolResult
	output := map[string]any{
		"success": result.Success,
		"data":    result.Data,
	}
	return &monitoring.MCPToolResult{ToolName: name, Output: output}, nil
}
