package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/dashboard"

	"github.com/google/uuid"
)

// MCPAdapter bridges ares_mcp.MCPClient to dashboard.MCPExecutor.
type MCPAdapter struct{ Client *ares_mcp.MCPClient }

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

// clientTools associates an MCP client with its server name and tools.
type clientTools struct {
	client *ares_mcp.MCPClient
	name   string
	tools  []ares_mcp.MCPToolDef
}

// MultiMCPAdapter aggregates multiple MCP servers and routes tool calls to the
// correct client based on the tool name.
type MultiMCPAdapter struct {
	entries []clientTools
	toolMap map[string]*ares_mcp.MCPClient // tool name → owning client
}

// NewMultiMCPAdapter creates an adapter from multiple MCP clients and their tools.
func NewMultiMCPAdapter(entries []clientTools) *MultiMCPAdapter {
	toolMap := make(map[string]*ares_mcp.MCPClient)
	for _, e := range entries {
		for _, t := range e.tools {
			toolMap[t.Name] = e.client
		}
	}
	return &MultiMCPAdapter{entries: entries, toolMap: toolMap}
}

func (a *MultiMCPAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*dashboard.MCPToolResult, error) {
	client, ok := a.toolMap[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found on any MCP server", name)
	}
	r, err := client.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	blocks := make([]dashboard.MCPContentBlock, len(r.Content))
	for i, b := range r.Content {
		blocks[i] = dashboard.MCPContentBlock{Type: b.Type, Text: b.Text}
	}
	return &dashboard.MCPToolResult{Content: blocks, IsError: r.IsError}, nil
}

func (a *MultiMCPAdapter) ListTools(ctx context.Context) ([]dashboard.MCPToolInfo, error) {
	seen := make(map[string]bool)
	var infos []dashboard.MCPToolInfo
	for _, e := range a.entries {
		for _, t := range e.tools {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			infos = append(infos, dashboard.MCPToolInfo{Name: t.Name, Description: t.Description})
		}
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
type MCPStatusBridge struct {
	Tools   []ares_mcp.MCPToolDef
	Servers []MCPStatusServer
}

// MCPStatusServer describes a connected MCP server for the dashboard.
type MCPStatusServer struct {
	Name  string
	Tools []ares_mcp.MCPToolDef
}

func (b *MCPStatusBridge) ListServers() []dashboard.MCPServerStatusView {
	if len(b.Servers) > 0 {
		views := make([]dashboard.MCPServerStatusView, 0, len(b.Servers))
		for _, s := range b.Servers {
			toolViews := make([]dashboard.MCPToolView, len(s.Tools))
			for i, t := range s.Tools {
				toolViews[i] = dashboard.MCPToolView{
					Name: t.Name, Description: t.Description, ServerName: s.Name,
				}
			}
			views = append(views, dashboard.MCPServerStatusView{
				Name: s.Name, Connected: true, ToolCount: len(s.Tools),
				Version: "connected", Tools: toolViews,
			})
		}
		return views
	}
	// Fallback for callers that only populate Tools.
	views := make([]dashboard.MCPToolView, len(b.Tools))
	for i, t := range b.Tools {
		views[i] = dashboard.MCPToolView{Name: t.Name, Description: t.Description, ServerName: "ares_mcp"}
	}
	return []dashboard.MCPServerStatusView{{
		Name: "ares_mcp", Connected: true, ToolCount: len(b.Tools), Version: "connected", Tools: views,
	}}
}

// ArenaAdapter implements dashboard.ArenaProvider.
type ArenaAdapter struct {
	Orch       *dashboard.Orchestrator
	Store      ares_events.EventStore
	llmAdapter *LLMAdapter
	mcpAdapter *MCPAdapter
	mu         sync.Mutex
	// stats counters — updated on each Execute call.
	totalActions      int
	successfulActions int
	failedActions     int
	resurrectionTotal int
	// pausedAgents tracks agents that have been paused.
	pausedAgents map[string]bool
	// slowAgents tracks agents that have been slowed down.
	slowAgents map[string]bool
	// history records every Execute result.
	history []dashboard.ArenaResult
}

func (a *ArenaAdapter) Execute(action dashboard.ArenaAction) dashboard.ArenaResult {
	start := time.Now()
	success := false
	switch action.Type {
	case dashboard.ArenaActionKillLeader:
		// Cancel first non-completed/failed agent to simulate leader kill.
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				success = a.Orch.CancelAgent(ag.ID)
				slog.Info("arena: killed leader", "id", ag.ID, "success", success)
				break
			}
		}
	case dashboard.ArenaActionKillAgent:
		// Cancel specific agent by ID.
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: killed agent", "id", action.TargetID, "success", success)
		}
	case dashboard.ArenaActionPauseAgent:
		// Pause agent: track paused state in map only.
		// The orchestrator does not natively support pause, so this is a
		// tracking-only operation. success=true reflects that the map write
		// succeeded; callers should treat this as "recorded" not "paused".
		if action.TargetID != "" {
			a.mu.Lock()
			if a.pausedAgents == nil {
				a.pausedAgents = make(map[string]bool)
			}
			a.pausedAgents[action.TargetID] = true
			a.mu.Unlock()
			success = true
			slog.Warn("arena: pause requested for agent (tracking-only, no native pause support)",
				"id", action.TargetID, "reason", "orchestrator_has_no_native_pause")
		}
	case dashboard.ArenaActionResumeAgent:
		// Resume is a no-op under cancel model; auto-resurrection handles it.
		success = true
		slog.Info("arena: resume requested (auto-resurrection handles)", "id", action.TargetID)
	case dashboard.ArenaActionSlowAgent:
		// Inject latency: track slowed state in map only.
		// The orchestrator does not natively support slowdown, so this is a
		// tracking-only operation. success=true reflects that the map write
		// succeeded; callers should treat this as "recorded" not "slowed".
		if action.TargetID != "" {
			a.mu.Lock()
			if a.slowAgents == nil {
				a.slowAgents = make(map[string]bool)
			}
			a.slowAgents[action.TargetID] = true
			a.mu.Unlock()
			success = true
			slog.Warn("arena: slow agent injected (tracking-only, no native slowdown support)",
				"id", action.TargetID, "note", "actual_slowdown_requires_proxy_or_middleware")
		}
	case dashboard.ArenaActionKillOrchestrator:
		// Cancel ALL running agents to simulate orchestrator failure.
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				if a.Orch.CancelAgent(ag.ID) {
					success = true
					slog.Info("arena: orchestrator failure — cancelled agent",
						"id", ag.ID, "severity", "critical")
				}
			}
		}
	case dashboard.ArenaActionNetworkPartition:
		// Simulate network isolation: cancel agent with higher severity logging.
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: network partition injected on agent",
				"id", action.TargetID, "success", success,
				"severity", "high", "fault_type", "network_isolation")
		}
	case dashboard.ArenaActionToolTimeout:
		// Simulate tool timeout: cancel with specific reason.
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: tool timeout simulated on agent",
				"id", action.TargetID, "success", success,
				"reason", "tool_timeout")
		}
	case dashboard.ArenaActionMemoryCorrupt:
		// Simulate memory corruption: cancel with reason and severity.
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			slog.Info("arena: memory corruption simulated on agent",
				"id", action.TargetID, "success", success,
				"reason", "memory_corrupt", "severity", "critical")
		}
	case dashboard.ArenaActionMCPDisconnect:
		// Simulate MCP disconnect: adapter exists but we cannot safely
		// disconnect it mid-operation, so fall back to cancel in all cases.
		if action.TargetID != "" {
			if a.mcpAdapter != nil && a.mcpAdapter.Client != nil {
				// Adapter available but unsafe to null-out mid-operation.
				success = a.Orch.CancelAgent(action.TargetID)
				slog.Warn("arena: MCP disconnect simulated (adapter available, fell back to cancel)",
					"id", action.TargetID, "success", success,
					"reason", "mcp_disconnect", "has_adapter", true)
			} else {
				success = a.Orch.CancelAgent(action.TargetID)
				slog.Info("arena: MCP disconnect simulated (no adapter, fallback to cancel)",
					"id", action.TargetID, "success", success,
					"reason", "mcp_disconnect")
			}
		}
	case dashboard.ArenaActionLLMFailure:
		// Simulate LLM failure: adapter exists but we cannot safely fail it
		// mid-operation, so fall back to cancel in all cases.
		if action.TargetID != "" {
			if a.llmAdapter != nil && a.llmAdapter.Adapter != nil {
				// Adapter available but unsafe to corrupt mid-operation.
				success = a.Orch.CancelAgent(action.TargetID)
				slog.Warn("arena: LLM failure simulated (adapter available, fell back to cancel)",
					"id", action.TargetID, "success", success,
					"reason", "llm_failure", "has_adapter", true)
			} else {
				success = a.Orch.CancelAgent(action.TargetID)
				slog.Info("arena: LLM failure simulated (no adapter, fallback to cancel)",
					"id", action.TargetID, "success", success,
					"reason", "llm_failure")
			}
		}
	}
	if a.Store != nil {
		evt := &ares_events.Event{
			ID: uuid.New().String(), StreamID: "arena",
			Type: "arena.action", Payload: map[string]any{"action": string(action.Type)},
			Timestamp: time.Now(),
		}
		if err := a.Store.Append(context.Background(), "arena", []*ares_events.Event{evt}, 0); err != nil {
			slog.Warn("arena: failed to record action event", "error", err)
		}
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
	pausedCount := 0
	slowCount := 0
	if a.pausedAgents != nil {
		pausedCount = len(a.pausedAgents)
	}
	if a.slowAgents != nil {
		slowCount = len(a.slowAgents)
	}
	return map[string]any{
		"total_actions":      a.totalActions,
		"successful_actions": a.successfulActions,
		"failed_actions":     a.failedActions,
		"paused_agents":      pausedCount,
		"slow_agents":        slowCount,
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

// roundTo rounds a float64 to the specified precision.
func roundTo(v float64, prec int) float64 {
	p := math.Pow(10, float64(prec))
	return math.Round(v*p) / p
}

// ResilienceScore returns a basic resilience score based on executed actions.
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

// --- SurvivalStarter / SurvivalProvider implementation ---

// StartSurvival starts survival mode (demo mode).
func (a *ArenaAdapter) StartSurvival(ctx context.Context) error {
	slog.Info("arena: survival mode started (demo mode)")
	return nil
}

// StopSurvival stops survival mode.
func (a *ArenaAdapter) StopSurvival() error {
	slog.Info("arena: survival mode stopped")
	return nil
}

// GetResilienceScore returns the current resilience score.
func (a *ArenaAdapter) GetResilienceScore() map[string]any {
	return a.ResilienceScore()
}

// GetSurvivalStatus returns the survival mode status.
func (a *ArenaAdapter) GetSurvivalStatus() map[string]any {
	return map[string]any{"running": false, "mode": "chaos_demo"}
}
