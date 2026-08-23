package main

//nolint: errcheck // best-effort ResponseWriter writes (see writeJSON below)

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/ares_security"
)

// writeJSON encodes v to w. HTTP handlers cannot recover a failed response
// write (the status line and headers are already sent), so the error is only
// logged — the client sees a truncated body, the log is the trace.
func writeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("actions: encode response failed", "error", err)
	}
}

// actionHandler wraps the monitoring HTTP handler with:
//   - Agent lifecycle (kill/resume/retry)
//   - Chaos engineering (random-kill/kill-all/recover)
//   - Tool API (list/call)
//
// All destructive endpoints (agents, chaos, tools/call) require authentication:
// either the legacy API key (Authorization: Bearer <key>) or a valid JWT with
// write permission (admin/operator) when JWT auth is configured. When neither
// credential is available, all destructive requests are denied
// (deny-by-default). Every destructive action is recorded on the modular audit
// sink (v0.3.0 review: these paths were previously API-key-only and
// un-audited because actionHandler intercepted the gin routes).
type actionHandler struct {
	inner  http.Handler
	mgr    *ares_runtime.Manager
	tools  *api_tools.Registry
	apiKey string                        // legacy credential (nil/empty = disabled)
	auth   *ares_security.AuthMiddleware // JWT credential (nil = disabled)
	audit  *ares_security.AuditLogger    // modular audit sink (nil = disabled)
	// kernel is the peer-runtime kernel handle (Leader OFF mode). It powers
	// POST /api/tasks (submitPeerTask); nil on the legacy leader path makes
	// that endpoint report 503 "peer runtime not active".
	kernel *kernelHandle
}

// checkAuth enforces authentication on destructive endpoints: the legacy API
// key OR a valid JWT with write permission. Returns true if authorized.
// When neither credential is configured, all requests are denied
// (deny-by-default). A valid JWT that lacks write permission is rejected with
// 403 Forbidden (not 401), so the "unauthenticated vs forbidden" distinction
// matches the gin middleware.
func (h *actionHandler) checkAuth(w http.ResponseWriter, r *http.Request) *ares_security.Principal {
	// JWT path first: a valid token with write permission.
	jwtForbidden := false
	if h.auth != nil {
		if princ, status := h.auth.Verify(r); status == http.StatusOK {
			return princ
		} else if status == http.StatusForbidden {
			jwtForbidden = true
		}
	}
	// Legacy API key path.
	if h.apiKey != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			token := strings.TrimPrefix(auth, prefix)
			if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.apiKey)) == 1 {
				return &ares_security.Principal{Subject: "api-key", Role: ares_security.RoleOperator}
			}
		}
	}
	if h.apiKey == "" && h.auth == nil {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "auth not configured"})
		return nil
	}
	// A well-formed JWT was presented but the role lacks write permission.
	// Report Forbidden (authenticated, not authorized) rather than the
	// misleading Unauthorized the generic path would give.
	if jwtForbidden {
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, map[string]any{"error": "insufficient role: token is valid but lacks write permission"})
		return nil
	}
	w.WriteHeader(http.StatusUnauthorized)
	writeJSON(w, map[string]any{"error": "invalid credentials"})
	return nil
}

// auditAction records a destructive action on the modular audit sink.
func (h *actionHandler) auditAction(action, target string, princ *ares_security.Principal, ok bool) {
	if h.audit == nil {
		return
	}
	subject := "unauthenticated"
	if princ != nil {
		subject = princ.Subject
	}
	h.audit.Action(action, subject, target, ok)
}

func (h *actionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Agent lifecycle: POST /api/agents/:id/{kill,resume,retry}
	if r.Method == "POST" && strings.HasPrefix(path, "/api/agents/") {
		princ := h.checkAuth(w, r)
		if princ == nil {
			return
		}
		parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
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
	}

	// Chaos engineering: POST /api/chaos/{random-kill,kill-all,recover}
	if r.Method == "POST" && strings.HasPrefix(path, "/api/chaos/") {
		princ := h.checkAuth(w, r)
		if princ == nil {
			return
		}
		h.handleChaos(w, r, princ, strings.TrimPrefix(path, "/api/chaos/"))
		return
	}

	// Tool API: POST /api/tools/call
	if r.Method == "POST" && path == "/api/tools/call" {
		princ := h.checkAuth(w, r)
		if princ == nil {
			return
		}
		h.handleCallTool(w, r, princ)
		return
	}

	// Tool API: GET /api/tools
	if r.Method == "GET" && path == "/api/tools" {
		h.handleListTools(w)
		return
	}

	// Peer task submission: POST /api/tasks — the user-facing entry of the
	// peer runtime loop (submitPeerTask). A task is created in the Task
	// Fabric and the kernel scheduler drives it to completion asynchronously.
	if r.Method == "POST" && path == "/api/tasks" {
		princ := h.checkAuth(w, r)
		if princ == nil {
			return
		}
		h.handleSubmitTask(w, r, princ)
		return
	}

	// Pass through to monitoring server
	h.inner.ServeHTTP(w, r)
}

// ── Peer Task Submission ─────────────────────────────────

// submitTaskRequest is the POST /api/tasks payload. capability selects the
// peer agent that can handle the task (matches its declared capabilities);
// payload carries opaque user data (task_desc, profile fields, ...).
type submitTaskRequest struct {
	Capability string         `json:"capability"`
	Payload    map[string]any `json:"payload"`
}

// handleSubmitTask submits a task to the peer runtime through the kernel
// (submitPeerTask) and returns the assigned task id. The submission is
// asynchronous: the scheduler drains the fabric and executes the task; the
// response only confirms acceptance. A nil peer kernel reports 503 so
// callers can distinguish "not a peer runtime" from a real submission
// failure.
func (h *actionHandler) handleSubmitTask(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	if h.kernel == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{
			"error":  "peer runtime not active",
			"status": "error",
		})
		return
	}
	var req submitTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	if req.Capability == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "capability is required"})
		return
	}
	taskID, err := submitPeerTask(r.Context(), h.kernel, req.Capability, req.Payload)
	if err != nil {
		h.auditAction("submit_task", req.Capability, princ, false)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": err.Error(), "status": "error"})
		return
	}
	h.auditAction("submit_task", req.Capability, princ, true)
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{
		"task_id": taskID,
		"status":  "submitted",
		"message": "task accepted by the peer runtime",
	})
}

// ── Agent Lifecycle ──────────────────────────────────────

func (h *actionHandler) handleAction(w http.ResponseWriter, r *http.Request, agentID, action string, princ *ares_security.Principal, fn func(context.Context, string) error) {
	w.Header().Set("Content-Type", "application/json")
	if err := fn(r.Context(), agentID); err != nil {
		h.auditAction(action, agentID, princ, false)
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{
			"action": action, "agent": agentID, "error": err.Error(), "status": "error",
		})
		return
	}
	h.auditAction(action, agentID, princ, true)
	writeJSON(w, map[string]any{
		"action": action, "agent": agentID, "success": true,
		"message": action + " agent " + agentID + " succeeded",
	})
}

// ── Chaos Engineering ────────────────────────────────────

func (h *actionHandler) handleChaos(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal, chaosType string) {
	w.Header().Set("Content-Type", "application/json")
	switch chaosType {
	case "random-kill":
		// P1 unified lifecycle: when the peer kernel exists, kill a fabric
		// agent so the death flows through the REAL kernel recovery chain
		// (agent.killed → lease expiry → requeue → replacement) instead of
		// the legacy runtime's resurrection.
		if h.kernel != nil {
			target, err := chaosKillRandomFabric(r.Context(), h.kernel)
			if err != nil {
				h.auditAction("chaos-random-kill", "unknown", princ, false)
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			h.auditAction("chaos-random-kill", target, princ, true)
			writeJSON(w, map[string]any{
				"chaos": "random-kill", "target": target, "success": true,
				"message": "chaos: killed fabric agent " + target + " (kernel recovery will resume its tasks)",
			})
			return
		}
		agents := h.mgr.ListAgents()
		if len(agents) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "no agents"})
			return
		}
		target := agents[rand.Intn(len(agents))]
		if err := h.mgr.StopAgent(r.Context(), target.ID); err != nil {
			h.auditAction("chaos-random-kill", target.ID, princ, false)
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		h.auditAction("chaos-random-kill", target.ID, princ, true)
		writeJSON(w, map[string]any{
			"chaos": "random-kill", "target": target.ID, "success": true,
			"message": "chaos: killed random agent " + target.ID,
		})
	case "kill-all":
		if h.kernel != nil {
			killed, failed, err := chaosKillAllFabric(r.Context(), h.kernel)
			if err != nil {
				h.auditAction("chaos-kill-all", "unknown", princ, false)
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			h.auditAction("chaos-kill-all", strings.Join(killed, ","), princ, true)
			writeJSON(w, map[string]any{
				"chaos": "kill-all", "killed": killed, "failed": failed, "success": true,
			})
			return
		}
		agents := h.mgr.ListAgents()
		killed := make([]string, 0, len(agents))
		for _, a := range agents {
			if err := h.mgr.StopAgent(r.Context(), a.ID); err == nil {
				killed = append(killed, a.ID)
			}
		}
		h.auditAction("chaos-kill-all", strings.Join(killed, ","), princ, true)
		writeJSON(w, map[string]any{
			"chaos": "kill-all", "killed": killed, "success": true,
		})
	case "recover":
		// Kernel semantics: what recovers is the TASK (durable intent), not
		// the agent (disposable cognition). Force one recovery sweep that
		// requeues every expired-lease task; the recovery loop spawns a
		// replacement executor on demand and resumes from checkpoint.
		if h.kernel != nil {
			requeued, err := chaosRecoverSweep(h.kernel)
			if err != nil {
				h.auditAction("chaos-recover", "unknown", princ, false)
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			h.auditAction("chaos-recover", strings.Join(requeued, ","), princ, true)
			writeJSON(w, map[string]any{
				"chaos": "recover", "recovered_tasks": requeued, "success": true,
				"message": "requeued expired-lease tasks; replacement executors resume from checkpoint",
			})
			return
		}
		agents := h.mgr.ListAgents()
		recovered := make([]string, 0, len(agents))
		for _, a := range agents {
			if a.Status != "running" {
				if err := h.mgr.RestartAgent(r.Context(), a.ID); err == nil {
					recovered = append(recovered, a.ID)
				}
			}
		}
		h.auditAction("chaos-recover", strings.Join(recovered, ","), princ, true)
		writeJSON(w, map[string]any{
			"chaos": "recover", "recovered": recovered, "success": true,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{
			"error":     "unknown chaos type: " + chaosType,
			"available": []string{"random-kill", "kill-all", "recover"},
		})
	}
}

// ── Tool API ─────────────────────────────────────────────

type callToolRequest struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

func (h *actionHandler) handleCallTool(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	var req callToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "name is required"})
		return
	}

	if h.tools != nil {
		result, err := h.tools.Execute(r.Context(), req.Name, req.Params)
		if err != nil {
			h.auditAction("call_tool", req.Name, princ, false)
			// Distinguish "tool not found" from a real execution failure so
			// callers get an accurate error instead of a blanket 404.
			if _, ok := h.tools.Get(req.Name); ok {
				w.WriteHeader(http.StatusInternalServerError)
				writeJSON(w, map[string]any{
					"error": "tool execution failed: " + err.Error(),
				})
			} else {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, map[string]any{
					"error": "tool not found: " + req.Name,
					"tools": h.tools.List(),
				})
			}
			return
		}
		h.auditAction("call_tool", req.Name, princ, true)
		writeJSON(w, map[string]any{
			"tool": req.Name, "success": result.Success, "data": result.Data,
		})
		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	writeJSON(w, map[string]any{"error": "no tool registry"})
}

func (h *actionHandler) handleListTools(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if h.tools == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{"error": "no tool registry"})
		return
	}
	names := h.tools.List()
	writeJSON(w, map[string]any{
		"tools": names,
		"count": len(names),
	})
}
