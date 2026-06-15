package api

import (
	"context"
	"log/slog"
	"math"
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
	// stats counters — updated on each Execute call.
	totalActions      int
	successfulActions int
	failedActions     int
	resurrectionTotal int
	// history records every Execute result.
	history []dashboard.ArenaResult
}

func (a *ArenaAdapter) Execute(action dashboard.ArenaAction) dashboard.ArenaResult {
	start := time.Now()
	success := false
	switch action.Type {
	case dashboard.ArenaActionKillLeader:
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				success = a.Orch.CancelAgent(ag.ID)
				slog.Info("arena: killed leader", "id", ag.ID, "success", success)
				break
			}
		}
	case dashboard.ArenaActionKillAgent:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: killed agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionPauseAgent:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: paused agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionResumeAgent:
		// Resume 在 cancel 模型下是空操作；自动复活机制处理。
		success = true
		slog.Info("arena: resume requested (auto-resurrection handles)", "id", action.TargetID)
	case dashboard.ArenaActionSlowAgent:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: slowed agent (kill→resurrect cycle)", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionKillOrchestrator:
		// 杀死第一个运行中的 agent（模拟编排器故障）。
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				success = a.Orch.CancelAgent(ag.ID)
				slog.Info("arena: killed via orchestrator-fault", "id", ag.ID, "success", success)
				break
			}
		}
	case dashboard.ArenaActionNetworkPartition:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: network partition on agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionToolTimeout:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: tool timeout on agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionMemoryCorrupt:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: memory corrupt on agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionMCPDisconnect:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: mcp disconnect on agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionLLMFailure:
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: llm failure on agent", "id", action.TargetID, "success", success)
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
	result := dashboard.ArenaResult{Success: success, Action: action, Duration: time.Since(start)}

	// Track stats and history under lock.
	a.mu.Lock()
	a.totalActions++
	if success {
		a.successfulActions++
		a.resurrectionTotal++
	} else {
		a.failedActions++
	}
	a.history = append(a.history, result)
	a.mu.Unlock()

	return result
}

func (a *ArenaAdapter) Stats() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"total_actions":      a.totalActions,
		"successful_actions": a.successfulActions,
		"failed_actions":     a.failedActions,
	}
}

func (a *ArenaAdapter) History() []dashboard.ArenaResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.history) == 0 {
		return nil
	}
	cp := make([]dashboard.ArenaResult, len(a.history))
	copy(cp, a.history)
	return cp
}

// roundTo 将浮点数四舍五入到指定精度。
func roundTo(v float64, prec int) float64 {
	p := math.Pow(10, float64(prec))
	return math.Round(v*p) / p
}

// ResilienceScore 返回基于已执行动作的基本弹性评分。
func (a *ArenaAdapter) ResilienceScore() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	score := 100.0
	if a.totalActions > 0 {
		score = float64(a.successfulActions) / float64(a.totalActions) * 100
	}
	grade := "F"
	if score >= 97 {
		grade = "A+"
	} else if score >= 93 {
		grade = "A"
	} else if score >= 90 {
		grade = "A-"
	} else if score >= 87 {
		grade = "B+"
	} else if score >= 83 {
		grade = "B"
	} else if score >= 80 {
		grade = "B-"
	} else if score >= 77 {
		grade = "C+"
	} else if score >= 73 {
		grade = "C"
	} else if score >= 70 {
		grade = "C-"
	} else if score >= 67 {
		grade = "D+"
	} else if score >= 63 {
		grade = "D"
	} else if score >= 60 {
		grade = "D-"
	}
	return map[string]any{
		"score":         roundTo(score, 1),
		"grade":         grade,
		"total_actions": a.totalActions,
		"success_rate":  roundTo(score, 1),
	}
}

// ── SurvivalStarter / SurvivalProvider 实现 ──

// StartSurvival 启动生存模式（演示模式）。
func (a *ArenaAdapter) StartSurvival(ctx context.Context) error {
	slog.Info("arena: survival mode started (demo mode)")
	return nil
}

// StopSurvival 停止生存模式。
func (a *ArenaAdapter) StopSurvival() error {
	slog.Info("arena: survival mode stopped")
	return nil
}

// GetResilienceScore 返回当前弹性评分。
func (a *ArenaAdapter) GetResilienceScore() map[string]any {
	return a.ResilienceScore()
}

// GetSurvivalStatus 返回生存模式状态。
func (a *ArenaAdapter) GetSurvivalStatus() map[string]any {
	return map[string]any{"running": false, "mode": "chaos_demo"}
}
