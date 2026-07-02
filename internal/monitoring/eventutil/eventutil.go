// Package eventutil provides shared helpers for extracting typed values
// from ares_events.Event payloads. These functions replace duplicated
// helpers previously scattered across monitoring sub-packages.
package eventutil

import (
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// ExtractString reads a string value from the event payload by key.
// Returns "" if the payload is nil, the key is missing, or the value
// is not a string.
func ExtractString(evt *ares_events.Event, key string) string {
	if evt == nil || evt.Payload == nil {
		return ""
	}
	v, ok := evt.Payload[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// ExtractInt64 reads an int64 from the event payload by key.
// Handles int64, float64 (JSON default), and int values.
func ExtractInt64(evt *ares_events.Event, key string) int64 {
	if evt == nil || evt.Payload == nil {
		return 0
	}
	v, ok := evt.Payload[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	default:
		return 0
	}
}

// ExtractFloat64 reads a float64 from the event payload by key.
// Handles float64, int64, and int values.
func ExtractFloat64(evt *ares_events.Event, key string) float64 {
	if evt == nil || evt.Payload == nil {
		return 0
	}
	v, ok := evt.Payload[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

// ExtractDuration reads a time.Duration from the event payload by key.
// Handles time.Duration, float64 (nanoseconds), and int64 (nanoseconds).
func ExtractDuration(evt *ares_events.Event, key string) time.Duration {
	if evt == nil || evt.Payload == nil {
		return 0
	}
	v, ok := evt.Payload[key]
	if !ok {
		return 0
	}
	switch d := v.(type) {
	case time.Duration:
		return d
	case float64:
		return time.Duration(d)
	case int64:
		return time.Duration(d)
	default:
		return 0
	}
}

// ExtractMapString reads a string value from a map by key.
// Returns "" if the map is nil, the key is missing, or the value
// is not a string.
func ExtractMapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// ExtractMapInt64 reads an int64 from a map by key.
// Handles int64, float64, and int values.
func ExtractMapInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	default:
		return 0
	}
}

// ExtractMapFloat64 reads a float64 from a map by key.
// Handles float64, int64, and int values.
func ExtractMapFloat64(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

// ExtractAgentID reads agent_id from the event payload, falling back to StreamID.
func ExtractAgentID(evt *ares_events.Event) string {
	if id := ExtractString(evt, "agent_id"); id != "" {
		return id
	}
	return evt.StreamID
}
