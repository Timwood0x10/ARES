package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/base"
)

// BenchmarkStreamHandler_HandleStream benchmarks the SSE streaming handler.
func BenchmarkStreamHandler_HandleStream(b *testing.B) {
	handler := NewStreamHandler()

	events := []base.AgentEvent{
		{Type: base.EventPlanning, Source: "test", Data: "planning"},
		{Type: base.EventTaskStart, Source: "test", Data: "task"},
		{Type: base.EventTaskComplete, Source: "test", Data: "result"},
		{Type: base.EventComplete, Source: "test", Data: "done"},
	}

	processor := &MockAgentProcessor{events: events}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/stream", strings.NewReader(`{"query": "test"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		handler.HandleStream(processor).ServeHTTP(rec, req)
	}
}

func BenchmarkStreamHandler_ConvertEvent(b *testing.B) {
	handler := NewStreamHandler()

	event := base.AgentEvent{
		Type:   base.EventTaskComplete,
		Source: "test-agent",
		Data:   "test data",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = handler.convertEvent(event)
	}
}

func BenchmarkStreamHandler_MultipleEvents(b *testing.B) {
	handler := NewStreamHandler()

	// Simulate a larger number of events
	events := make([]base.AgentEvent, 101)
	for i := 0; i < 100; i++ {
		events[i] = base.AgentEvent{
			Type:   base.EventTaskProgress,
			Source: "test",
			Data:   "progress update",
		}
	}
	events[100] = base.AgentEvent{Type: base.EventComplete, Source: "test", Data: "done"}

	processor := &MockAgentProcessor{events: events}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/stream", strings.NewReader(`{"query": "test"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		handler.HandleStream(processor).ServeHTTP(rec, req)
	}
}
