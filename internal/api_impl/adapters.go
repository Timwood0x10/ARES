package apiimpl

import (
	"context"
	"fmt"
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
	Orch  *dashboard.Orchestrator
	Store ares_events.EventStore
	mu    sync.Mutex
	// stats counters — updated on each Execute call.
	totalActions      int
	successfulActions int
	failedActions     int
	resurrectionTotal int
	// history records every Execute result.
	history []dashboard.ArenaResult
}

//nolint:gocyclo // Complex arena action execution with multiple action types
func (a *ArenaAdapter) Execute(action dashboard.ArenaAction) dashboard.ArenaResult {
	start := time.Now()
	success := false
	var errMsg string

	switch action.Type {
	case dashboard.ArenaActionKillLeader:
		// Cancel first non-completed/failed agent to simulate leader kill.
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				success = a.Orch.CancelAgent(ag.ID)
				log.Info("arena: killed leader", "id", ag.ID, "success", success)
				break
			}
		}

	case dashboard.ArenaActionKillAgent:
		// Cancel specific agent by ID.
		if action.TargetID != "" {
			success = a.Orch.CancelAgent(action.TargetID)
			log.Info("arena: killed agent", "id", action.TargetID, "success", success)
		}

	case dashboard.ArenaActionKillOrchestrator:
		// Cancel ALL running agents to simulate orchestrator failure.
		for _, ag := range a.Orch.ListAgents() {
			if ag.Status != "completed" && ag.Status != "failed" {
				if a.Orch.CancelAgent(ag.ID) {
					success = true
					log.Info("arena: orchestrator failure — cancelled agent",
						"id", ag.ID, "severity", "critical")
				}
			}
		}

	case dashboard.ArenaActionPauseAgent:
		// Pause is not supported by the orchestrator. Return an explicit error
		// rather than silently tracking state in a map (P1-7).
		errMsg = "arena: pause not supported — orchestrator has no native pause"

	case dashboard.ArenaActionResumeAgent:
		// Resume is a no-op under cancel model; auto-resurrection handles it.
		success = true
		log.Info("arena: resume requested (auto-resurrection handles)", "id", action.TargetID)

	case dashboard.ArenaActionSlowAgent:
		// Slowdown is not supported by the orchestrator. Return an explicit error
		// rather than silently tracking state in a map (P1-7).
		errMsg = "arena: slow not supported — orchestrator has no native slowdown"

	case dashboard.ArenaActionNetworkPartition:
		errMsg = "arena: network partition not supported — use CancelAgent directly"

	case dashboard.ArenaActionToolTimeout:
		errMsg = "arena: tool timeout not supported — use CancelAgent directly"

	case dashboard.ArenaActionMemoryCorrupt:
		errMsg = "arena: memory corruption not supported — use CancelAgent directly"

	case dashboard.ArenaActionMCPDisconnect:
		errMsg = "arena: MCP disconnect not supported — use CancelAgent directly"

	case dashboard.ArenaActionLLMFailure:
		errMsg = "arena: LLM failure not supported — use CancelAgent directly"
	}

	if errMsg != "" {
		log.Warn(errMsg, "target_id", action.TargetID, "action", action.Type)
	}
	if a.Store != nil {
		evt := &ares_events.Event{
			ID: uuid.New().String(), StreamID: "arena",
			Type: "arena.action", Payload: map[string]any{"action": string(action.Type)},
			Timestamp: time.Now(),
		}
		if err := a.Store.Append(context.Background(), "arena", []*ares_events.Event{evt}, 0); err != nil {
			log.Warn("arena: failed to record action event", "error", err)
		}
	}
	result := dashboard.ArenaResult{Success: success, Action: action, Duration: time.Since(start), Error: errMsg}

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
	switch {
	case score >= 97:
		grade = "A+"
	case score >= 93:
		grade = "A"
	case score >= 90:
		grade = "A-"
	case score >= 87:
		grade = "B+"
	case score >= 83:
		grade = "B"
	case score >= 80:
		grade = "B-"
	case score >= 77:
		grade = "C+"
	case score >= 73:
		grade = "C"
	case score >= 70:
		grade = "C-"
	case score >= 67:
		grade = "D+"
	case score >= 63:
		grade = "D"
	case score >= 60:
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
	log.Info("arena: survival mode started (demo mode)")
	return nil
}

// StopSurvival stops survival mode.
func (a *ArenaAdapter) StopSurvival() error {
	log.Info("arena: survival mode stopped")
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
