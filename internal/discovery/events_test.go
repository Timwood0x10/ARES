package discovery

import (
	"testing"
	"time"
)

func TestEventHandlerFunc(t *testing.T) {
	var received Event
	handler := EventHandlerFunc(func(evt Event) {
		received = evt
	})

	evt := Event{
		Type:      EventServiceAdded,
		ServiceID: "test-service",
		Message:   "test message",
		Timestamp: time.Now(),
	}

	handler.HandleDiscoveryEvent(evt)

	if received.Type != EventServiceAdded {
		t.Errorf("expected type %s, got %s", EventServiceAdded, received.Type)
	}
	if received.ServiceID != "test-service" {
		t.Errorf("expected service ID 'test-service', got %q", received.ServiceID)
	}
	if received.Message != "test message" {
		t.Errorf("expected message 'test message', got %q", received.Message)
	}
}

func TestEventTypes(t *testing.T) {
	// Verify all event types are defined.
	types := []EventType{
		EventServiceAdded,
		EventServiceRemoved,
		EventServiceUpdated,
		EventHealthChanged,
		EventDiscoveryComplete,
	}

	for _, et := range types {
		if et == "" {
			t.Error("event type should not be empty")
		}
	}

	// Verify uniqueness.
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestEventWithService(t *testing.T) {
	svc := &DiscoveredService{
		Identity: ServiceIdentity{
			ID:   "test",
			Name: "test-service",
		},
	}

	evt := Event{
		Type:      EventServiceAdded,
		ServiceID: "test",
		Service:   svc,
		Timestamp: time.Now(),
	}

	if evt.Service == nil {
		t.Fatal("expected non-nil service")
	}
	if evt.Service.Identity.Name != "test-service" {
		t.Errorf("expected name 'test-service', got %q", evt.Service.Identity.Name)
	}
}
