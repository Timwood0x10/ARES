package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/base"
)

// MockAgentProcessor implements AgentProcessor for testing.
type MockAgentProcessor struct {
	events []base.AgentEvent
	err    error
}

func (m *MockAgentProcessor) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	if m.err != nil {
		return nil, m.err
	}

	ch := make(chan base.AgentEvent, len(m.events))
	for _, event := range m.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestStreamHandler_HandleStream(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		body           string
		events         []base.AgentEvent
		expectedStatus int
		expectEvents   int
	}{
		{
			name:   "valid streaming request",
			method: "POST",
			body:   `{"query": "test query"}`,
			events: []base.AgentEvent{
				{Type: base.EventPlanning, Source: "test", Data: "planning"},
				{Type: base.EventTaskStart, Source: "test", Data: "task"},
				{Type: base.EventComplete, Source: "test", Data: "result"},
			},
			expectedStatus: http.StatusOK,
			expectEvents:   4, // 3 events + 1 done event
		},
		{
			name:           "empty query",
			method:         "POST",
			body:           `{"query": ""}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid method",
			method:         "GET",
			body:           `{"query": "test"}`,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "invalid JSON",
			method:         "POST",
			body:           `invalid`,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewStreamHandler()
			processor := &MockAgentProcessor{events: tt.events}

			req := httptest.NewRequestWithContext(context.Background(), tt.method, "/api/v1/stream", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleStream(processor).ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			if tt.expectedStatus == http.StatusOK {
				// Verify SSE headers
				contentType := rec.Header().Get("Content-Type")
				if contentType != "text/event-stream" {
					t.Errorf("expected SSE content type, got %s", contentType)
				}

				// Count events
				body := rec.Body.String()
				eventCount := strings.Count(body, "event:")
				if eventCount != tt.expectEvents {
					t.Errorf("expected %d events, got %d", tt.expectEvents, eventCount)
				}
			}
		})
	}
}

func TestStreamHandler_ConvertEvent(t *testing.T) {
	handler := NewStreamHandler()

	tests := []struct {
		name          string
		event         base.AgentEvent
		expectedType  string
		expectedError bool
	}{
		{
			name:         "planning event",
			event:        base.AgentEvent{Type: base.EventPlanning, Source: "test"},
			expectedType: "planning",
		},
		{
			name:         "task start event",
			event:        base.AgentEvent{Type: base.EventTaskStart, Source: "test"},
			expectedType: "task_start",
		},
		{
			name:         "complete event",
			event:        base.AgentEvent{Type: base.EventComplete, Source: "test"},
			expectedType: "complete",
		},
		{
			name:          "error event",
			event:         base.AgentEvent{Type: base.EventComplete, Source: "test", Err: context.Canceled},
			expectedType:  "complete",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := handler.convertEvent(tt.event)

			if resp.Event != tt.expectedType {
				t.Errorf("expected event type %s, got %s", tt.expectedType, resp.Event)
			}

			if tt.expectedError && resp.Error == "" {
				t.Error("expected error message")
			}
		})
	}
}

func TestStreamRequest_JSON(t *testing.T) {
	req := StreamRequest{
		Query:     "test query",
		SessionID: "session-123",
		Options:   map[string]any{"key": "value"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded StreamRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Query != req.Query {
		t.Errorf("query mismatch")
	}
}
