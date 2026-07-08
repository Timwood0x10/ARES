// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/Timwood0x10/ares/internal/agents/base"
)

// apiKeyHeader is the HTTP header used to pass the API key.
const apiKeyHeader = "X-API-Key"

// StreamHandler handles SSE streaming requests.
type StreamHandler struct {
	counter        atomic.Uint64
	apiKey         string   // when non-empty, requests must carry X-API-Key header
	allowedOrigins []string // when non-empty, only these origins receive CORS headers
}

// StreamOption configures a StreamHandler.
type StreamOption func(*StreamHandler)

// WithAPIKey enables API key authentication on the stream endpoint.
// When key is empty, authentication is disabled.
func WithAPIKey(key string) StreamOption {
	return func(h *StreamHandler) { h.apiKey = key }
}

// WithAllowedOrigins restricts CORS to the given origins. When empty (default),
// no CORS headers are sent, preventing cross-origin browser requests.
// Passing "*" restores the legacy permissive behavior (not recommended for production).
func WithAllowedOrigins(origins ...string) StreamOption {
	return func(h *StreamHandler) { h.allowedOrigins = origins }
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler(opts ...StreamOption) *StreamHandler {
	h := &StreamHandler{}
	for _, opt := range opts {
		opt(h)
	}
	return h
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

		// API key authentication (when configured).
		if h.apiKey != "" {
			provided := r.Header.Get(apiKeyHeader)
			if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(h.apiKey)) != 1 {
				http.Error(w, "missing or invalid API key", http.StatusUnauthorized)
				return
			}
		}

		// CORS: only echo back explicitly allowed origins (never wildcard by default).
		if origin := r.Header.Get("Origin"); origin != "" && h.isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+apiKeyHeader)
			w.Header().Set("Vary", "Origin")
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
		if len(req.Query) > 8192 {
			http.Error(w, "Query too long", http.StatusBadRequest)
			return
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

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
			if err := h.sendSSE(w, flusher, "error", map[string]string{"message": err.Error()}); err != nil {
				fmt.Printf("stream: send SSE error: %v\n", err)
			}
			return
		}

		// Stream events to client
		for event := range eventCh {
			// Check if client disconnected
			select {
			case <-ctx.Done():
				log.Debug("Client disconnected, stopping stream")
				return
			default:
			}

			// Convert event to SSE response
			resp := h.convertEvent(event)

			// Send SSE event
			if err := h.sendSSE(w, flusher, resp.Event, resp.Data); err != nil {
				log.Warn("Failed to send SSE event", "error", err)
				return
			}
		}

		// Send done event
		if err = h.sendSSE(w, flusher, "done", map[string]string{keyStatus: "complete"}); err != nil {
			log.Warn("Failed to send done event", "error", err)
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

// isOriginAllowed reports whether the given Origin header value is in the
// configured allow list. When the list is empty, no origin is allowed (the
// default secure posture). An explicit "*" entry allows all origins.
func (h *StreamHandler) isOriginAllowed(origin string) bool {
	for _, allowed := range h.allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
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
