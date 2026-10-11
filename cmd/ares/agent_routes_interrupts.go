// agent_routes_interrupts — the HITL approval-gate HTTP surface:
// GET  /api/interrupts            (list pending approvals)
// POST /api/interrupts/{id}/resolve (approve or reject)
//
// Split out of agent.go per the route registry pattern (M-C2). Handler
// bodies follow the agent_routes_tasks.go template: nil-kernel 503 guard,
// json decode, writeJSON, fabric operations via h.kernel.fabric.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_security"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// routeListInterrupts serves GET /api/interrupts: lists every task with a
// pending (unresolved) HITL approval gate. Read-only; auth level is authRead.
func (h *actionHandler) routeListInterrupts(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	if h.kernel == nil || h.kernel.fabric == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{
			"error":  "peer runtime not active",
			"status": "error",
		})
		return
	}
	pending := h.kernel.fabric.PendingApprovals()
	if pending == nil {
		pending = []taskfabric.ApprovalView{}
	}
	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]any{
		"interrupts": pending,
		"count":      len(pending),
	})
}

// resolveInterruptRequest is the POST /api/interrupts/{id}/resolve payload.
type resolveInterruptRequest struct {
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback,omitempty"`
}

// routeResolveInterrupt serves POST /api/interrupts/{id}/resolve: resolves a
// HITL approval gate. authWrite is enforced by the route registry dispatcher
// — the principal is verified and passed as the approver of record. The
// {id} segment is extracted from the path the same way routeGetTask extracts
// its task_id (prefix-strip + trim).
func (h *actionHandler) routeResolveInterrupt(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	if h.kernel == nil || h.kernel.fabric == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{
			"error":  "peer runtime not active",
			"status": "error",
		})
		return
	}
	// Extract {id} from /api/interrupts/{id}/resolve.
	// Strip the prefix "/api/interrupts/" and the suffix "/resolve".
	id := strings.TrimPrefix(r.URL.Path, "/api/interrupts/")
	id = strings.TrimSuffix(id, "/resolve")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "interrupt id is required"})
		return
	}
	var req resolveInterruptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	decision := taskfabric.ApprovalDecisionRejected
	if req.Approved {
		decision = taskfabric.ApprovalDecisionApproved
	}
	approver := ""
	if princ != nil {
		approver = princ.Subject
	}
	if err := h.kernel.fabric.ResolveApproval(id, decision, req.Feedback, approver); err != nil {
		switch {
		case errors.Is(err, taskfabric.ErrTaskNotFound):
			w.WriteHeader(http.StatusNotFound)
		case errors.Is(err, taskfabric.ErrApprovalNotRequired):
			w.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, taskfabric.ErrInvalidApprovalDecision):
			w.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, taskfabric.ErrApprovalAlreadyResolved):
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
		writeJSON(w, map[string]any{"error": err.Error(), "status": "error"})
		return
	}
	// Read the task state for the response.
	resp := map[string]any{
		"task_id":  id,
		"decision": decision,
	}
	if tk, err := h.kernel.fabric.Task(id); err == nil {
		resp["state"] = string(tk.State)
	}
	w.WriteHeader(http.StatusOK)
	writeJSON(w, resp)
}
