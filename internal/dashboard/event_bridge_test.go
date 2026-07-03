package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestNewEventBridge(t *testing.T) {
	store := &mockEventStore{}
	hub := NewWSHub()

	bridge := NewEventBridge(store, hub, nil)
	if bridge == nil {
		t.Fatal("NewEventBridge returned nil")
	}
}

func TestEventBridgeStartNilStore(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	// NewEventBridge with nil eventStore should not panic on construction.
	// Start should fail because Subscribe will be called on nil.
	bridge := NewEventBridge(nil, hub, nil)

	// Calling Start on a bridge with nil eventStore should panic or error.
	// We test that it doesn't hang; a panic is acceptable here.
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Start with nil store panicked (expected): %v", r)
		}
	}()

	err := bridge.Start(context.Background())
	if err == nil {
		// If Start succeeded, the bridge somehow handled nil; stop it.
		bridge.Stop()
	}
}

func TestEventBridgeHandleEventAgentStarted(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:        "e1",
		StreamID:  "agent-1",
		Type:      ares_events.EventAgentStarted,
		Payload:   nil,
		Version:   1,
		Timestamp: time.Now(),
	}

	// Should not panic.
	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventAgentStopped(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:        "e2",
		StreamID:  "agent-1",
		Type:      ares_events.EventAgentStopped,
		Payload:   nil,
		Version:   2,
		Timestamp: time.Now(),
	}

	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventFailoverTriggered(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:        "e3",
		StreamID:  "agent-1",
		Type:      ares_events.EventFailoverTriggered,
		Payload:   nil,
		Version:   3,
		Timestamp: time.Now(),
	}

	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventFailoverCompleted(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:        "e4",
		StreamID:  "agent-1",
		Type:      ares_events.EventFailoverCompleted,
		Payload:   nil,
		Version:   4,
		Timestamp: time.Now(),
	}

	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventTaskCreated(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:       "e5",
		StreamID: "workflow-1",
		Type:     ares_events.EventTaskCreated,
		Payload: map[string]any{
			"execution_id": "exec-123",
		},
		Version:   1,
		Timestamp: time.Now(),
	}

	// Should route to workflow channel.
	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventTaskCompleted(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:       "e6",
		StreamID: "workflow-1",
		Type:     ares_events.EventTaskCompleted,
		Payload: map[string]any{
			"workflow_id": "wf-abc",
		},
		Version:   2,
		Timestamp: time.Now(),
	}

	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventTaskFailed(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:       "e7",
		StreamID: "workflow-1",
		Type:     ares_events.EventTaskFailed,
		Payload: map[string]any{
			"task_id": "task-456",
		},
		Version:   3,
		Timestamp: time.Now(),
	}

	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventTaskWithoutExecutionID(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:        "e8",
		StreamID:  "workflow-1",
		Type:      ares_events.EventTaskCreated,
		Payload:   map[string]any{"other_key": "value"},
		Version:   4,
		Timestamp: time.Now(),
	}

	// Should not panic even without execution ID.
	bridge.handleEvent(evt)
}

func TestEventBridgeHandleEventUnknownType(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	store := &mockEventStore{}
	bridge := NewEventBridge(store, hub, nil)

	evt := &ares_events.Event{
		ID:        "e9",
		StreamID:  "stream-1",
		Type:      ares_events.EventLLMCall,
		Payload:   nil,
		Version:   1,
		Timestamp: time.Now(),
	}

	// Should broadcast to ares_events channel but not route to agents/workflow.
	bridge.handleEvent(evt)
}

func TestEventBridgeStartStop(t *testing.T) {
	now := time.Now()
	store := &mockEventStore{
		ares_events: []*ares_events.Event{
			{ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted, Timestamp: now},
		},
	}

	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	bridge := NewEventBridge(store, hub, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the forward loop time to process.
	time.Sleep(50 * time.Millisecond)

	// Stop should complete without hanging.
	bridge.Stop()
}

func TestExtractExecutionID(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{
			name:    "nil payload",
			payload: nil,
			want:    "",
		},
		{
			name:    "empty payload",
			payload: map[string]any{},
			want:    "",
		},
		{
			name:    "execution_id present",
			payload: map[string]any{"execution_id": "exec-123"},
			want:    "exec-123",
		},
		{
			name:    "workflow_id present",
			payload: map[string]any{"workflow_id": "wf-456"},
			want:    "wf-456",
		},
		{
			name:    "task_id present",
			payload: map[string]any{"task_id": "task-789"},
			want:    "task-789",
		},
		{
			name:    "execution_id takes priority",
			payload: map[string]any{"execution_id": "exec-1", "workflow_id": "wf-2", "task_id": "task-3"},
			want:    "exec-1",
		},
		{
			name:    "non-string execution_id ignored",
			payload: map[string]any{"execution_id": 12345},
			want:    "",
		},
		{
			name:    "empty string execution_id ignored",
			payload: map[string]any{"execution_id": ""},
			want:    "",
		},
		{
			name:    "unrelated keys only",
			payload: map[string]any{"foo": "bar", "baz": 42},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &ares_events.Event{
				Payload: tt.payload,
			}
			got := extractExecutionID(evt)
			if got != tt.want {
				t.Errorf("extractExecutionID() = %q, want %q", got, tt.want)
			}
		})
	}
}
