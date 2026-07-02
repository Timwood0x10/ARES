package tabs

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
)

const (
	maxToolCalls = 1000
	maxToolDefs  = 200
)

// MCPTabSnapshot is the snapshot payload returned by MCPTab.Snapshot.
type MCPTabSnapshot struct {
	Tools []ToolDef                `json:"tools"`
	Calls []monitoring.MCPToolCall `json:"calls"`
	Total int                      `json:"total"`
}

// ToolDef describes a registered MCP tool.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPTab implements the Tab interface for the MCP tab.
// It tracks tool registrations and tool call events.
type MCPTab struct {
	mu    sync.RWMutex
	tools []ToolDef
	calls []monitoring.MCPToolCall
}

// NewMCPTab creates a new MCPTab instance.
func NewMCPTab() *MCPTab {
	return &MCPTab{
		tools: make([]ToolDef, 0, maxToolDefs),
		calls: make([]monitoring.MCPToolCall, 0, maxToolCalls),
	}
}

// Name returns the tab identifier.
func (t *MCPTab) Name() string { return "mcp" }

// Label returns the human-readable tab name.
func (t *MCPTab) Label() string { return "MCP" }

// HandleEvent processes MCP-related events.
func (t *MCPTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case ares_events.EventToolCallStarted:
		t.handleToolCallStarted(evt)
	case ares_events.EventToolCallCompleted:
		t.handleToolCallCompleted(evt)
	}
}

// Snapshot returns the current MCP state.
func (t *MCPTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	tools := make([]ToolDef, len(t.tools))
	copy(tools, t.tools)
	calls := make([]monitoring.MCPToolCall, len(t.calls))
	copy(calls, t.calls)

	return MCPTabSnapshot{
		Tools: tools,
		Calls: calls,
		Total: len(calls),
	}
}

func (t *MCPTab) handleToolCallStarted(evt *ares_events.Event) {
	toolName := getString(evt.Payload, "tool_name")
	call := monitoring.MCPToolCall{
		ID:        evt.ID,
		AgentID:   getString(evt.Payload, "agent_id"),
		ToolName:  toolName,
		Input:     getMap(evt.Payload, "input"),
		Status:    "running",
		Timestamp: evt.Timestamp,
	}
	if call.AgentID == "" {
		call.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Register tool if new.
	if toolName != "" && !t.toolExists(toolName) {
		if len(t.tools) >= maxToolDefs {
			t.tools = t.tools[1:]
		}
		t.tools = append(t.tools, ToolDef{
			Name:        toolName,
			Description: getString(evt.Payload, "tool_description"),
		})
	}

	if len(t.calls) >= maxToolCalls {
		t.calls = t.calls[1:]
	}
	t.calls = append(t.calls, call)
}

func (t *MCPTab) handleToolCallCompleted(evt *ares_events.Event) {
	callID := getString(evt.Payload, "call_id")
	if callID == "" {
		callID = evt.ID
	}
	duration := getDuration(evt.Payload, "duration")

	t.mu.Lock()
	defer t.mu.Unlock()

	// Update existing call if found.
	for i := len(t.calls) - 1; i >= 0; i-- {
		if t.calls[i].ID == callID {
			t.calls[i].Status = "completed"
			t.calls[i].Output = getMap(evt.Payload, "output")
			t.calls[i].Duration = duration
			return
		}
	}

	// Not found — create a new completed record.
	call := monitoring.MCPToolCall{
		ID:        evt.ID,
		AgentID:   getString(evt.Payload, "agent_id"),
		ToolName:  getString(evt.Payload, "tool_name"),
		Output:    getMap(evt.Payload, "output"),
		Status:    "completed",
		Duration:  duration,
		Timestamp: evt.Timestamp,
	}
	if call.AgentID == "" {
		call.AgentID = evt.ModuleName
	}
	if len(t.calls) >= maxToolCalls {
		t.calls = t.calls[1:]
	}
	t.calls = append(t.calls, call)
}

// toolExists checks if a tool with the given name is already registered.
// Caller must hold t.mu.
func (t *MCPTab) toolExists(name string) bool {
	for _, td := range t.tools {
		if td.Name == name {
			return true
		}
	}
	return false
}

// Trim retains at most maxLen tool calls, discarding the oldest.
func (t *MCPTab) Trim(maxLen int) {
	if maxLen <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.calls) > maxLen {
		t.calls = t.calls[len(t.calls)-maxLen:]
	}
}
