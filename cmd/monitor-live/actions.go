package main

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"strings"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// actionHandler wraps the monitoring HTTP handler with:
//   - Agent lifecycle (kill/resume/retry)
//   - Chaos engineering (random-kill/kill-all/recover)
//   - Tool API (list/call)
type actionHandler struct {
	inner http.Handler
	mgr   *ares_runtime.Manager
	tools *api_tools.Registry
}

func (h *actionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Agent lifecycle: POST /api/agents/:id/{kill,resume,retry}
	if r.Method == "POST" && strings.HasPrefix(path, "/api/agents/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
		if len(parts) == 2 {
			agentID, action := parts[0], parts[1]
			switch action {
			case "kill":
				h.handleAction(w, r, agentID, "kill", h.mgr.StopAgent)
				return
			case "resume", "retry":
				h.handleAction(w, r, agentID, action, func(ctx context.Context, id string) error {
					return h.mgr.RestartAgent(ctx, id)
				})
				return
			}
		}
	}

	// Chaos engineering: POST /api/chaos/{random-kill,kill-all,recover}
	if r.Method == "POST" && strings.HasPrefix(path, "/api/chaos/") {
		h.handleChaos(w, r, strings.TrimPrefix(path, "/api/chaos/"))
		return
	}

	// Tool API: POST /api/tools/call
	if r.Method == "POST" && path == "/api/tools/call" {
		h.handleCallTool(w, r)
		return
	}

	// Tool API: GET /api/tools
	if r.Method == "GET" && path == "/api/tools" {
		h.handleListTools(w)
		return
	}

	// Pass through to monitoring server
	h.inner.ServeHTTP(w, r)
}

// ── Agent Lifecycle ──────────────────────────────────────

func (h *actionHandler) handleAction(w http.ResponseWriter, r *http.Request, agentID, action string, fn func(context.Context, string) error) {
	w.Header().Set("Content-Type", "application/json")
	if err := fn(r.Context(), agentID); err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": action, "agent": agentID, "error": err.Error(), "status": "error",
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"action": action, "agent": agentID, "success": true,
		"message": action + " agent " + agentID + " succeeded",
	})
}

// ── Chaos Engineering ────────────────────────────────────

func (h *actionHandler) handleChaos(w http.ResponseWriter, r *http.Request, chaosType string) {
	w.Header().Set("Content-Type", "application/json")
	switch chaosType {
	case "random-kill":
		agents := h.mgr.ListAgents()
		if len(agents) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "no agents"})
			return
		}
		target := agents[rand.Intn(len(agents))]
		if err := h.mgr.StopAgent(r.Context(), target.ID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chaos": "random-kill", "target": target.ID, "success": true,
			"message": "chaos: killed random agent " + target.ID,
		})
	case "kill-all":
		agents := h.mgr.ListAgents()
		killed := make([]string, 0, len(agents))
		for _, a := range agents {
			if err := h.mgr.StopAgent(r.Context(), a.ID); err == nil {
				killed = append(killed, a.ID)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chaos": "kill-all", "killed": killed, "success": true,
		})
	case "recover":
		agents := h.mgr.ListAgents()
		recovered := make([]string, 0, len(agents))
		for _, a := range agents {
			if a.Status != "running" {
				if err := h.mgr.RestartAgent(r.Context(), a.ID); err == nil {
					recovered = append(recovered, a.ID)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chaos": "recover", "recovered": recovered, "success": true,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     "unknown chaos type: " + chaosType,
			"available": []string{"random-kill", "kill-all", "recover"},
		})
	}
}

// ── Tool API ─────────────────────────────────────────────

// callToolRequest is the body for POST /api/tools/call.
type callToolRequest struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

func (h *actionHandler) handleCallTool(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req callToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "name is required"})
		return
	}

	if h.tools != nil {
		result, err := h.tools.Execute(r.Context(), req.Name, req.Params)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "tool not found: " + req.Name,
				"tools": h.tools.List(),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tool": req.Name, "success": result.Success, "data": result.Data,
		})
		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "no tool registry"})
}

func (h *actionHandler) handleListTools(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	names := h.tools.List()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tools": names,
		"count": len(names),
	})
}
