// package dashboard - agent orchestration for the web dashboard.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"goagentx/internal/events"
)

// AgentTemplate defines a reusable agent configuration.
type AgentTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MCPTool     string `json:"mcp_tool"`
	MCPArgs     map[string]any `json:"mcp_args,omitempty"`
	LLMPrompt   string `json:"llm_prompt"`
}

// AgentRequest holds a request to create and run an agent.
type AgentRequest struct {
	TemplateID string         `json:"template_id,omitempty"` // Use a template
	Name       string         `json:"name,omitempty"`        // Or custom name
	MCPTool    string         `json:"mcp_tool,omitempty"`    // Or custom MCP tool
	MCPArgs    map[string]any `json:"mcp_args,omitempty"`
	LLMPrompt  string         `json:"llm_prompt,omitempty"`  // Or custom prompt
	Target     string         `json:"target,omitempty"`      // Target to analyze
}

// AgentResult holds the full result of an agent run.
type AgentResult struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Progress   int       `json:"progress"`
	MCPTool    string    `json:"mcp_tool"`
	RawDataLen int       `json:"raw_data_len"`
	Analysis   string    `json:"analysis"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Duration   string    `json:"duration,omitempty"`
}

// MCPExecutor abstracts MCP tool calls for the orchestrator.
type MCPExecutor interface {
	CallTool(ctx context.Context, name string, args map[string]any) (*MCPToolResult, error)
	ListTools(ctx context.Context) ([]MCPToolInfo, error)
}

// MCPToolResult is a simplified tool call result.
type MCPToolResult struct {
	Content []MCPContentBlock `json:"content"`
	IsError bool              `json:"is_error"`
}

// MCPContentBlock is a simplified content block.
type MCPContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCPToolInfo is a simplified tool info.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// LLMExecutor abstracts LLM calls for the orchestrator.
type LLMExecutor interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// EventBroadcaster emits events (optional, may be nil).
type EventBroadcaster interface {
	Broadcast(channel string, msg *WSMessage)
}

// Orchestrator manages agent lifecycle — creation, execution, results.
type Orchestrator struct {
	mcp       MCPExecutor
	llm       LLMExecutor
	templates []AgentTemplate
	agents    map[string]*AgentResult
	hub       *WSHub                   // optional, for real-time WS updates
	store     *events.MemoryEventStore // optional, for event persistence
	mu        sync.RWMutex
	nextID    atomic.Int64
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(mcp MCPExecutor, llm LLMExecutor) *Orchestrator {
	return &Orchestrator{
		mcp:    mcp,
		llm:    llm,
		agents: make(map[string]*AgentResult),
	}
}

// SetHub attaches a WebSocket hub for real-time agent updates.
func (o *Orchestrator) SetHub(hub *WSHub) {
	o.hub = hub
}

// SetEventStore attaches an event store for event persistence.
func (o *Orchestrator) SetEventStore(store *events.MemoryEventStore) {
	o.store = store
}

// SetTemplates sets the available agent templates.
func (o *Orchestrator) SetTemplates(templates []AgentTemplate) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.templates = templates
}

// GetTemplates returns available templates.
func (o *Orchestrator) GetTemplates() []AgentTemplate {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]AgentTemplate, len(o.templates))
	copy(result, o.templates)
	return result
}

// CreateAgent creates and starts an agent from a request.
// Returns the agent ID immediately; the agent runs in background.
func (o *Orchestrator) CreateAgent(req AgentRequest) (string, error) {
	// Resolve template if specified.
	if req.TemplateID != "" {
		o.mu.RLock()
		for _, t := range o.templates {
			if t.ID == req.TemplateID {
				if req.Name == "" {
					req.Name = t.Name
				}
				if req.MCPTool == "" {
					req.MCPTool = t.MCPTool
				}
				if req.MCPArgs == nil {
					req.MCPArgs = t.MCPArgs
				}
				if req.LLMPrompt == "" {
					req.LLMPrompt = t.LLMPrompt
				}
				break
			}
		}
		o.mu.RUnlock()
	}

	if req.Name == "" {
		return "", fmt.Errorf("agent name is required")
	}

	id := fmt.Sprintf("agent-%d", o.nextID.Add(1))

	result := &AgentResult{
		ID:        id,
		Name:      req.Name,
		Status:    "pending",
		MCPTool:   req.MCPTool,
		StartedAt: time.Now(),
	}

	o.mu.Lock()
	o.agents[id] = result
	o.mu.Unlock()

	// Run in background.
	go o.runAgent(id, req, result)

	return id, nil
}

// GetAgent returns the current state of an agent.
func (o *Orchestrator) GetAgent(id string) (*AgentResult, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	a, ok := o.agents[id]
	if !ok {
		return nil, false
	}
	// Return a copy.
	cp := *a
	return &cp, true
}

// ListAgents returns all agents.
func (o *Orchestrator) ListAgents() []AgentResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	results := make([]AgentResult, 0, len(o.agents))
	for _, a := range o.agents {
		cp := *a
		results = append(results, cp)
	}
	return results
}

// runAgent executes the full agent lifecycle: MCP → LLM → result.
func (o *Orchestrator) runAgent(id string, req AgentRequest, result *AgentResult) {
	ctx := context.Background()

	o.updateStatus(id, "running", 10, "")
	o.emitEvent(id, "agent.started", map[string]any{"name": req.Name, "tool": req.MCPTool})
	slog.Info("orchestrator: agent started", "id", id, "name", req.Name, "tool", req.MCPTool)

	// Phase 1: MCP data gathering.
	o.updateStatus(id, "gathering data...", 20, "")

	var rawData string
	if req.MCPTool == "" {
		// List tools.
		tools, err := o.mcp.ListTools(ctx)
		if err != nil {
			o.failAgent(id, err)
			return
		}
		data, _ := json.MarshalIndent(tools, "", "  ")
		rawData = string(data)
	} else {
		res, err := o.mcp.CallTool(ctx, req.MCPTool, req.MCPArgs)
		if err != nil {
			o.failAgent(id, err)
			return
		}
		for _, b := range res.Content {
			rawData += b.Text
		}
	}

	o.updateRawDataLen(id, len(rawData))
	o.updateStatus(id, "analyzing with LLM...", 50, "")
	slog.Info("orchestrator: MCP data gathered", "id", id, "bytes", len(rawData))

	// Phase 2: LLM analysis.
	prompt := req.LLMPrompt
	if prompt == "" {
		prompt = "Analyze the following code data and provide insights:\n\n{{.raw_data}}"
	}

	// Simple template substitution.
	if contains(prompt, "{{.raw_data}}") {
		prompt = replaceAll(prompt, "{{.raw_data}}", truncateStr(rawData, 8000))
	} else {
		prompt = prompt + "\n\nData:\n" + truncateStr(rawData, 8000)
	}

	analysis, err := o.llm.Generate(ctx, prompt)
	if err != nil {
		o.failAgent(id, err)
		return
	}

	// Phase 3: Done.
	o.mu.Lock()
	result.Status = "completed"
	result.Progress = 100
	result.Analysis = analysis
	result.FinishedAt = time.Now()
	result.Duration = result.FinishedAt.Sub(result.StartedAt).Round(time.Second).String()
	cp := *result
	o.mu.Unlock()

	slog.Info("orchestrator: agent completed", "id", id, "duration", result.Duration)
	o.emitEvent(id, "agent.completed", map[string]any{"duration": result.Duration, "analysis_len": len(analysis)})

	// Broadcast completion.
	if o.hub != nil {
		o.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: &cp,
		})
		// Also broadcast to the agent-specific channel for result viewers.
		o.hub.BroadcastToChannel("agent:"+id, &WSMessage{
			Type: "agent_result",
			Data: &cp,
		})
	}
}

func (o *Orchestrator) updateStatus(id, status string, progress int, errMsg string) {
	o.mu.Lock()
	var agentCopy *AgentResult
	if a, ok := o.agents[id]; ok {
		a.Status = status
		a.Progress = progress
		if errMsg != "" {
			a.Error = errMsg
		}
		cp := *a
		agentCopy = &cp
	}
	o.mu.Unlock()

	// Broadcast to WebSocket subscribers.
	if o.hub != nil && agentCopy != nil {
		o.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: agentCopy,
		})
	}
}

func (o *Orchestrator) updateRawDataLen(id string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if a, ok := o.agents[id]; ok {
		a.RawDataLen = n
	}
}

func (o *Orchestrator) failAgent(id string, err error) {
	o.mu.Lock()
	var cp *AgentResult
	if a, ok := o.agents[id]; ok {
		a.Status = "failed"
		a.Error = err.Error()
		a.FinishedAt = time.Now()
		a.Duration = a.FinishedAt.Sub(a.StartedAt).Round(time.Second).String()
		tmp := *a
		cp = &tmp
	}
	o.mu.Unlock()

	slog.Error("orchestrator: agent failed", "id", id, "error", err)
	o.emitEvent(id, "agent.failed", map[string]any{"error": err.Error()})

	if o.hub != nil && cp != nil {
		o.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: cp,
		})
	}
}

// emitEvent stores an event in the event store (if configured).
func (o *Orchestrator) emitEvent(streamID, eventType string, payload map[string]any) {
	if o.store == nil {
		return
	}
	ctx := context.Background()
	evt := &events.Event{
		ID:        fmt.Sprintf("evt-%d", time.Now().UnixNano()),
		StreamID:  streamID,
		Type:      events.EventType(eventType),
		Payload:   payload,
		Timestamp: time.Now(),
	}
	_ = o.store.Append(ctx, streamID, []*events.Event{evt}, 0)
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func replaceAll(s, old, new string) string {
	result := ""
	for {
		idx := searchString2(s, old)
		if idx < 0 {
			result += s
			break
		}
		result += s[:idx] + new
		s = s[idx+len(old):]
	}
	return result
}

func searchString2(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
