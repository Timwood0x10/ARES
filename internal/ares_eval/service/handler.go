package evalapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Handler provides HTTP handlers for evaluation API endpoints.
type Handler struct {
	svc *Service
}

// NewHandler creates a new eval API handler.
//
// Args:
//
//	svc - eval service instance (must not be nil).
//
// Returns:
//
//	*Handler - the handler instance.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// HandleRunEval handles POST /api/v1/eval/run.
// It validates the request, generates a run ID, starts the evaluation
// asynchronously in a background goroutine, and returns 202 Accepted immediately.
func (h *Handler) HandleRunEval(w http.ResponseWriter, r *http.Request) {
	var req RunEvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Basic input validation at HTTP level — reject obviously invalid requests early.
	if req.SuitePath == "" {
		writeError(w, http.StatusBadRequest, ErrEmptySuitePath.Error(), ErrEmptySuitePath)
		return
	}
	if len(req.AgentConfigs) == 0 {
		writeError(w, http.StatusBadRequest, ErrEmptyAgentConfigs.Error(), ErrEmptyAgentConfigs)
		return
	}

	// Generate run ID early so we can return immediately.
	runID := uuid.New().String()

	// Run evaluation asynchronously in a background goroutine.
	go func() {
		evalCtx := context.Background()
		evalCtx, evalCancel := context.WithTimeout(evalCtx, 30*time.Minute)
		defer evalCancel()

		req.RunID = runID // Ensure run ID is set for the service to use
		resp, err := h.svc.RunEval(evalCtx, &req)
		if err != nil {
			slog.Error("async eval run failed", "run_id", runID, "error", err)
		} else {
			slog.Info("async eval run completed", "run_id", runID, "status", resp.Status)
		}
	}()

	writeJSON(w, http.StatusAccepted, &RunEvalResponse{
		RunID:          runID,
		Status:         "running",
		TotalConfigs:   len(req.AgentConfigs),
		TotalTestCases: 0, // Will be filled after completion
	})
}

// HandleGetResults handles GET /api/v1/eval/results/:run_id.
// It returns all evaluation results for the specified run.
func (h *Handler) HandleGetResults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, cancel := contextWithTimeout(ctx, 30*time.Second)
	defer cancel()

	runID := extractPathValue(r.URL.Path, "/api/v1/eval/results/")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required", ErrInvalidRunID)
		return
	}

	resp, err := h.svc.GetResults(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrInvalidRunID) {
			writeError(w, http.StatusBadRequest, err.Error(), err)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get results", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleGetLeaderboard handles GET /api/v1/eval/leaderboard.
// Query params: limit (default 20), offset (default 0).
func (h *Handler) HandleGetLeaderboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, cancel := contextWithTimeout(ctx, 30*time.Second)
	defer cancel()

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	resp, err := h.svc.GetLeaderboard(ctx, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get leaderboard", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleGetComparison handles GET /api/v1/eval/comparison.
// Query param: run_ids (comma-separated list of run IDs).
func (h *Handler) HandleGetComparison(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, cancel := contextWithTimeout(ctx, 60*time.Second)
	defer cancel()

	runIDsStr := r.URL.Query().Get("run_ids")
	if runIDsStr == "" {
		writeError(w, http.StatusBadRequest, "run_ids query parameter is required", ErrEmptyRunIDs)
		return
	}

	runIDs := splitCommaSeparated(runIDsStr)
	if len(runIDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one run_id is required", ErrEmptyRunIDs)
		return
	}

	resp, err := h.svc.GetComparison(ctx, runIDs)
	if err != nil {
		if errors.Is(err, ErrEmptyRunIDs) {
			writeError(w, http.StatusBadRequest, err.Error(), err)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get comparison", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Response helpers ---

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}

// errorResponse is the standard error response body format.
type errorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

// writeError writes an error response in a standard format.
func writeError(w http.ResponseWriter, status int, message string, err error) {
	slog.Warn("eval api error",
		"status", status,
		"message", message,
		"error", err,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errorResponse{
		Error: message,
		Code:  status,
	}); err != nil {
		slog.Warn("eval: encode error response", "error", err)
	}
}

// --- Request parsing helpers ---

// extractPathValue extracts a dynamic path segment after the given prefix.
// For example, with prefix "/api/v1/eval/results/" and path "/api/v1/eval/results/abc-123",
// it returns "abc-123".
func extractPathValue(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	value := strings.TrimPrefix(path, prefix)
	// Strip trailing slash if present.
	value = strings.TrimSuffix(value, "/")
	if value == "" {
		return ""
	}
	return value
}

// splitCommaSeparated splits a comma-separated string into trimmed, non-empty parts.
func splitCommaSeparated(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// contextWithTimeout wraps context.WithTimeout but returns the original
// context if it already has a deadline.
func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := parent.Deadline(); hasDeadline {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
