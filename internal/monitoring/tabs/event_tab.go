package tabs

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

const (
	// maxEvents is the maximum number of events retained in the EventTab.
	maxEvents = 1000
	// snapshotLimit is the number of recent events returned by Snapshot.
	snapshotLimit = 100
)

// EventTabSnapshot is the snapshot payload returned by EventTab.Snapshot.
type EventTabSnapshot struct {
	Events []*ares_events.Event `json:"events"`
	Total  int                  `json:"total"`
}

// EventTab implements the Tab interface for the Events tab.
// It retains all incoming events in a capped circular buffer.
type EventTab struct {
	mu     sync.RWMutex
	events []*ares_events.Event
}

// NewEventTab creates a new EventTab instance.
func NewEventTab() *EventTab {
	return &EventTab{
		events: make([]*ares_events.Event, 0, maxEvents),
	}
}

// Name returns the tab identifier.
func (t *EventTab) Name() string { return "events" }

// Label returns the human-readable tab name.
func (t *EventTab) Label() string { return "Events" }

// HandleEvent processes an incoming event by appending it to the event list.
func (t *EventTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) >= maxEvents {
		// Drop the oldest event to stay within the cap.
		t.events = t.events[1:]
	}
	t.events = append(t.events, evt)
}

// Snapshot returns the most recent events (up to snapshotLimit).
func (t *EventTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()
	total := len(t.events)
	start := 0
	if total > snapshotLimit {
		start = total - snapshotLimit
	}
	result := make([]*ares_events.Event, total-start)
	copy(result, t.events[start:])
	return EventTabSnapshot{
		Events: result,
		Total:  total,
	}
}

// FilterByType returns events matching the given event type string.
func (t *EventTab) FilterByType(eventType ares_events.EventType) []*ares_events.Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []*ares_events.Event
	for _, ev := range t.events {
		if ev.Type == eventType {
			result = append(result, ev)
		}
	}
	return result
}

// FilterByAgent returns events whose ModuleName matches the given agent ID.
func (t *EventTab) FilterByAgent(agentID string) []*ares_events.Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []*ares_events.Event
	for _, ev := range t.events {
		if ev.ModuleName == agentID {
			result = append(result, ev)
		}
	}
	return result
}
