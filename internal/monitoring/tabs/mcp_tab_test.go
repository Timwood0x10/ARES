package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
)

func TestMCPTab_Interface(t *testing.T) {
	var tab Tab = NewMCPTab()
	assert.Equal(t, "mcp", tab.Name())
	assert.Equal(t, "MCP", tab.Label())
}

func TestMCPTab_ToolCallStarted(t *testing.T) {
	tab := NewMCPTab()
	tab.HandleEvent(&ares_events.Event{
		ID:   "c1",
		Type: ares_events.EventToolCallStarted,
		Payload: map[string]any{
			"agent_id":         "a1",
			"tool_name":        "search",
			"tool_description": "web search",
			"input":            map[string]any{"query": "hello"},
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(MCPTabSnapshot)
	assert.Len(t, snap.Calls, 1)
	assert.Equal(t, "running", snap.Calls[0].Status)
	assert.Equal(t, "search", snap.Calls[0].ToolName)
	assert.Len(t, snap.Tools, 1)
	assert.Equal(t, "search", snap.Tools[0].Name)
}

func TestMCPTab_ToolCallCompleted(t *testing.T) {
	tab := NewMCPTab()
	// Start then complete.
	tab.HandleEvent(&ares_events.Event{
		ID:   "c1",
		Type: ares_events.EventToolCallStarted,
		Payload: map[string]any{
			"agent_id":  "a1",
			"tool_name": "search",
		},
		Timestamp: time.Now(),
	})
	tab.HandleEvent(&ares_events.Event{
		ID:   "c2",
		Type: ares_events.EventToolCallCompleted,
		Payload: map[string]any{
			"call_id":  "c1",
			"output":   map[string]any{"results": 5},
			"duration": float64(150000000), // 150ms in nanoseconds
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(MCPTabSnapshot)
	assert.Equal(t, "completed", snap.Calls[0].Status)
	assert.NotZero(t, snap.Calls[0].Duration)
}

func TestMCPTab_CompleteWithoutStart(t *testing.T) {
	tab := NewMCPTab()
	// Completing a call that was never started should still add a record.
	tab.HandleEvent(&ares_events.Event{
		ID:   "c1",
		Type: ares_events.EventToolCallCompleted,
		Payload: map[string]any{
			"agent_id":  "a1",
			"tool_name": "fetch",
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(MCPTabSnapshot)
	assert.Len(t, snap.Calls, 1)
	assert.Equal(t, "completed", snap.Calls[0].Status)
}

func TestMCPTab_DeduplicateTools(t *testing.T) {
	tab := NewMCPTab()
	for i := 0; i < 5; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("c%d", i),
			Type:      ares_events.EventToolCallStarted,
			Payload:   map[string]any{"tool_name": "search"},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(MCPTabSnapshot)
	assert.Equal(t, 1, len(snap.Tools))
	assert.Equal(t, 5, len(snap.Calls))
}

func TestMCPTab_NilEvent(t *testing.T) {
	tab := NewMCPTab()
	tab.HandleEvent(nil)
}

func TestMCPTab_Capacity(t *testing.T) {
	tab := NewMCPTab()
	for i := 0; i < maxToolCalls+10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("c%d", i),
			Type:      ares_events.EventToolCallStarted,
			Payload:   map[string]any{"tool_name": "t"},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(MCPTabSnapshot)
	assert.Equal(t, maxToolCalls, len(snap.Calls))
}

func TestMCPTab_IgnoresIrrelevantEvents(t *testing.T) {
	tab := NewMCPTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(MCPTabSnapshot)
	assert.Empty(t, snap.Calls)
}
