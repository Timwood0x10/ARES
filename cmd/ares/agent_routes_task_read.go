// agent_routes_task_read — the external task read surface: GET
// /api/tasks/{task_id}. Read-only projection of the fabric's TaskView slim
// fields plus the task's checkpoint step output when present. The full
// checkpoint envelope is internal and deliberately not exposed.
package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_security"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// taskStatusResponse is the external task read shape: TaskView slim fields
// (fabric read model) plus result, which carries the checkpoint step output
// once a quantum has run. The checkpoint envelope itself (user profile,
// strategy attribution, raw payload) stays internal.
type taskStatusResponse struct {
	TaskID        string    `json:"task_id"`
	Capability    string    `json:"capability"`
	State         string    `json:"state"`
	Owner         string    `json:"owner,omitempty"`
	Quantum       int       `json:"quantum"`
	HasCheckpoint bool      `json:"has_checkpoint"`
	Dependencies  []string  `json:"dependencies,omitempty"`
	Origin        string    `json:"origin,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// Result is the checkpoint step output when one exists; nil while the
	// task is pending or mid-quantum.
	Result any `json:"result,omitempty"`
}

// taskStatusFromTask maps a fabric task snapshot to the external response.
func taskStatusFromTask(t *taskfabric.Task) taskStatusResponse {
	resp := taskStatusResponse{
		TaskID:        t.ID,
		Capability:    t.Capability,
		State:         string(t.State),
		Owner:         t.Owner,
		Quantum:       t.Quantum,
		HasCheckpoint: t.Checkpoint != nil,
		Dependencies:  t.Dependencies,
		Origin:        t.Origin,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
	}
	if env, ok := t.Checkpoint.(*taskfabric.CheckpointEnvelope); ok && env != nil && env.StepCheckpoint != nil {
		resp.Result = env.StepCheckpoint
	}
	return resp
}

// waitForTaskTerminal polls the fabric until the task reaches a terminal
// state or the wait/context expires. Returns (task, true) only on a terminal
// hit — timeout and cancellation are (nil, false) and the caller degrades to
// the async 202 contract. Polling uses a ticker (never a bare Sleep) so the
// context cancellation path is prompt.
func waitForTaskTerminal(ctx context.Context, kernel *kernelHandle, taskID string, wait time.Duration) (*taskfabric.Task, bool) {
	if kernel == nil || kernel.fabric == nil || taskID == "" {
		return nil, false
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if t, err := kernel.fabric.Task(taskID); err == nil {
			if t.State == taskfabric.StateCompleted || t.State == taskfabric.StateFailed {
				return t, true
			}
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-deadline.C:
			return nil, false
		case <-ticker.C:
		}
	}
}

// routeGetTask serves GET /api/tasks/{task_id}: the external result-read
// path for tasks submitted through POST /api/tasks. Read-only; auth level is
// authRead (enforced by the route registry dispatcher before this handler).
func (h *actionHandler) routeGetTask(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	if h.kernel == nil || h.kernel.fabric == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{
			"error":  "peer runtime not active",
			"status": "error",
		})
		return
	}
	taskID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if taskID == "" || strings.Contains(taskID, "/") {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "task id is required"})
		return
	}
	t, err := h.kernel.fabric.Task(taskID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"error": "task not found", "task_id": taskID})
		return
	}
	w.WriteHeader(http.StatusOK)
	writeJSON(w, taskStatusFromTask(t))
}
