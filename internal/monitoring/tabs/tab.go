// Package tabs implements component-level tabs for the ARES Console
// monitoring plugin. Each tab processes a subset of events and provides
// a focused snapshot for its domain.
package tabs

import (
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// Tab is the interface that all monitoring tabs must implement.
// Each tab receives events via HandleEvent and produces snapshots via Snapshot.
type Tab interface {
	// Name returns the unique identifier for this tab.
	Name() string
	// Label returns the human-readable display name.
	Label() string
	// HandleEvent processes an incoming event.
	HandleEvent(evt *ares_events.Event)
	// Snapshot returns the current state of the tab.
	Snapshot() any
}

// Custom event types not defined in ares_events but used by tabs.
// These will be promoted to ares_events when the subsystems are integrated.
const (
	eventMemoryRetrieved      ares_events.EventType = "memory.retrieved"
	eventEvolutionMutated     ares_events.EventType = "evolution.mutated"
	eventEvolutionRecommended ares_events.EventType = "evolution.recommended"
)

// getString safely extracts a string value from a map.
func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getInt64 safely extracts a numeric value from a map as int64.
func getInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// getFloat64 safely extracts a float64 value from a map.
func getFloat64(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

// getMap safely extracts a nested map from a map.
func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

// getDuration safely extracts a duration from payload fields.
func getDuration(m map[string]any, key string) time.Duration {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case time.Duration:
		return v
	case float64:
		return time.Duration(v)
	case int64:
		return time.Duration(v)
	case int:
		return time.Duration(v)
	default:
		return 0
	}
}
