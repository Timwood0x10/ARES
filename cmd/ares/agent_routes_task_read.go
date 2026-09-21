// agent_routes_task_read — the external task read surface: GET
// /api/tasks/{task_id}. Read-only projection of the fabric's TaskView slim
// fields plus the task's terminal result channel: the session answer when
// one exists (the L2 path's real user-facing output), the quantum step
// checkpoint otherwise, and the failure cause on FAILED. The full checkpoint
// envelope is internal and deliberately not exposed.
package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/ares_security"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// External response field names shared by the GET view and the ?wait=
// terminal body (goconst shield).
const (
	taskViewFieldTaskID = "task_id"
	taskViewFieldState  = "state"
)

// sessionStalledError is the external-view error for a COMPLETED
// session-scoped task whose session died before producing an answer — the
// same verdict the sdk wait loop reports as a submission failure.
const sessionStalledError = "session stalled before an answer landed"

// taskStatusResponse is the external task read shape: TaskView slim fields
// (fabric read model) plus the terminal result channel. Result carries the
// session answer on the L2 path (items[0].Content of the session's completed
// answer task) and falls back to the quantum step checkpoint when no session
// answer exists. Error carries the failure cause on FAILED. The checkpoint
// envelope itself (user profile, strategy attribution, raw payload) stays
// internal.
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
	// Result is the terminal output when one exists: nil while the task is
	// pending or mid-quantum.
	Result any `json:"result,omitempty"`
	// Error is the terminal failure cause when one exists: the quantum step
	// error persisted on the checkpoint (envelope LastError), or a derived
	// provenance message when only cascade/session evidence is available.
	Error string `json:"error,omitempty"`
}

// taskStatusFromTask maps a fabric task snapshot to the external response,
// resolving the terminal result channel through the handler's kernel.
func (h *actionHandler) taskStatusFromTask(t *taskfabric.Task) taskStatusResponse {
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
	dc, err := taskfabric.DecodeCheckpoint(t.Checkpoint)
	if err != nil {
		return resp
	}
	resp.Result, resp.Error = h.terminalViewFields(t, dc)
	return resp
}

// terminalViewFields resolves the external result/error pair for a terminal
// (or mid-flight) task. The L2 path's user-facing output lives on the
// session's answer task — the submitted plan task's own checkpoint only
// carries the last quantum's step output — so a completed session-scoped
// task prefers the session answer. Failure text priority: persisted quantum
// cause, then cascade provenance (FailedDependency), then a failed session
// answer node.
func (h *actionHandler) terminalViewFields(t *taskfabric.Task, dc taskfabric.DecodedCheckpoint) (result any, errText string) {
	result = dc.StepCheckpoint
	errText = dc.LastError
	if h.kernel == nil || dc.SessionID == "" {
		return result, derivedTaskError(t, errText)
	}
	if t.State == taskfabric.StateCompleted {
		if answer, ok := completedSessionAnswer(h.kernel, dc.SessionID); ok {
			return answer, ""
		}
		if sessionAnswerFailed(h.kernel.fabric, dc.SessionID) {
			return result, "session answer task failed"
		}
		// A stall-resolved COMPLETED task with no answer is a dead session
		// (the sdk wait loop fails the same shape): surfacing the quantum
		// step placeholder as if it were the answer would re-open the P1
		// wrong-result hole. A later GET self-heals if the answer lands
		// after the verdict — the answer scan above runs first.
		if agentruntime.SessionStalled(h.kernel.fabric, dc.SessionID, t.ID) {
			return result, sessionStalledError
		}
		return result, ""
	}
	if t.State == taskfabric.StateFailed && errText == "" && sessionAnswerFailed(h.kernel.fabric, dc.SessionID) {
		errText = "session answer task failed"
	}
	return result, derivedTaskError(t, errText)
}

// derivedTaskError fills failure provenance when no persisted quantum cause
// exists: a cascaded task names the prerequisite whose failure doomed it.
func derivedTaskError(t *taskfabric.Task, errText string) string {
	if errText != "" || t.State != taskfabric.StateFailed || t.FailedDependency == "" {
		return errText
	}
	return "dependency " + t.FailedDependency + " failed"
}

// sessionAnswerFailed reports whether any answer task of the session is
// terminally FAILED — the session's sole exit is closed, so the external
// view surfaces that instead of an empty result. Mirrors the sdk wait loop's
// fast-failure check (sdk/l2_submit.go l2SessionAnswerFailed): the
// sess/<sid>/ boundary match keeps sibling sessions from shadowing each
// other.
func sessionAnswerFailed(f *taskfabric.Fabric, sessionID string) bool {
	if f == nil || sessionID == "" {
		return false
	}
	prefix := "sess/" + sessionID + "/"
	for _, id := range f.IDs() {
		if !strings.HasPrefix(id, prefix) || !strings.Contains(id, "/answer#") {
			continue
		}
		if tk, err := f.Task(id); err == nil && tk.State == taskfabric.StateFailed {
			return true
		}
	}
	return false
}

// waitForTaskResult polls the fabric until the task's external result
// channel resolves: terminal state AND, for session-scoped completions, the
// session answer readable (or confirmed unreachable — the answer node runs
// after the plan task, so COMPLETED alone does not mean the answer has
// landed). Contract:
//   - (task, true)  — result resolved; caller answers 200 with the task view
//   - (task, false) — task readable but unresolved within the wait; caller
//     degrades to the async 202 contract carrying the current state
//   - (nil, false)  — task never readable; plain 202 acceptance
//
// Polling uses a ticker (never a bare Sleep) so the context cancellation
// path is prompt. The stall detector supplies sustained evidence so the
// async answer-node compile gap is not mistaken for session death (same
// consecutive-poll contract as the sdk submit wait loop).
func waitForTaskResult(ctx context.Context, kernel *kernelHandle, taskID string, wait time.Duration) (*taskfabric.Task, bool) {
	if kernel == nil || kernel.fabric == nil || taskID == "" {
		return nil, false
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	stalls := &agentruntime.StallDetector{}
	var last *taskfabric.Task
	for {
		if t, err := kernel.fabric.Task(taskID); err == nil {
			last = t
			if resultResolved(kernel, t, taskID, stalls) {
				return t, true
			}
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-deadline.C:
			return last, false
		case <-ticker.C:
		}
	}
}

// resultResolved reports whether the external result channel for this task
// has fully resolved. Order mirrors the sdk wait contract: the answer scan
// runs FIRST, then the stall verdict, so a just-landed answer wins over a
// stall declared in the same poll.
func resultResolved(kernel *kernelHandle, t *taskfabric.Task, taskID string, stalls *agentruntime.StallDetector) bool {
	switch t.State {
	case taskfabric.StateFailed:
		return true
	case taskfabric.StateCompleted:
		dc, err := taskfabric.DecodeCheckpoint(t.Checkpoint)
		if err != nil || dc.SessionID == "" {
			return true
		}
		if _, ok := completedSessionAnswer(kernel, dc.SessionID); ok {
			return true
		}
		if sessionAnswerFailed(kernel.fabric, dc.SessionID) {
			return true
		}
		return stalls.Stalled(kernel.fabric, dc.SessionID, taskID)
	default:
		return false
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
		writeJSON(w, map[string]any{"error": "task not found", taskViewFieldTaskID: taskID})
		return
	}
	w.WriteHeader(http.StatusOK)
	writeJSON(w, h.taskStatusFromTask(t))
}
