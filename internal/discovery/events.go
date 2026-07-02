package discovery

import "time"

// EventType classifies discovery events.
type EventType string

const (
	EventServiceAdded      EventType = "discovery.service.added"
	EventServiceRemoved    EventType = "discovery.service.removed"
	EventServiceUpdated    EventType = "discovery.service.updated"
	EventHealthChanged     EventType = "discovery.health.changed"
	EventDiscoveryComplete EventType = "discovery.cycle.complete"
)

// Event is emitted by the Engine when something changes.
type Event struct {
	Type      EventType          `json:"type"`
	ServiceID string             `json:"service_id"`
	Service   *DiscoveredService `json:"service,omitempty"`
	Source    string             `json:"source,omitempty"`
	Message   string             `json:"message,omitempty"`
	Timestamp time.Time          `json:"timestamp"`
}

// EventHandler processes discovery events.
type EventHandler interface {
	HandleDiscoveryEvent(evt Event)
}

// EventHandlerFunc is a function adapter for EventHandler.
type EventHandlerFunc func(Event)

func (f EventHandlerFunc) HandleDiscoveryEvent(evt Event) {
	f(evt)
}
