// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// RuntimeHandler handles HTTP requests for agent runtime management.
type RuntimeHandler struct {
	runtime core.Runtime
}

// NewRuntimeHandler creates a new runtime handler.
// Args:
//
//	runtime - the runtime service implementation.
//
// Returns:
//
//	handler - the initialized runtime handler.
func NewRuntimeHandler(runtime core.Runtime) *RuntimeHandler {
	return &RuntimeHandler{runtime: runtime}
}

// StartAgentRequest is the request body for POST /api/v1/runtime/agents/{id}/start.
type StartAgentRequest struct {
	AgentID string `json:"agent_id"`
}

// HandleStart starts the runtime.
func (h *RuntimeHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime service not initialized")
		return
	}

	if err := h.runtime.Start(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("start runtime failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "started"})
}

// HandleStop stops the runtime.
func (h *RuntimeHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime service not initialized")
		return
	}

	if err := h.runtime.Stop(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("stop runtime failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "stopped"})
}

// HandleGetAgent returns an agent by ID from the runtime.
func (h *RuntimeHandler) HandleGetAgent(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime service not initialized")
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		writeJSONError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	agent := h.runtime.GetAgent(agentID)
	if agent.ID == "" {
		writeJSONError(w, http.StatusNotFound, "agent not found")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// HandleStats returns runtime statistics.
func (h *RuntimeHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime service not initialized")
		return
	}

	stats := h.runtime.Stats()
	writeJSON(w, http.StatusOK, stats)
}
