// agent_routes_agents — the /api/agents lifecycle surface (kill/resume/retry)
// and the /api/evolution/approve gate release, split out of agent.go (M-C2).
// Handler bodies are moved verbatim; only the file boundary is new.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// routeAgentLifecycle dispatches POST /api/agents/:id/{kill,resume,retry}.
// The write credential is enforced by the dispatcher (authWrite) BEFORE the
// path shape is parsed — matching the pre-registry switch, which ran
// checkAuth on the whole /api/agents/ prefix — and an unknown shape falls
// through to the control-server tail (read-gate + inner) instead of a local
// 404.
func (h *actionHandler) routeAgentLifecycle(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/agents/"), "/")
	if len(parts) == 2 {
		agentID, action := parts[0], parts[1]
		switch action {
		case "kill":
			h.handleAction(w, r, agentID, "kill", princ, h.mgr.StopAgent)
			return
		case "resume", "retry":
			h.handleAction(w, r, agentID, action, princ, func(ctx context.Context, id string) error {
				return h.mgr.RestartAgent(ctx, id)
			})
			return
		}
	}
	// Unknown shape: the control-server tail, exactly as the fall-through
	// of the pre-registry switch.
	if !h.checkAuthRead(w, r) {
		return
	}
	h.inner.ServeHTTP(w, r)
}

// ── Evolution Governance ─────────────────────────────────

// handleEvolutionApprove promotes the candidate held in SHADOW by the
// gates.require_manual_approval manual gate. Submit only HOLDS the
// candidate (it returns immediately), so Approve here performs the actual
// promote — the response reports the newly-active strategy ID. Approve() is
// a no-op when nothing is pending; the 409 below distinguishes that from a
// real approval. Like every mutator here it is deny-by-default (checkAuth)
// and audited.
func (h *actionHandler) handleEvolutionApprove(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	if h.lifecycle == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{
			"error":  "evolution lifecycle not active",
			"status": "error",
		})
		return
	}
	pendingBefore := h.lifecycle.Snapshot().PendingApproval
	if !pendingBefore {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]any{
			"error":  "no candidate pending manual approval",
			"status": "error",
		})
		return
	}
	h.lifecycle.Approve()
	h.auditAction("evolution_approve", "lifecycle", princ, true)
	snap := h.lifecycle.Snapshot()
	writeJSON(w, map[string]any{
		"status":         "approved",
		"pending_before": true,
		"pending_after":  snap.PendingApproval,
		"active_id":      snap.ActiveID,
		"last_decision":  snap.LastDecision,
	})
}

// ── Agent Lifecycle ──────────────────────────────────────

func (h *actionHandler) handleAction(w http.ResponseWriter, r *http.Request, agentID, action string, princ *ares_security.Principal, fn func(context.Context, string) error) {
	w.Header().Set("Content-Type", "application/json")
	if err := fn(r.Context(), agentID); err != nil {
		h.auditAction(action, agentID, princ, false)
		// Map error to proper HTTP status; don't leak raw err.Error().
		status := http.StatusInternalServerError
		msg := "internal server error"
		switch {
		case errors.Is(err, runtime.ErrAgentNotFound):
			status = http.StatusNotFound
			msg = "agent not found"
		case errors.Is(err, runtime.ErrRuntimeStopped):
			status = http.StatusServiceUnavailable
			msg = "runtime is stopped"
		case errors.Is(err, runtime.ErrAgentAlreadyRegistered):
			status = http.StatusConflict
			msg = "agent already registered"
		case errors.Is(err, runtime.ErrNilAgent), errors.Is(err, runtime.ErrNilFactory):
			status = http.StatusBadRequest
			msg = "invalid agent specification"
		}
		w.WriteHeader(status)
		writeJSON(w, map[string]any{
			"action": action, "agent": agentID, "error": msg, "status": "error",
		})
		return
	}
	h.auditAction(action, agentID, princ, true)
	writeJSON(w, map[string]any{
		"action": action, "agent": agentID, "success": true,
		"message": action + " agent " + agentID + " succeeded",
	})
}
