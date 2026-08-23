package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// newGraphTestKernel is a chaos-style harness with STATIC capability
// executors registered, so submitted graphs resolve their nodes immediately.
func newGraphTestKernel(t *testing.T, ctx context.Context) (*actionHandler, *kernelHandle) {
	t.Helper()
	_, kh, fabric, _ := newChaosTestKernel(t, ctx, true)
	for _, cap := range []string{"research", "review", "write"} {
		kh.scheduler.RegisterExecutor("peer-"+cap, &chaosStubExecutor{
			id: "peer-" + cap, typ: models.AgentType(cap),
		})
	}
	_ = fabric
	handler := &actionHandler{kernel: kh, apiKey: "test-key"}
	return handler, kh
}

// postGraph submits a graph JSON payload to the endpoint.
func postGraph(t *testing.T, h *actionHandler, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphs", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestGraphsEndpointPipeline locks the pipeline mode (delegate/pipeline/
// orchestrate share one engine — the kernel fabric DAG): a serial chain runs
// in dependency order and every node's output is returned.
func TestGraphsEndpointPipeline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler, _ := newGraphTestKernel(t, ctx)

	body := `{
		"schema_version": 1,
		"run_id": "pipe-1",
		"nodes": [
			{"id": "s1", "capability": "research", "input": "topic"},
			{"id": "s2", "capability": "write",    "input": "draft"}
		],
		"edges": [{"from": "s1", "to": "s2"}]
	}`
	code, resp := postGraph(t, handler, body)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("status=%d resp=%v", code, resp)
	}
	outputs := resp["outputs"].(map[string]any)
	if outputs["s1"] == "" || outputs["s2"] == "" {
		t.Fatalf("pipeline outputs missing: %v", outputs)
	}
}

// TestGraphsEndpointFanOutJoin locks the orchestration mode: two workers run
// after the root; the join node waits for BOTH (dependency fan-in), proving
// round-barrier ordering through kernel Dependencies.
func TestGraphsEndpointFanOutJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler, kh := newGraphTestKernel(t, ctx)

	var joinRanAfterBoth bool
	joinExec := &orderProbeExecutor{id: "peer-review", onExecute: func() {
		tkA, _ := kh.fabric.Task("collab-g-fj-w1")
		tkB, _ := kh.fabric.Task("collab-g-fj-w2")
		sa, sb := "nil", "nil"
		if tkA != nil {
			sa = string(tkA.State)
		}
		if tkB != nil {
			sb = string(tkB.State)
		}
		t.Logf("probe: w1=%s w2=%s", sa, sb)
		joinRanAfterBoth = tkA != nil && tkB != nil &&
			tkA.State == taskfabric.StateCompleted && tkB.State == taskfabric.StateCompleted
	}}
	kh.scheduler.RegisterExecutor("peer-review", joinExec)

	body := `{
		"schema_version": 1,
		"run_id": "g-fj",
		"nodes": [
			{"id": "root",   "capability": "research"},
			{"id": "w1",     "capability": "research"},
			{"id": "w2",     "capability": "write"},
			{"id": "join",   "capability": "review"}
		],
		"edges": [
			{"from": "root", "to": "w1"},
			{"from": "root", "to": "w2"},
			{"from": "w1",  "to": "join"},
			{"from": "w2",  "to": "join"}
		]
	}`
	code, resp := postGraph(t, handler, body)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("status=%d resp=%v", code, resp)
	}
	if !joinRanAfterBoth {
		t.Fatal("join node must execute only after both worker tasks completed")
	}
}

// TestGraphsEndpointRejectsUnknownCapability locks defensive validation:
// an un-servable capability is rejected BEFORE any task is created.
func TestGraphsEndpointRejectsUnknownCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler, kh := newGraphTestKernel(t, ctx)

	body := `{"schema_version":1,"nodes":[{"id":"x","capability":"nonexistent"}],"edges":[]}`
	code, resp := postGraph(t, handler, body)
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", code)
	}
	if !strings.Contains(resp["error"].(string), "no peer executor") {
		t.Fatalf("error = %v", resp["error"])
	}
	// Nothing leaked into the fabric.
	if _, err := kh.fabric.Task("collab-x"); err == nil {
		t.Fatal("rejected graph must not create tasks")
	}
}

// TestGraphsEndpointSchemaVersionGuard covers the wire-evolution guard.
func TestGraphsEndpointSchemaVersionGuard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler, _ := newGraphTestKernel(t, ctx)
	code, resp := postGraph(t, handler, `{"schema_version":2,"nodes":[{"id":"x","capability":"research"}]}`)
	if code != http.StatusBadRequest || !strings.Contains(resp["error"].(string), "schema_version") {
		t.Fatalf("status=%d resp=%v", code, resp)
	}
}

// orderProbeExecutor wraps the stub with a post-execution hook so tests can
// observe fabric state at execution time (join-after-both-workers proof).
type orderProbeExecutor struct {
	id        string
	onExecute func()
}

func (e *orderProbeExecutor) ID() string             { return e.id }
func (e *orderProbeExecutor) Type() models.AgentType { return "review" }
func (e *orderProbeExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	e.onExecute()
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "probed")
	return &sub.StepOutcome{Done: true, Result: res}, nil
}
