// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// EvolutionHandler handles HTTP requests for the evolution system.
type EvolutionHandler struct {
	evolution core.Evolution
}

// NewEvolutionHandler creates a new evolution handler.
func NewEvolutionHandler(evolution core.Evolution) *EvolutionHandler {
	return &EvolutionHandler{
		evolution: evolution,
	}
}

// EvolutionRequest is the request body for POST /api/v1/evolution/start.
type EvolutionRequest struct {
	// Generations is the number of evolution generations to run.
	Generations int `json:"generations"`
}

// HandleStart starts an evolution run and returns the result.
func (h *EvolutionHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if h.evolution == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "evolution system not initialized")
		return
	}

	var req EvolutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Generations <= 0 {
		req.Generations = 15
	}

	result, err := h.evolution.Evolve(r.Context(), req.Generations)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("evolution failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleIdleStart starts an idle evolution run with report generation.
func (h *EvolutionHandler) HandleIdleStart(w http.ResponseWriter, r *http.Request) {
	if h.evolution == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "evolution system not initialized")
		return
	}

	var req EvolutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Generations <= 0 {
		req.Generations = 15
	}

	if err := h.evolution.RunIdleEvolution(r.Context(), req.Generations); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("idle evolution failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
}

// HandleReport returns the latest evolution report text.
func (h *EvolutionHandler) HandleReport(w http.ResponseWriter, r *http.Request) {
	if h.evolution == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "evolution system not initialized")
		return
	}

	report, err := h.evolution.LatestReport()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("get report: %v", err))
		return
	}
	if report == "" {
		writeJSONError(w, http.StatusNotFound, "no report available")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"report": report})
}

// HandleStatus returns evolution system status.
func (h *EvolutionHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if h.evolution == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "evolution system not initialized")
		return
	}

	stats, err := h.evolution.Stats()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("get stats: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
