package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
)

func TestMemoryTab_Interface(t *testing.T) {
	var tab Tab = NewMemoryTab()
	assert.Equal(t, "memory", tab.Name())
	assert.Equal(t, "Memory", tab.Label())
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
	assert.Len(t, snap.Distillations, 1)
	assert.Equal(t, "a1", snap.Distillations[0].AgentID)
	assert.Equal(t, "learned X", snap.Distillations[0].Content)
	assert.InDelta(t, 0.9, snap.Distillations[0].Relevance, 0.0001)
	assert.Equal(t, "distilled", snap.Distillations[0].Category)
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
	assert.Len(t, snap.Retrievals, 1)
	assert.Equal(t, "retrieved", snap.Retrievals[0].Category)
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
	assert.Empty(t, snap.Distillations)
	assert.Empty(t, snap.Retrievals)
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
	assert.Equal(t, maxDistillations, len(dist))
}

func TestMemoryTab_EmptySnapshot(t *testing.T) {
	tab := NewMemoryTab()
	snap := tab.Snapshot().(MemoryTabSnapshot)
	assert.Empty(t, snap.Distillations)
	assert.Empty(t, snap.Retrievals)
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
	assert.Len(t, dist, 1)
	assert.Empty(t, dist[0].AgentID)
}
