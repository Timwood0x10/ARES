package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestMemoryTab_Interface(t *testing.T) {
	var tab Tab = NewMemoryTab()
	if tab.Name() != "memory" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "memory")
	}
	if tab.Label() != "Memory" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "Memory")
	}
}

func TestMemoryTab_HandleDistilled(t *testing.T) {
	tab := NewMemoryTab()
	evt := &ares_events.Event{
		ID:        "d1",
		Type:      ares_events.EventMemoryDistilled,
		Payload:   map[string]any{"agent_id": "a1", "content": "learned X", "relevance": 0.9},
		Timestamp: time.Now(),
	}
	tab.HandleEvent(evt)

	snap := tab.Snapshot().(MemoryTabSnapshot)
	if len(snap.Distillations) != 1 {
		t.Fatalf("got %d distillations, want 1", len(snap.Distillations))
	}
	if snap.Distillations[0].AgentID != "a1" {
		t.Errorf("AgentID = %q, want %q", snap.Distillations[0].AgentID, "a1")
	}
	if snap.Distillations[0].Content != "learned X" {
		t.Errorf("Content = %q, want %q", snap.Distillations[0].Content, "learned X")
	}
	if snap.Distillations[0].Relevance != 0.9 {
		t.Errorf("Relevance = %f, want %f", snap.Distillations[0].Relevance, 0.9)
	}
	if snap.Distillations[0].Category != "distilled" {
		t.Errorf("Category = %q, want %q", snap.Distillations[0].Category, "distilled")
	}
}

func TestMemoryTab_HandleRetrieved(t *testing.T) {
	tab := NewMemoryTab()
	evt := &ares_events.Event{
		ID:        "r1",
		Type:      eventMemoryRetrieved,
		Payload:   map[string]any{"agent_id": "a1", "content": "recalled Y", "relevance": 0.7},
		Timestamp: time.Now(),
	}
	tab.HandleEvent(evt)

	snap := tab.Snapshot().(MemoryTabSnapshot)
	if len(snap.Retrievals) != 1 {
		t.Fatalf("got %d retrievals, want 1", len(snap.Retrievals))
	}
	if snap.Retrievals[0].Category != "retrieved" {
		t.Errorf("Category = %q, want %q", snap.Retrievals[0].Category, "retrieved")
	}
}

func TestMemoryTab_IgnoresIrrelevantEvents(t *testing.T) {
	tab := NewMemoryTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(MemoryTabSnapshot)
	if len(snap.Distillations) != 0 || len(snap.Retrievals) != 0 {
		t.Error("non-memory events should be ignored")
	}
}

func TestMemoryTab_NilEvent(t *testing.T) {
	tab := NewMemoryTab()
	tab.HandleEvent(nil) // should not panic
}

func TestMemoryTab_Capacity(t *testing.T) {
	tab := NewMemoryTab()
	for i := 0; i < maxDistillations+10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("d%d", i),
			Type:      ares_events.EventMemoryDistilled,
			Payload:   map[string]any{"agent_id": "a1", "content": "x"},
			Timestamp: time.Now(),
		})
	}
	dist := tab.Distillations()
	if len(dist) != maxDistillations {
		t.Errorf("distillation count = %d, want %d", len(dist), maxDistillations)
	}
}

func TestMemoryTab_EmptySnapshot(t *testing.T) {
	tab := NewMemoryTab()
	snap := tab.Snapshot().(MemoryTabSnapshot)
	if len(snap.Distillations) != 0 {
		t.Errorf("distillations = %d, want 0", len(snap.Distillations))
	}
	if len(snap.Retrievals) != 0 {
		t.Errorf("retrievals = %d, want 0", len(snap.Retrievals))
	}
}

func TestMemoryTab_MissingPayloadFields(t *testing.T) {
	tab := NewMemoryTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "d1",
		Type:      ares_events.EventMemoryDistilled,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	dist := tab.Distillations()
	if len(dist) != 1 {
		t.Fatalf("got %d, want 1", len(dist))
	}
	// Should have empty strings, not panic.
	if dist[0].AgentID != "" {
		t.Errorf("AgentID = %q, want empty", dist[0].AgentID)
	}
}
