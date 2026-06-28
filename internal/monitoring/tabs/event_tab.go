package tabs

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
)

const (
	// maxEvents is the maximum number of events retained in the EventTab.
	maxEvents = 1000
	// snapshotLimit is the number of recent events returned by Snapshot.
	snapshotLimit = 100
)

// EventTabSnapshot is the snapshot payload returned by EventTab.Snapshot.
type EventTabSnapshot struct {
	Events []monitoring.EventView `json:"events"`
	Total  int                    `json:"total"`
}

// EventTab implements the Tab interface for the Events tab.
// It retains all incoming events in a capped circular buffer.
type EventTab struct {
	mu     sync.RWMutex
	events []monitoring.EventView
}

// NewEventTab creates a new EventTab instance.
func NewEventTab() *EventTab {
	return &EventTab{
		events: make([]monitoring.EventView, 0, maxEvents),
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
	view := eventToView(evt)
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) >= maxEvents {
		// Drop the oldest event to stay within the cap.
		t.events = t.events[1:]
	}
	t.events = append(t.events, view)
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
	result := make([]monitoring.EventView, total-start)
	copy(result, t.events[start:])
	return EventTabSnapshot{
		Events: result,
		Total:  total,
	}
}

// FilterByType returns copies of events matching the given event type string.
func (t *EventTab) FilterByType(eventType string) []monitoring.EventView {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []monitoring.EventView
	for _, ev := range t.events {
		if ev.Type == eventType {
			result = append(result, copyEventView(ev))
		}
	}
	return result
}

// FilterByAgent returns copies of events whose Source matches the given agent ID.
func (t *EventTab) FilterByAgent(agentID string) []monitoring.EventView {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []monitoring.EventView
	for _, ev := range t.events {
		if ev.Source == agentID {
			result = append(result, copyEventView(ev))
		}
	}
	return result
}

// copyEventView creates a deep copy of an EventView, including its Details map.
func copyEventView(ev monitoring.EventView) monitoring.EventView {
	cp := ev
	if ev.Details != nil {
		cp.Details = make(map[string]any, len(ev.Details))
		for k, v := range ev.Details {
			cp.Details[k] = v
		}
	}
	return cp
}
