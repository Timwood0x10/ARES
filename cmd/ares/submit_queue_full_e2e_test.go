package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// newSubmitHarness wires the REAL chain the handler uses: actionHandler -> Submitter -> Sessions ->
// Fabric. cap=0 leaves the fabric unlimited (the default a deployment gets).
//
// prefill seeds the fabric with plain READY tasks, which is what an occupied queue looks like to the
// admission check.
func newSubmitHarness(t *testing.T, cap, prefill int) (*actionHandler, *taskfabric.Fabric) {
	h, fabric, _ := newSubmitHarnessWithSessions(t, cap, prefill)
	return h, fabric
}

// newSubmitHarnessWithSessions is the same wiring, also exposing the Sessions so a test can ask
// whether a rejected submission left a session (and its compiled root) behind.
func newSubmitHarnessWithSessions(t *testing.T, cap, prefill int) (*actionHandler, *taskfabric.Fabric, *agentruntime.Sessions) {
	t.Helper()
	fabric := taskfabric.NewFabric()
	if cap > 0 {
		fabric = fabric.WithMaxReadyTasks(cap)
	}
	for i := 0; i < prefill; i++ {
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: fmt.Sprintf("fill-%d", i)}))
	}
	sessions := &agentruntime.Sessions{
		Reg:     agentfabric.NewSessionRegistry(),
		Fabric:  fabric,
		Compile: planprojection.NewCompileCoordinator(fabric, nil),
	}
	return &actionHandler{
		kernel: &kernelHandle{fabric: fabric, submitter: agentruntime.NewSubmitter(sessions)},
	}, fabric, sessions
}

// submitThroughHandler drives the real handler, not the mapping helper: if the handler regressed to
// a blanket 500, every assertion below would fail.
func submitThroughHandler(t *testing.T, h *actionHandler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"capability":"plan","payload":{"input":"hello"}}`))
	rr := httptest.NewRecorder()
	h.handleSubmitTask(rr, req, nil)

	var body map[string]any
	if rr.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body), "body: %s", rr.Body.String())
	}
	return rr, body
}

// TestSubmitQueueFull_RejectedAtAdmission is the first case S03 named: the queue is already at
// capacity, so admitting the session (which compiles its root task) is what gets rejected.
func TestSubmitQueueFull_RejectedAtAdmission(t *testing.T) {
	h, fabric := newSubmitHarness(t, 1, 1) // one slot, already taken

	rr, body := submitThroughHandler(t, h)

	require.Equal(t, http.StatusTooManyRequests, rr.Code, "body: %s", rr.Body.String())
	assert.Equal(t, "queue_full", body["code"])
	assert.Equal(t, "error", body["status"])
	assert.Equal(t, float64(1), body["depth"])
	assert.Equal(t, float64(1), body["limit"])
	assert.Len(t, fabric.ReadyTasks(), 1, "a rejected submission must not add work")
}

// TestSubmitQueueFull_RejectedAtPlanCreation is the second case: admission succeeds (its root task
// takes the only slot) and the plan task is what the cap refuses.
//
// The root is the evidence that this differs from the case above — one task exists afterwards, and
// it is not the plan's.
func TestSubmitQueueFull_RejectedAtPlanCreation(t *testing.T) {
	h, fabric := newSubmitHarness(t, 1, 0) // empty, one slot

	rr, body := submitThroughHandler(t, h)

	require.Equal(t, http.StatusTooManyRequests, rr.Code, "body: %s", rr.Body.String())
	assert.Equal(t, "queue_full", body["code"])
	assert.Equal(t, float64(1), body["limit"])

	// The rejection happened AFTER admission (the root took the only slot), and the half-admitted
	// session is then cleaned up, so the observable end state is the same as a refusal at
	// admission. Which phase failed is asserted at the submitter layer, where the error chain
	// still names it — an earlier version of this test used the leftover root as that evidence,
	// which quietly made the leak look like a feature.
	assert.Empty(t, fabric.ReadyTasks(), "a refused submission must leave no work behind")
	assert.NotContains(t, fabric.ReadyTasks(), "peer-plan-1")
}

// TestSubmitQueueFull_UnlimitedAccepts is the control. Without it, a handler that returned 429 for
// everything would satisfy both tests above.
func TestSubmitQueueFull_UnlimitedAccepts(t *testing.T) {
	h, fabric := newSubmitHarness(t, 0, 0) // the default: no cap

	rr, body := submitThroughHandler(t, h)

	require.Equal(t, http.StatusAccepted, rr.Code, "body: %s", rr.Body.String())
	assert.NotEmpty(t, body["task_id"])
	assert.NotEmpty(t, fabric.ReadyTasks(), "an accepted submission leaves work behind")
}

// TestS04_RejectedSubmissionLeavesNoResidue is the first item of the plan's S04 checklist, and it
// reproduced before the fix: the 429 was correct, but admission had already registered the session
// and compiled a READY root task. Nothing would ever complete that root, the scheduler would run it
// with no plan behind it, and it held an admission slot — so a rejection made the next submission
// likelier to be rejected too.
func TestS04_RejectedSubmissionLeavesNoResidue(t *testing.T) {
	h, fabric, sessions := newSubmitHarnessWithSessions(t, 1, 0)

	rr, body := submitThroughHandler(t, h)
	require.Equal(t, http.StatusTooManyRequests, rr.Code, "body: %s", rr.Body.String())
	require.Equal(t, "queue_full", body["code"])

	assert.Empty(t, sessions.Reg.SessionIDs(),
		"a rejected submission must not leave an admitted session behind")
	assert.Empty(t, fabric.ReadyTasks(),
		"and must not leave the root task it compiled; the slot it takes is the slot it was refused")
}

// TestS04_ExistingSessionIsNotReleased is the other half of the same rule, and the reason the
// cleanup cannot simply release on any failure: a caller continuing an existing session must keep
// its graph and tasks when the new plan is refused. Losing that state is worse than the leak the
// previous test is about.
func TestS04_ExistingSessionIsNotReleased(t *testing.T) {
	h, fabric, sessions := newSubmitHarnessWithSessions(t, 1, 0)

	// Put the session in place the way a previous successful turn would have, filling the only
	// slot with its root.
	require.NoError(t, sessions.Admit(context.Background(), "live-session", "first turn"))
	require.Len(t, sessions.Reg.SessionIDs(), 1)
	require.Len(t, fabric.ReadyTasks(), 1)

	body := strings.NewReader(`{"capability":"plan","payload":{"input":"second turn","session_id":"live-session"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", body)
	rr := httptest.NewRecorder()
	h.handleSubmitTask(rr, req, nil)

	require.Equal(t, http.StatusTooManyRequests, rr.Code, "body: %s", rr.Body.String())
	assert.Equal(t, []string{"live-session"}, sessions.Reg.SessionIDs(),
		"an existing session must survive a refused continuation")
	assert.Len(t, fabric.ReadyTasks(), 1,
		"and its compiled root must not be harvested by someone else's failed plan")
}

// TestSubmitQueueFull_PhaseIsDistinguishable pins WHICH step refused the submission, because the
// HTTP response deliberately does not leak internal messages. The error chain does: a refused root
// compilation reads "compile session", a refused plan reads "create task".
func TestSubmitQueueFull_PhaseIsDistinguishable(t *testing.T) {
	t.Run("plan_creation", func(t *testing.T) {
		_, fabric, sessions := newSubmitHarnessWithSessions(t, 1, 0) // empty: root fits, plan does not
		sub := agentruntime.NewSubmitter(sessions)

		_, _, err := sub.Submit(context.Background(), "plan", map[string]any{"input": "x"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "create task", "the plan is what the cap refused")
		assert.Empty(t, fabric.ReadyTasks(), "the half-admitted session is cleaned up afterwards")
	})

	t.Run("admission", func(t *testing.T) {
		_, _, sessions := newSubmitHarnessWithSessions(t, 1, 1) // full: the root cannot be compiled
		sub := agentruntime.NewSubmitter(sessions)

		_, _, err := sub.Submit(context.Background(), "plan", map[string]any{"input": "x"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "compile session", "the root compilation is what the cap refused")
	})
}

// TestS04_ConcurrentSameSessionDoesNotTearDownAcceptedWork is an END-TO-END GUARD for the
// invariant, not a proof of the locking fix: the capacity is 2, so the first submission fits
// (root + plan) and the second joins the same session and is refused, and if any submission was
// accepted its session and task must still exist.
//
// Measured honestly: releasing the admission lock before plan creation does NOT make this test fail
// on this machine — the damaging interleaving lasts a few instructions, and 20 iterations do not
// reach it. The deterministic proof lives in the package where the lock is:
// TestAdmitAndDo_HoldsTheLockAcrossTheCallersWork (internal/agentruntime), which does fail the
// moment the lock is released early. This test stays because it exercises the real handler chain
// and would catch a coarse regression (for example, cleanup released unconditionally).
func TestS04_ConcurrentSameSessionDoesNotTearDownAcceptedWork(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		h, fabric, sessions := newSubmitHarnessWithSessions(t, 2, 0)

		type result struct {
			code   int
			taskID string
		}
		results := make([]result, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				body := strings.NewReader(`{"capability":"plan","payload":{"input":"go","session_id":"shared"}}`)
				req := httptest.NewRequest(http.MethodPost, "/api/tasks", body)
				rr := httptest.NewRecorder()
				h.handleSubmitTask(rr, req, nil)
				var out map[string]any
				_ = json.Unmarshal(rr.Body.Bytes(), &out)
				id, _ := out["task_id"].(string)
				results[idx] = result{code: rr.Code, taskID: id}
			}(i)
		}
		wg.Wait()

		accepted := 0
		for _, r := range results {
			if r.code == http.StatusAccepted {
				accepted++
				require.NotEmpty(t, r.taskID)

				assert.Equal(t, []string{"shared"}, sessions.Reg.SessionIDs(),
					"iteration %d: a submission was accepted but its session was torn down", iter)
				_, err := fabric.Task(r.taskID)
				assert.NoError(t, err, "iteration %d: accepted task %s is gone", iter, r.taskID)
			}
		}
		require.Equal(t, 1, accepted,
			"iteration %d: exactly one submission fits the capacity; codes were %v", iter, results)
	}
}
