package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
)

func TestEventTab_Interface(t *testing.T) {
	var tab Tab = NewEventTab()
	assert.Equal(t, "events", tab.Name())
	assert.Equal(t, "Events", tab.Label())
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
			assert.Equal(t, tt.wantCount, len(snap.Events))
			assert.Equal(t, tt.wantCount, snap.Total)
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
	tab.mu.RLock()
	count := len(tab.events)
	tab.mu.RUnlock()
	assert.Equal(t, maxEvents, count)
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
	assert.Equal(t, snapshotLimit, len(snap.Events))
	assert.Equal(t, 200, snap.Total)
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
		eventType ares_events.EventType
		wantCount int
	}{
		{name: "filter agent.started", eventType: ares_events.EventAgentStarted, wantCount: 2},
		{name: "filter task.created", eventType: ares_events.EventTaskCreated, wantCount: 1},
		{name: "filter llm.call", eventType: ares_events.EventLLMCall, wantCount: 1},
		{name: "filter nonexistent", eventType: "nonexistent", wantCount: 0},
		{name: "filter empty string", eventType: "", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tab.FilterByType(tt.eventType)
			assert.Equal(t, tt.wantCount, len(result))
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
			assert.Equal(t, tt.wantCount, len(result))
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
	assert.Len(t, snap.Events, 1)
	// Summary is now in the event payload, not a top-level field.
	assert.Equal(t, "Agent booted up", snap.Events[0].Payload["summary"])
}

func TestEventTab_Trim(t *testing.T) {
	tab := NewEventTab()
	for i := 0; i < 50; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("evt-%d", i),
			Type:      ares_events.EventAgentStarted,
			Payload:   map[string]any{},
			Timestamp: time.Now(),
		})
	}
	assert.Equal(t, 50, len(tab.events))

	tab.Trim(20)
	assert.Equal(t, 20, len(tab.events))
	// Verify the most recent events are retained.
	assert.Equal(t, "evt-30", tab.events[0].ID)
	assert.Equal(t, "evt-49", tab.events[19].ID)
}

func TestEventTab_TrimNoop(t *testing.T) {
	tab := NewEventTab()
	for i := 0; i < 5; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID: fmt.Sprintf("evt-%d", i), Type: ares_events.EventAgentStarted,
			Payload: map[string]any{}, Timestamp: time.Now(),
		})
	}
	tab.Trim(10)
	assert.Equal(t, 5, len(tab.events))
}

func TestEventTab_TrimZero(t *testing.T) {
	tab := NewEventTab()
	for i := 0; i < 5; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID: fmt.Sprintf("evt-%d", i), Type: ares_events.EventAgentStarted,
			Payload: map[string]any{}, Timestamp: time.Now(),
		})
	}
	tab.Trim(0)
	assert.Equal(t, 5, len(tab.events))
}
