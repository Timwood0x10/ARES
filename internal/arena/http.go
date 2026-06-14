package arena

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Handler provides HTTP endpoints for the arena.
type Handler struct {
	service *Service
}

// NewHandler creates a Handler backed by the given Service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes registers arena routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /arena/leader/kill", h.handleKillLeader)
	mux.HandleFunc("POST /arena/agent/{id}/kill", h.handleKillAgent)
	mux.HandleFunc("POST /arena/node/{id}/remove", h.handleRemoveNode)
	mux.HandleFunc("POST /arena/edge/remove", h.handleRemoveEdge)
	mux.HandleFunc("GET /arena/stats", h.handleStats)
	mux.HandleFunc("GET /arena/history", h.handleHistory)
	mux.HandleFunc("GET /arena/stream", h.handleStream)
	mux.HandleFunc("GET /arena/score", h.handleScore)
	mux.HandleFunc("POST /arena/survival", h.handleSurvivalStart)
	mux.HandleFunc("GET /arena/survival/status", h.handleSurvivalStatus)
}

// edgeRequest is the JSON body for RemoveEdge requests.
type edgeRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (h *Handler) handleKillLeader(w http.ResponseWriter, r *http.Request) {
	action := Action{
		ID:        uuid.New().String(),
		Type:      ActionKillLeader,
		CreatedAt: time.Now(),
	}
	result := h.service.Execute(r.Context(), action)
	writeResult(w, result)
}

func (h *Handler) handleKillAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing agent id")
		return
	}
	action := Action{
		ID:        uuid.New().String(),
		Type:      ActionKillAgent,
		TargetID:  id,
		CreatedAt: time.Now(),
	}
	result := h.service.Execute(r.Context(), action)
	writeResult(w, result)
}

func (h *Handler) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing node id")
		return
	}
	action := Action{
		ID:        uuid.New().String(),
		Type:      ActionRemoveNode,
		TargetID:  id,
		CreatedAt: time.Now(),
	}
	result := h.service.Execute(r.Context(), action)
	writeResult(w, result)
}

func (h *Handler) handleRemoveEdge(w http.ResponseWriter, r *http.Request) {
	var req edgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.From == "" || req.To == "" {
		writeError(w, http.StatusBadRequest, "both 'from' and 'to' are required")
		return
	}
	action := Action{
		ID:        uuid.New().String(),
		Type:      ActionRemoveEdge,
		TargetID:  req.To,
		SourceID:  req.From,
		CreatedAt: time.Now(),
	}
	result := h.service.Execute(r.Context(), action)
	writeResult(w, result)
}

func (h *Handler) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.Stats())
}

func (h *Handler) handleHistory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.History())
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ch, err := h.service.Subscribe(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ch == nil {
		writeError(w, http.StatusServiceUnavailable, "event store not configured")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				slog.Error("arena: sse marshal error", "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				slog.Debug("arena: sse write error (client disconnected?)", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Handler) handleScore(w http.ResponseWriter, _ *http.Request) {
	stats := h.service.Stats()
	avgRecovery := h.service.calculateAvgRecoveryTime(nil)
	score := CalculateScore(stats, avgRecovery)
	writeJSON(w, http.StatusOK, score)
}

func (h *Handler) handleSurvivalStart(w http.ResponseWriter, r *http.Request) {
	var cfg SurvivalConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 30 * time.Minute
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}

	// Run survival in background.
	go func() {
		report := h.service.RunSurvival(context.Background(), cfg)
		slog.Info("arena: survival run finished in background",
			"actions", report.ActionsRun,
			"score", report.Score.Score,
			"grade", report.Score.Grade,
		)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "started",
		"message": "survival run started",
	})
}

func (h *Handler) handleSurvivalStatus(w http.ResponseWriter, _ *http.Request) {
	status := h.service.GetSurvivalStatus()
	writeJSON(w, http.StatusOK, status)
}

// writeResult writes an action result as JSON. Returns 500 on failure, 200 on success.
func writeResult(w http.ResponseWriter, result Result) {
	status := http.StatusOK
	if !result.Success {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, result)
}

// writeJSON marshals v as JSON and writes it to w.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("arena: json encode error", "error", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// RecoverMiddleware wraps an http.Handler with panic recovery.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("arena: handler panic recovered", "panic", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ValidateAction checks that an action has the required fields for its type.
func ValidateAction(action Action) error {
	if action.Type == "" {
		return errors.New("action type is required")
	}
	switch action.Type {
	case ActionKillLeader:
		// No target required; leader is discovered automatically.
	case ActionKillAgent:
		if action.TargetID == "" {
			return errors.New("target_id is required for kill_agent")
		}
	case ActionRemoveNode:
		if action.TargetID == "" {
			return errors.New("target_id is required for remove_node")
		}
	case ActionRemoveEdge:
		if action.SourceID == "" || action.TargetID == "" {
			return errors.New("source_id and target_id are required for remove_edge")
		}
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
	return nil
}

// RoutePath returns the canonical route path for an action type.
func RoutePath(actionType ActionType) string {
	switch actionType {
	case ActionKillLeader:
		return "POST /arena/leader/kill"
	case ActionKillAgent:
		return "POST /arena/agent/{id}/kill"
	case ActionRemoveNode:
		return "POST /arena/node/{id}/remove"
	case ActionRemoveEdge:
		return "POST /arena/edge/remove"
	default:
		return ""
	}
}

// ParseActionType converts a string to an ActionType.
func ParseActionType(s string) (ActionType, error) {
	switch strings.ToLower(s) {
	case "kill_leader":
		return ActionKillLeader, nil
	case "kill_agent":
		return ActionKillAgent, nil
	case "remove_node":
		return ActionRemoveNode, nil
	case "remove_edge":
		return ActionRemoveEdge, nil
	default:
		return "", fmt.Errorf("unknown action type: %s", s)
	}
}
