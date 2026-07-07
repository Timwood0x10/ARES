// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// ArenaHandler handles HTTP requests for chaos engineering operations.
type ArenaHandler struct {
	arena core.Arena
}

// NewArenaHandler creates a new arena handler.
// Args:
//
//	arena - the arena service implementation.
//
// Returns:
//
//	handler - the initialized arena handler.
func NewArenaHandler(arena core.Arena) *ArenaHandler {
	return &ArenaHandler{arena: arena}
}

// InjectFaultRequest is the request body for POST /api/v1/arena/faults.
type InjectFaultRequest struct {
	FaultType string `json:"fault_type"`
	TargetID  string `json:"target_id"`
}

// HandleInjectFault injects a fault into the system.
func (h *ArenaHandler) HandleInjectFault(w http.ResponseWriter, r *http.Request) {
	if h.arena == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "arena service not initialized")
		return
	}

	var req InjectFaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.FaultType == "" || req.TargetID == "" {
		writeJSONError(w, http.StatusBadRequest, "fault_type and target_id are required")
		return
	}

	if err := h.arena.InjectFault(r.Context(), core.FaultType(req.FaultType), req.TargetID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("inject fault failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "fault injected"})
}

// HandleScore returns the current resilience score.
func (h *ArenaHandler) HandleScore(w http.ResponseWriter, r *http.Request) {
	if h.arena == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "arena service not initialized")
		return
	}

	score := h.arena.Score()
	writeJSON(w, http.StatusOK, score)
}

// HandleRunRandom runs random fault injection.
func (h *ArenaHandler) HandleRunRandom(w http.ResponseWriter, r *http.Request) {
	if h.arena == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "arena service not initialized")
		return
	}

	durationStr := r.URL.Query().Get("duration")
	duration := 30 * time.Second
	if durationStr != "" {
		parsed, err := time.ParseDuration(durationStr)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid duration: %v", err))
			return
		}
		duration = parsed
	}

	report, err := h.arena.RunRandom(r.Context(), duration)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("run random faults failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, report)
}

// HandleListAgents returns agents under test.
func (h *ArenaHandler) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	if h.arena == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "arena service not initialized")
		return
	}

	agents := h.arena.ListAgents()
	writeJSON(w, http.StatusOK, agents)
}
