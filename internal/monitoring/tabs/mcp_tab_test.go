package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestMCPTab_Interface(t *testing.T) {
	var tab Tab = NewMCPTab()
	if tab.Name() != "mcp" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "mcp")
	}
	if tab.Label() != "MCP" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "MCP")
	}
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
	if len(snap.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(snap.Calls))
	}
	if snap.Calls[0].Status != "running" {
		t.Errorf("Status = %q, want %q", snap.Calls[0].Status, "running")
	}
	if snap.Calls[0].ToolName != "search" {
		t.Errorf("ToolName = %q, want %q", snap.Calls[0].ToolName, "search")
	}
	// Tool should be registered.
	if len(snap.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(snap.Tools))
	}
	if snap.Tools[0].Name != "search" {
		t.Errorf("Tool name = %q, want %q", snap.Tools[0].Name, "search")
	}
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
	if snap.Calls[0].Status != "completed" {
		t.Errorf("Status = %q, want %q", snap.Calls[0].Status, "completed")
	}
	if snap.Calls[0].Duration == 0 {
		t.Error("Duration should be set after completion")
	}
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
	if len(snap.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(snap.Calls))
	}
	if snap.Calls[0].Status != "completed" {
		t.Errorf("Status = %q, want %q", snap.Calls[0].Status, "completed")
	}
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
	if len(snap.Tools) != 1 {
		t.Errorf("tool count = %d, want 1 (deduplicated)", len(snap.Tools))
	}
	if len(snap.Calls) != 5 {
		t.Errorf("call count = %d, want 5", len(snap.Calls))
	}
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
	if len(snap.Calls) != maxToolCalls {
		t.Errorf("call count = %d, want %d", len(snap.Calls), maxToolCalls)
	}
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
	if len(snap.Calls) != 0 {
		t.Error("non-MCP events should be ignored")
	}
}
