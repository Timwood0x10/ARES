package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	}, fabric
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
	require.Len(t, fabric.ReadyTasks(), 1,
		"admission created the root, so the rejection happened at plan creation")
	assert.NotContains(t, fabric.ReadyTasks(), "peer-plan-1",
		"the plan task must be the one that was refused")
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
