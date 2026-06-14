package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"goagentx/internal/dashboard"
	"goagentx/internal/events"
	"goagentx/internal/mcp"

	"github.com/google/uuid"
)

// MCPAdapter bridges mcp.MCPClient to dashboard.MCPExecutor.
type MCPAdapter struct{ Client *mcp.MCPClient }

func (a *MCPAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*dashboard.MCPToolResult, error) {
	r, err := a.Client.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	blocks := make([]dashboard.MCPContentBlock, len(r.Content))
	for i, b := range r.Content {
		blocks[i] = dashboard.MCPContentBlock{Type: b.Type, Text: b.Text}
	}
	return &dashboard.MCPToolResult{Content: blocks, IsError: r.IsError}, nil
}

func (a *MCPAdapter) ListTools(ctx context.Context) ([]dashboard.MCPToolInfo, error) {
	tools, err := a.Client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]dashboard.MCPToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = dashboard.MCPToolInfo{Name: t.Name, Description: t.Description}
	}
	return infos, nil
}

// LLMAdapter bridges output.LLMAdapter to dashboard.LLMExecutor.
type LLMAdapter struct {
	Adapter interface {
		Generate(ctx context.Context, prompt string) (string, error)
	}
}

func (w *LLMAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	return w.Adapter.Generate(ctx, prompt)
}

// MCPStatusBridge provides MCP server status to the dashboard.
type MCPStatusBridge struct{ Tools []mcp.MCPToolDef }

func (b *MCPStatusBridge) ListServers() []dashboard.MCPServerStatusView {
	views := make([]dashboard.MCPToolView, len(b.Tools))
	for i, t := range b.Tools {
		views[i] = dashboard.MCPToolView{Name: t.Name, Description: t.Description, ServerName: "codegraph"}
	}
	return []dashboard.MCPServerStatusView{{
		Name: "codegraph", Connected: true, ToolCount: len(b.Tools), Version: "connected", Tools: views,
	}}
}

// ArenaAdapter implements dashboard.ArenaProvider.
type ArenaAdapter struct {
	Orch  *dashboard.Orchestrator
	Store *events.MemoryEventStore
	mu    sync.Mutex
}

func (a *ArenaAdapter) Execute(action dashboard.ArenaAction) dashboard.ArenaResult {
	start := time.Now()
	switch action.Type {
	case dashboard.ArenaActionKillLeader:
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				a.Orch.CancelAgent(ag.ID)
				slog.Info("arena: killed leader", "id", ag.ID)
				break
			}
		}
	case dashboard.ArenaActionKillAgent:
		if action.TargetID != "" {
			a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: killed agent", "id", action.TargetID)
		}
	}
	if a.Store != nil {
		evt := &events.Event{
			ID: uuid.New().String(), StreamID: "arena",
			Type: "arena.action", Payload: map[string]any{"action": string(action.Type)},
			Timestamp: time.Now(),
		}
		_ = a.Store.Append(context.Background(), "arena", []*events.Event{evt}, 0)
	}
	return dashboard.ArenaResult{Success: true, Action: action, Duration: time.Since(start)}
}

func (a *ArenaAdapter) Stats() map[string]any {
	return map[string]any{"total_actions": 0, "successful_actions": 0, "failed_actions": 0}
}

func (a *ArenaAdapter) History() []dashboard.ArenaResult {
	return nil
}
