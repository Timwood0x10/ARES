package callbacks

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestEmitterInterfaceVerification verifies that Registry satisfies Emitter interface.
func TestEmitterInterfaceVerification(t *testing.T) {
	var e Emitter = NewRegistry()

	// Should be able to call Emit without panicking.
	e.Emit(&Context{Event: EventAgentStart})
}

// TestCallbackEventDataIntegrity verifies that all Context fields
// are properly populated during event emission.
func TestCallbackEventDataIntegrity(t *testing.T) {
	reg := NewRegistry()

	var received *Context
	testErr := errors.New("test error")

	reg.On(EventLLMError, func(ctx *Context) {
		received = ctx
	})

	reg.Emit(&Context{
		Event:      EventLLMError,
		AgentID:    "agent-test-1",
		ToolName:   "web_search",
		Model:      "gpt-4",
		Input:      "search query",
		Output:     "search results",
		Error:      testErr,
		Duration:   150 * time.Millisecond,
		TokenCount: 42,
		Extra:      map[string]any{"key": "value"},
	})

	if received == nil {
		t.Fatal("handler should have been called")
	}
	if received.Event != EventLLMError {
		t.Errorf("Event mismatch: got %s, want %s", received.Event, EventLLMError)
	}
	if received.AgentID != "agent-test-1" {
		t.Errorf("AgentID mismatch: got %s, want agent-test-1", received.AgentID)
	}
	if received.ToolName != "web_search" {
		t.Errorf("ToolName mismatch: got %s, want web_search", received.ToolName)
	}
	if received.Model != "gpt-4" {
		t.Errorf("Model mismatch: got %s, want gpt-4", received.Model)
	}
	if received.Input != "search query" {
		t.Errorf("Input mismatch: got %s, want search query", received.Input)
	}
	if received.Output != "search results" {
		t.Errorf("Output mismatch: got %s, want search results", received.Output)
	}
	if received.Error != testErr {
		t.Error("Error mismatch: expected test error")
	}
	if received.Duration != 150*time.Millisecond {
		t.Errorf("Duration mismatch: got %s, want 150ms", received.Duration)
	}
	if received.TokenCount != 42 {
		t.Errorf("TokenCount mismatch: got %d, want 42", received.TokenCount)
	}
	if received.Extra["key"] != "value" {
		t.Errorf("Extra mismatch: got %v, want value", received.Extra["key"])
	}
}

// TestMultipleComponentsShareRegistry verifies that multiple emitters
// can share the same registry and all events are captured.
func TestMultipleComponentsShareRegistry(t *testing.T) {
	reg := NewRegistry()

	var mu sync.Mutex
	eventCount := 0

	// Count all events from any source.
	reg.On(EventLLMStart, func(ctx *Context) {
		mu.Lock()
		defer mu.Unlock()
		eventCount++
	})
	reg.On(EventLLMError, func(ctx *Context) {
		mu.Lock()
		defer mu.Unlock()
		eventCount++
	})

	// Simulate two components emitting to the same registry.
	reg.Emit(&Context{Event: EventLLMStart})
	reg.Emit(&Context{Event: EventLLMError})
	reg.Emit(&Context{Event: EventLLMStart})
	reg.Emit(&Context{Event: EventLLMError})

	mu.Lock()
	finalCount := eventCount
	mu.Unlock()

	if finalCount != 4 {
		t.Fatalf("expected 4 events from 2 simulated components, got %d", finalCount)
	}
}

// TestNilContextEmit verifies that Emit with nil context is a no-op.
func TestNilContextEmit(t *testing.T) {
	reg := NewRegistry()

	called := false
	reg.On(EventLLMStart, func(ctx *Context) {
		called = true
	})

	// Should not panic and handler should not be called.
	reg.Emit(nil)

	if called {
		t.Fatal("handler should not be called for nil context")
	}
}

// TestAllEventTypesConst verifies that all 10 event type constants are defined.
func TestAllEventTypesConst(t *testing.T) {
	expectedEvents := []Event{
		EventLLMStart,
		EventLLMEnd,
		EventLLMError,
		EventLLMToken,
		EventAgentStart,
		EventAgentEnd,
		EventAgentError,
		EventToolStart,
		EventToolEnd,
		EventToolError,
	}

	if len(expectedEvents) != 10 {
		t.Fatalf("expected 10 event types, got %d", len(expectedEvents))
	}

	// Verify each event type is non-empty and unique.
	seen := make(map[Event]bool, len(expectedEvents))
	for _, ev := range expectedEvents {
		if ev == "" {
			t.Errorf("event type should not be empty")
		}
		if seen[ev] {
			t.Errorf("duplicate event type: %s", ev)
		}
		seen[ev] = true
	}
}
