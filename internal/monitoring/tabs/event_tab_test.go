package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestEventTab_Interface(t *testing.T) {
	var tab Tab = NewEventTab()
	if tab.Name() != "events" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "events")
	}
	if tab.Label() != "Events" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "Events")
	}
}

func TestEventTab_HandleEvent(t *testing.T) {
	tests := []struct {
		name      string
		events    []*ares_events.Event
		wantCount int
	}{
		{
			name:      "nil event is ignored",
			events:    []*ares_events.Event{nil},
			wantCount: 0,
		},
		{
			name: "single event",
			events: []*ares_events.Event{
				{ID: "1", Type: ares_events.EventAgentStarted, ModuleName: "agent-1", Payload: map[string]any{}, Timestamp: time.Now()},
			},
			wantCount: 1,
		},
		{
			name: "multiple events",
			events: []*ares_events.Event{
				{ID: "1", Type: ares_events.EventAgentStarted, ModuleName: "agent-1", Payload: map[string]any{}, Timestamp: time.Now()},
				{ID: "2", Type: ares_events.EventTaskCreated, ModuleName: "agent-1", Payload: map[string]any{}, Timestamp: time.Now()},
				{ID: "3", Type: ares_events.EventLLMCall, ModuleName: "agent-2", Payload: map[string]any{}, Timestamp: time.Now()},
			},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := NewEventTab()
			for _, evt := range tt.events {
				tab.HandleEvent(evt)
			}
			snap := tab.Snapshot().(EventTabSnapshot)
			if len(snap.Events) != tt.wantCount {
				t.Errorf("got %d events, want %d", len(snap.Events), tt.wantCount)
			}
			if snap.Total != tt.wantCount {
				t.Errorf("Total = %d, want %d", snap.Total, tt.wantCount)
			}
		})
	}
}

func TestEventTab_Capacity(t *testing.T) {
	tab := NewEventTab()
	for i := 0; i < maxEvents+50; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:         fmt.Sprintf("evt-%d", i),
			Type:       ares_events.EventAgentStarted,
			ModuleName: "agent-1",
			Payload:    map[string]any{},
			Timestamp:  time.Now(),
		})
	}
	// Should be capped at maxEvents, not maxEvents+50.
	tab.mu.RLock()
	count := len(tab.events)
	tab.mu.RUnlock()
	if count != maxEvents {
		t.Errorf("event count = %d, want %d", count, maxEvents)
	}
}

func TestEventTab_Snapshot_Limit(t *testing.T) {
	tab := NewEventTab()
	for i := 0; i < 200; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("evt-%d", i),
			Type:      ares_events.EventAgentStarted,
			Payload:   map[string]any{},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(EventTabSnapshot)
	if len(snap.Events) != snapshotLimit {
		t.Errorf("snapshot has %d events, want %d", len(snap.Events), snapshotLimit)
	}
	if snap.Total != 200 {
		t.Errorf("Total = %d, want %d", snap.Total, 200)
	}
}

func TestEventTab_FilterByType(t *testing.T) {
	tab := NewEventTab()
	events := []*ares_events.Event{
		{ID: "1", Type: ares_events.EventAgentStarted, Payload: map[string]any{}, Timestamp: time.Now()},
		{ID: "2", Type: ares_events.EventTaskCreated, Payload: map[string]any{}, Timestamp: time.Now()},
		{ID: "3", Type: ares_events.EventAgentStarted, Payload: map[string]any{}, Timestamp: time.Now()},
		{ID: "4", Type: ares_events.EventLLMCall, Payload: map[string]any{}, Timestamp: time.Now()},
	}
	for _, evt := range events {
		tab.HandleEvent(evt)
	}

	tests := []struct {
		name      string
		eventType string
		wantCount int
	}{
		{name: "filter agent.started", eventType: "agent.started", wantCount: 2},
		{name: "filter task.created", eventType: "task.created", wantCount: 1},
		{name: "filter llm.call", eventType: "llm.call", wantCount: 1},
		{name: "filter nonexistent", eventType: "nonexistent", wantCount: 0},
		{name: "filter empty string", eventType: "", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tab.FilterByType(tt.eventType)
			if len(result) != tt.wantCount {
				t.Errorf("FilterByType(%q) returned %d events, want %d", tt.eventType, len(result), tt.wantCount)
			}
		})
	}
}

func TestEventTab_FilterByAgent(t *testing.T) {
	tab := NewEventTab()
	events := []*ares_events.Event{
		{ID: "1", Type: ares_events.EventAgentStarted, ModuleName: "agent-1", Payload: map[string]any{}, Timestamp: time.Now()},
		{ID: "2", Type: ares_events.EventTaskCreated, ModuleName: "agent-2", Payload: map[string]any{}, Timestamp: time.Now()},
		{ID: "3", Type: ares_events.EventAgentStarted, ModuleName: "agent-1", Payload: map[string]any{}, Timestamp: time.Now()},
	}
	for _, evt := range events {
		tab.HandleEvent(evt)
	}

	tests := []struct {
		name      string
		agentID   string
		wantCount int
	}{
		{name: "filter agent-1", agentID: "agent-1", wantCount: 2},
		{name: "filter agent-2", agentID: "agent-2", wantCount: 1},
		{name: "filter nonexistent agent", agentID: "agent-99", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tab.FilterByAgent(tt.agentID)
			if len(result) != tt.wantCount {
				t.Errorf("FilterByAgent(%q) returned %d events, want %d", tt.agentID, len(result), tt.wantCount)
			}
		})
	}
}

func TestEventTab_SummaryFromPayload(t *testing.T) {
	tab := NewEventTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"summary": "Agent booted up"},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(EventTabSnapshot)
	if len(snap.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(snap.Events))
	}
	if snap.Events[0].Summary != "Agent booted up" {
		t.Errorf("Summary = %q, want %q", snap.Events[0].Summary, "Agent booted up")
	}
}
