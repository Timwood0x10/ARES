package main

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// actionHandler wraps the monitoring HTTP handler and overrides
// kill/resume/retry routes + adds chaos engineering endpoints.
type actionHandler struct {
	inner http.Handler
	mgr   *ares_runtime.Manager
}

func (h *actionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Agent lifecycle actions: /api/agents/:id/{kill,resume,retry}
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

	// Chaos engineering: /api/chaos/*
	if r.Method == "POST" && strings.HasPrefix(path, "/api/chaos/") {
		chaosType := strings.TrimPrefix(path, "/api/chaos/")
		h.handleChaos(w, r, chaosType)
		return
	}

	// Pass through to monitoring server
	h.inner.ServeHTTP(w, r)
}

func (h *actionHandler) handleAction(w http.ResponseWriter, r *http.Request, agentID, action string, fn func(context.Context, string) error) {
	w.Header().Set("Content-Type", "application/json")
	err := fn(r.Context(), agentID)
	if err != nil {
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

// handleChaos implements chaos engineering injection.
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
		killed := make([]string, 0)
		for _, a := range agents {
			if err := h.mgr.StopAgent(r.Context(), a.ID); err == nil {
				killed = append(killed, a.ID)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chaos": "kill-all", "killed": killed, "success": true,
			"message": "chaos: killed all agents",
		})

	case "recover":
		agents := h.mgr.ListAgents()
		recovered := make([]string, 0)
		for _, a := range agents {
			if a.Status != "running" {
				if err := h.mgr.RestartAgent(r.Context(), a.ID); err == nil {
					recovered = append(recovered, a.ID)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chaos": "recover", "recovered": recovered, "success": true,
			"message": "chaos: recovered agents",
		})

	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     "unknown chaos type: " + chaosType,
			"available": []string{"random-kill", "kill-all", "recover"},
		})
	}
}
