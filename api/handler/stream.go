// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"

	"goagentx/internal/agents/base"
)

// StreamHandler handles SSE streaming requests.
type StreamHandler struct {
	counter atomic.Uint64
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler() *StreamHandler {
	return &StreamHandler{}
}

// StreamRequest represents a streaming request.
type StreamRequest struct {
	// Query is the user input text.
	Query string `json:"query"`
	// SessionID is an optional session ID for context.
	SessionID string `json:"session_id,omitempty"`
	// Options contains optional streaming options.
	Options map[string]any `json:"options,omitempty"`
}

// StreamResponse represents a single SSE event.
type StreamResponse struct {
	// Event is the event type.
	Event string `json:"event"`
	// Data is the event payload.
	Data any `json:"data"`
	// Error is the error message if any.
	Error string `json:"error,omitempty"`
}

// AgentProcessor defines the interface for processing streaming requests.
type AgentProcessor interface {
	ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error)
}

// HandleStream handles SSE streaming requests.
// POST /api/v1/stream
func (h *StreamHandler) HandleStream(processor AgentProcessor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST method
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse request body (limit to 1MB to prevent OOM).
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req StreamRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}

		if req.Query == "" {
			http.Error(w, "Query is required", http.StatusBadRequest)
			return
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Flush helper
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		// Create context that cancels when client disconnects
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Start processing
		eventCh, err := processor.ProcessStream(ctx, req.Query)
		if err != nil {
			_ = h.sendSSE(w, flusher, "error", map[string]string{"message": err.Error()})
			return
		}

		// Stream events to client
		for event := range eventCh {
			// Check if client disconnected
			select {
			case <-ctx.Done():
				slog.Debug("Client disconnected, stopping stream")
				return
			default:
			}

			// Convert event to SSE response
			resp := h.convertEvent(event)

			// Send SSE event
			if err := h.sendSSE(w, flusher, resp.Event, resp.Data); err != nil {
				slog.Warn("Failed to send SSE event", "error", err)
				return
			}
		}

		// Send done event
		if err = h.sendSSE(w, flusher, "done", map[string]string{"status": "complete"}); err != nil {
			slog.Warn("Failed to send done event", "error", err)
			return
		}
	}
}

// convertEvent converts an AgentEvent to StreamResponse.
func (h *StreamHandler) convertEvent(event base.AgentEvent) StreamResponse {
	resp := StreamResponse{
		Data: event.Data,
	}

	switch event.Type {
	case base.EventPlanning:
		resp.Event = "planning"
	case base.EventTaskStart:
		resp.Event = "task_start"
	case base.EventTaskProgress:
		resp.Event = "task_progress"
	case base.EventTaskComplete:
		resp.Event = "task_complete"
	case base.EventAggregating:
		resp.Event = "aggregating"
	case base.EventComplete:
		resp.Event = "complete"
	default:
		resp.Event = "unknown"
	}

	if event.Err != nil {
		resp.Error = event.Err.Error()
	}

	return resp
}

// sendSSE sends a single SSE event.
func (h *StreamHandler) sendSSE(w io.Writer, flusher http.Flusher, event string, data any) error {
	// Marshal data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal SSE data: %w", err)
	}

	// Write SSE format
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return fmt.Errorf("write SSE event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "id: %d\n", h.counter.Add(1)); err != nil {
		return fmt.Errorf("write SSE id: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", jsonData); err != nil {
		return fmt.Errorf("write SSE data: %w", err)
	}

	// Flush to send immediately
	flusher.Flush()

	return nil
}
