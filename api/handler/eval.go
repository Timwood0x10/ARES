// Package handler provides HTTP handlers for the ARES API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// EvalHandler handles HTTP requests for agent output evaluation.
type EvalHandler struct {
	evaluators core.EvaluatorRegistry
}

// NewEvalHandler creates a new evaluation handler.
// Args:
//
//	evaluators - the evaluator registry.
//
// Returns:
//
//	handler - the initialized eval handler.
func NewEvalHandler(evaluators core.EvaluatorRegistry) *EvalHandler {
	return &EvalHandler{evaluators: evaluators}
}

// EvaluateRequest is the request body for POST /api/v1/eval/evaluate.
type EvaluateRequest struct {
	Evaluator string `json:"evaluator"`
	Input     string `json:"input"`
	Output    string `json:"output"`
	Expected  string `json:"expected"`
}

// HandleEvaluate evaluates an agent's output using the specified evaluator.
func (h *EvalHandler) HandleEvaluate(w http.ResponseWriter, r *http.Request) {
	if h.evaluators == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "evaluation service not initialized")
		return
	}

	var req EvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Evaluator == "" {
		writeJSONError(w, http.StatusBadRequest, "evaluator is required")
		return
	}

	evaluator := h.evaluators.Get(req.Evaluator)
	if evaluator == nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown evaluator: %s", req.Evaluator))
		return
	}

	score, err := evaluator.Evaluate(r.Context(), req.Input, req.Output, req.Expected)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("evaluation failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]float64{"score": score})
}

// HandleListEvaluators returns all registered evaluator names.
func (h *EvalHandler) HandleListEvaluators(w http.ResponseWriter, r *http.Request) {
	if h.evaluators == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "evaluation service not initialized")
		return
	}

	writeJSON(w, http.StatusOK, h.evaluators.Names())
}
