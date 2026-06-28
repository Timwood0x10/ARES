package tabs

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
)

const (
	maxDistillations = 500
	maxRetrievals    = 500
)

// MemoryTabSnapshot is the snapshot payload returned by MemoryTab.Snapshot.
type MemoryTabSnapshot struct {
	Distillations []monitoring.MemoryRecord `json:"distillations"`
	Retrievals    []monitoring.MemoryRecord `json:"retrievals"`
}

// MemoryTab implements the Tab interface for the Memory tab.
// It tracks memory distillation and retrieval events.
type MemoryTab struct {
	mu            sync.RWMutex
	distillations []monitoring.MemoryRecord
	retrievals    []monitoring.MemoryRecord
}

// NewMemoryTab creates a new MemoryTab instance.
func NewMemoryTab() *MemoryTab {
	return &MemoryTab{
		distillations: make([]monitoring.MemoryRecord, 0, maxDistillations),
		retrievals:    make([]monitoring.MemoryRecord, 0, maxRetrievals),
	}
}

// Name returns the tab identifier.
func (t *MemoryTab) Name() string { return "memory" }

// Label returns the human-readable tab name.
func (t *MemoryTab) Label() string { return "Memory" }

// HandleEvent processes memory-related events.
func (t *MemoryTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case ares_events.EventMemoryDistilled:
		t.handleDistilled(evt)
	case eventMemoryRetrieved:
		t.handleRetrieved(evt)
	}
}

// Snapshot returns the current memory state.
func (t *MemoryTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	dist := make([]monitoring.MemoryRecord, len(t.distillations))
	copy(dist, t.distillations)
	ret := make([]monitoring.MemoryRecord, len(t.retrievals))
	copy(ret, t.retrievals)

	return MemoryTabSnapshot{
		Distillations: dist,
		Retrievals:    ret,
	}
}

// Distillations returns a copy of all distillation records.
func (t *MemoryTab) Distillations() []monitoring.MemoryRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]monitoring.MemoryRecord, len(t.distillations))
	copy(out, t.distillations)
	return out
}

// Retrievals returns a copy of all retrieval records.
func (t *MemoryTab) Retrievals() []monitoring.MemoryRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]monitoring.MemoryRecord, len(t.retrievals))
	copy(out, t.retrievals)
	return out
}

// Trim retains at most maxLen entries in each list, discarding the oldest.
func (t *MemoryTab) Trim(maxLen int) {
	if maxLen <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.distillations) > maxLen {
		t.distillations = t.distillations[len(t.distillations)-maxLen:]
	}
	if len(t.retrievals) > maxLen {
		t.retrievals = t.retrievals[len(t.retrievals)-maxLen:]
	}
}

func (t *MemoryTab) handleDistilled(evt *ares_events.Event) {
	record := memoryRecordFromEvent(evt, "distilled")
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.distillations) >= maxDistillations {
		t.distillations = t.distillations[1:]
	}
	t.distillations = append(t.distillations, record)
}

func (t *MemoryTab) handleRetrieved(evt *ares_events.Event) {
	record := memoryRecordFromEvent(evt, "retrieved")
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.retrievals) >= maxRetrievals {
		t.retrievals = t.retrievals[1:]
	}
	t.retrievals = append(t.retrievals, record)
}

// memoryRecordFromEvent builds a MemoryRecord from an event payload.
func memoryRecordFromEvent(evt *ares_events.Event, category string) monitoring.MemoryRecord {
	return monitoring.MemoryRecord{
		ID:        evt.ID,
		AgentID:   getString(evt.Payload, "agent_id"),
		Category:  category,
		Content:   getString(evt.Payload, "content"),
		Relevance: getFloat64(evt.Payload, "relevance"),
		CreatedAt: evt.Timestamp,
	}
}
