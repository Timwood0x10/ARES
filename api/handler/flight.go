// Package handler provides HTTP handlers for the ARES API.
package handler

import (
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// FlightHandler handles HTTP requests for flight recording and replay.
type FlightHandler struct {
	flight core.FlightRecorder
}

// NewFlightHandler creates a new flight handler.
// Args:
//
//	flight - the flight recorder implementation.
//
// Returns:
//
//	handler - the initialized flight handler.
func NewFlightHandler(flight core.FlightRecorder) *FlightHandler {
	return &FlightHandler{flight: flight}
}

// HandleReplay replays a flight by session ID.
func (h *FlightHandler) HandleReplay(w http.ResponseWriter, r *http.Request) {
	if h.flight == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "flight recorder not initialized")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session id is required")
		return
	}

	result, err := h.flight.Replay(r.Context(), sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("replay failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleStop stops the flight recorder.
func (h *FlightHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	if h.flight == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "flight recorder not initialized")
		return
	}

	h.flight.Stop()
	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "stopped"})
}
