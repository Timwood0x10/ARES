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

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "completed"})
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

// RuntimeEvolutionHandler handles HTTP requests for the runtime evolution system.
type RuntimeEvolutionHandler struct {
	runtimeEvo core.RuntimeEvolution
}

// NewRuntimeEvolutionHandler creates a new runtime evolution handler.
func NewRuntimeEvolutionHandler(runtimeEvo core.RuntimeEvolution) *RuntimeEvolutionHandler {
	return &RuntimeEvolutionHandler{runtimeEvo: runtimeEvo}
}

// HandleCycle runs one evolution cycle.
func (h *RuntimeEvolutionHandler) HandleCycle(w http.ResponseWriter, r *http.Request) {
	if h.runtimeEvo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime evolution not initialized")
		return
	}
	result, err := h.runtimeEvo.RunCycle(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("run cycle: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleRuntimeStatus returns the runtime evolution system status.
func (h *RuntimeEvolutionHandler) HandleRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if h.runtimeEvo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime evolution not initialized")
		return
	}
	status, err := h.runtimeEvo.Status()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("get status: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// ProposeRequest is the request body for POST /api/v1/evolution/runtime/propose.
type ProposeRequest struct {
	Source   string `json:"source"`
	Text     string `json:"text"`
	Priority int    `json:"priority"`
}

// HandlePropose submits a human/LLM proposal to the coordinator.
func (h *RuntimeEvolutionHandler) HandlePropose(w http.ResponseWriter, r *http.Request) {
	if h.runtimeEvo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime evolution not initialized")
		return
	}
	var req ProposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Source == "" {
		req.Source = "human"
	}
	if req.Priority <= 0 {
		req.Priority = 5
	}
	if err := h.runtimeEvo.Propose(r.Context(), core.RuntimeProposal{
		Source:   req.Source,
		Text:     req.Text,
		Priority: req.Priority,
	}); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("propose: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "submitted"})
}

// HandleEvidenceQuery returns evidence matching the query parameters.
func (h *RuntimeEvolutionHandler) HandleEvidenceQuery(w http.ResponseWriter, r *http.Request) {
	if h.runtimeEvo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime evolution not initialized")
		return
	}

	filter := core.EvidenceFilter{
		Source: r.URL.Query().Get("source"),
		Kind:   core.EvidenceKind(r.URL.Query().Get("kind")),
		Limit:  100,
	}
	if r.URL.Query().Get("limit") != "" {
		if v, err := fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &filter.Limit); err != nil || v != 1 {
			filter.Limit = 100
		}
	}

	evidence, err := h.runtimeEvo.QueryEvidence(r.Context(), filter)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("query evidence: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}

// HandleRegisterComponent registers a new runtime component for evolution.
func (h *RuntimeEvolutionHandler) HandleRegisterComponent(w http.ResponseWriter, r *http.Request) {
	if h.runtimeEvo == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runtime evolution not initialized")
		return
	}

	// Component registration requires a name query parameter.
	// The component must be registered programmatically via the SDK;
	// this endpoint is a placeholder for future REST-based registration.
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "component name is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "component registration requires SDK, use RegisterComponent()"})
}
