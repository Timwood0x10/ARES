package main

import (
	"context"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/monitoring"
)

// mcpAdapter implements monitoring.MCPManager by delegating to api/tools.Registry.
type mcpAdapter struct {
	registry *api_tools.Registry
}

func (a *mcpAdapter) ListTools(_ context.Context) ([]monitoring.MCPToolInfo, error) {
	if a.registry == nil {
		return []monitoring.MCPToolInfo{}, nil
	}
	infos := make([]monitoring.MCPToolInfo, 0)
	for _, t := range a.registry.ListTools() {
		infos = append(infos, monitoring.MCPToolInfo{
			Name:        t.Name,
			Description: t.Description,
		})
	}
	return infos, nil
}

func (a *mcpAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*monitoring.MCPToolResult, error) {
	if a.registry == nil {
		return &monitoring.MCPToolResult{ToolName: name, IsError: true, Error: "no registry"}, nil
	}
	result, err := a.registry.Execute(ctx, name, args)
	if err != nil {
		return &monitoring.MCPToolResult{ToolName: name, IsError: true, Error: err.Error()}, nil
	}
	output := map[string]any{"success": result.Success, "data": result.Data}
	return &monitoring.MCPToolResult{ToolName: name, Output: output}, nil
}
