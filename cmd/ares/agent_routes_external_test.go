package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/sdk"
)

// buildNoSchedulerKernel assembles the submission path (fabric + sessions +
// submitter) WITHOUT starting the scheduler loop, so submitted tasks stay
// non-terminal — the wait-timeout degrade path.
func buildNoSchedulerKernel(t *testing.T) *kernelHandle {
	t.Helper()
	kernel := &kernelHandle{}
	kernel.fabric = taskfabric.NewFabric()
	sessions := &agentruntime.Sessions{
		Reg:     agentfabric.NewSessionRegistry(),
		Fabric:  kernel.fabric,
		Compile: planprojection.NewCompileCoordinator(kernel.fabric, nil),
	}
	kernel.sessionReg = sessions.Reg
	kernel.compileCoord = sessions.Compile
	kernel.submitter = agentruntime.NewSubmitter(sessions)
	return kernel
}

// TestParseTaskWaitParam locks the POST /api/tasks wait-parameter contract:
// absent → async; empty → configured default; explicit duration honored;
// invalid/non-positive/over-cap rejected.
func TestParseTaskWaitParam(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		present bool
		def     time.Duration
		wantDur time.Duration
		wantReq bool
		wantErr bool
	}{
		{name: "absent means async", values: nil, present: false,
			def: 60 * time.Second, wantDur: 0, wantReq: false},
		{name: "present empty uses default", values: []string{""}, present: true,
			def: 45 * time.Second, wantDur: 45 * time.Second, wantReq: true},
		{name: "zero default falls back to 60s", values: []string{""}, present: true,
			def: 0, wantDur: taskWaitDefaultDuration, wantReq: true},
		{name: "explicit duration wins", values: []string{"5s"}, present: true,
			def: 60 * time.Second, wantDur: 5 * time.Second, wantReq: true},
		{name: "invalid duration rejected", values: []string{"soon"}, present: true,
			def: 60 * time.Second, wantErr: true},
		{name: "non-positive rejected", values: []string{"-3s"}, present: true,
			def: 60 * time.Second, wantErr: true},
		{name: "over cap rejected", values: []string{"301s"}, present: true,
			def: 60 * time.Second, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dur, req, err := parseTaskWaitParam(tc.values, tc.present, tc.def)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dur=%v req=%v", dur, req)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req != tc.wantReq || dur != tc.wantDur {
				t.Fatalf("got (%v, %v), want (%v, %v)", dur, req, tc.wantDur, tc.wantReq)
			}
		})
	}
}

// TestResolveExternalTaskDefaults locks the ares.yaml → handler defaults:
// server.default_capability and tasks.wait_timeout (clamped to the 300s cap),
// including the nil/empty fallbacks.
func TestResolveExternalTaskDefaults(t *testing.T) {
	if got := resolveDefaultCapability(nil); got != planCapability {
		t.Fatalf("nil cfg capability = %q, want %q", got, planCapability)
	}
	if got := resolveDefaultCapability(&ares_config.Config{}); got != planCapability {
		t.Fatalf("empty cfg capability = %q, want %q", got, planCapability)
	}
	cfg := &ares_config.Config{}
	cfg.Server.DefaultCapability = "custom/cap"
	if got := resolveDefaultCapability(cfg); got != "custom/cap" {
		t.Fatalf("capability = %q, want custom/cap", got)
	}

	if got := resolveTaskWaitDefault(nil); got != taskWaitDefaultDuration {
		t.Fatalf("nil cfg wait = %v, want %v", got, taskWaitDefaultDuration)
	}
	badCfg := &ares_config.Config{}
	badCfg.Tasks.WaitTimeout = "not-a-duration"
	if got := resolveTaskWaitDefault(badCfg); got != taskWaitDefaultDuration {
		t.Fatalf("unparseable wait = %v, want fallback %v", got, taskWaitDefaultDuration)
	}
	okCfg := &ares_config.Config{}
	okCfg.Tasks.WaitTimeout = "90s"
	if got := resolveTaskWaitDefault(okCfg); got != 90*time.Second {
		t.Fatalf("wait = %v, want 90s", got)
	}
	hugeCfg := &ares_config.Config{}
	hugeCfg.Tasks.WaitTimeout = "1h"
	if got := resolveTaskWaitDefault(hugeCfg); got != taskWaitMaxDuration {
		t.Fatalf("clamped wait = %v, want cap %v", got, taskWaitMaxDuration)
	}
}

// authorizedExternalRequest builds an authenticated request against the given
// path with the given body.
func authorizedExternalRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	return req
}

// TestHTTPSubmitTaskQueryOnlyExternalEntry is the external one-interface
// acceptance at the handler level: {"query": "..."} with no capability
// submits under the yaml default capability, folds the query into the
// canonical payload keys, and a follow-up GET reads the task back.
func TestHTTPSubmitTaskQueryOnlyExternalEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kernel, _ := buildTestPeerKernel(t, ctx)

	h := &actionHandler{
		kernel:            kernel,
		apiKey:            "test-key",
		defaultCapability: planCapability,
		taskWaitDefault:   taskWaitDefaultDuration,
	}
	body, _ := json.Marshal(map[string]any{"query": "solve the incident"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost, "/api/tasks", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	taskID, _ := resp["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id in %v", resp)
	}

	// The fabric task must carry the default capability and the folded query.
	got, taskErr := kernel.fabric.Task(taskID)
	if taskErr != nil {
		t.Fatalf("fabric.Task(%s): %v", taskID, taskErr)
	}
	if got.Capability != planCapability {
		t.Errorf("capability = %q, want %q", got.Capability, planCapability)
	}
	env, ok := got.Checkpoint.(*taskfabric.CheckpointEnvelope)
	if !ok || env == nil {
		t.Fatalf("checkpoint envelope missing: %#v", got.Checkpoint)
	}
	if env.Payload[taskPayloadInput] != "solve the incident" {
		t.Errorf("payload[%q] = %v, want folded query", taskPayloadInput, env.Payload[taskPayloadInput])
	}
	if env.Payload[taskPayloadDesc] != "solve the incident" {
		t.Errorf("payload[%q] = %v, want folded query", taskPayloadDesc, env.Payload[taskPayloadDesc])
	}

	// GET /api/tasks/{id} reads the slim task view back.
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, authorizedExternalRequest(t, http.MethodGet, "/api/tasks/"+taskID, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body: %s)", getRec.Code, getRec.Body.String())
	}
	var view taskStatusResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode task view: %v", err)
	}
	if view.TaskID != taskID || view.Capability != planCapability {
		t.Errorf("view = %+v, want task_id=%s capability=%s", view, taskID, planCapability)
	}
	if view.State == "" {
		t.Error("view.state must be non-empty")
	}
}

// TestHTTPSubmitTaskValidationAndBadRequest pins the guard contracts: missing
// query+capability, invalid wait, and the 404/400 GET shapes.
func TestHTTPSubmitTaskValidationAndBadRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kernel, _ := buildTestPeerKernel(t, ctx)
	h := &actionHandler{kernel: kernel, apiKey: "test-key", taskWaitDefault: taskWaitDefaultDuration}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost, "/api/tasks",
		[]byte(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost,
		"/api/tasks?wait=soon", []byte(`{"query":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid wait status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost,
		"/api/tasks?wait=999s", []byte(`{"query":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap wait status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodGet, "/api/tasks/ghost-id", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodGet, "/api/tasks/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty task id status = %d, want 400", rec.Code)
	}
}

// TestHTTPSubmitTaskWaitTerminalReturns200 covers the sync-wait success
// path: the test peer cognition completes every task in one quantum, so
// ?wait=2s lands on COMPLETED and returns the task view (200) instead of the
// async acceptance.
func TestHTTPSubmitTaskWaitTerminalReturns200(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kernel, _ := buildTestPeerKernel(t, ctx)
	h := &actionHandler{kernel: kernel, apiKey: "test-key", taskWaitDefault: taskWaitDefaultDuration}

	body, _ := json.Marshal(map[string]any{"query": "finish fast"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost, "/api/tasks?wait=2s", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var view taskStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if view.State != string(taskfabric.StateCompleted) {
		t.Fatalf("state = %q, want COMPLETED", view.State)
	}
	if view.TaskID == "" {
		t.Fatal("missing task_id in terminal wait response")
	}
}

// TestHTTPSubmitTaskWaitTimeoutDegradesTo202 pins the timeout contract: a
// kernel without a scheduler leaves tasks non-terminal, so ?wait=1ms expires
// and the handler still answers 202 — submission never fails on wait expiry.
func TestHTTPSubmitTaskWaitTimeoutDegradesTo202(t *testing.T) {
	kernel := buildNoSchedulerKernel(t)
	h := &actionHandler{kernel: kernel, apiKey: "test-key", taskWaitDefault: taskWaitDefaultDuration}

	body, _ := json.Marshal(map[string]any{"query": "never finishes"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost, "/api/tasks?wait=1ms", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 on wait expiry (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != taskStatusSubmitted {
		t.Fatalf("status field = %v, want %q", resp["status"], taskStatusSubmitted)
	}
}

// TestResolveServeAPIKey locks the HTTP control-plane credential precedence:
// security.api_key wins when set; empty falls back to llm.api_key; nil cfg
// yields "" (deny-by-default).
func TestResolveServeAPIKey(t *testing.T) {
	if got := resolveServeAPIKey(nil); got != "" {
		t.Fatalf("nil cfg key = %q, want empty", got)
	}
	legacy := &ares_config.Config{}
	legacy.LLM.APIKey = "llm-key"
	if got := resolveServeAPIKey(legacy); got != "llm-key" {
		t.Fatalf("legacy fallback key = %q, want llm-key", got)
	}
	dedicated := &ares_config.Config{}
	dedicated.LLM.APIKey = "llm-key"
	dedicated.Security.APIKey = "http-key"
	if got := resolveServeAPIKey(dedicated); got != "http-key" {
		t.Fatalf("precedence key = %q, want http-key (security.api_key must win)", got)
	}
	onlyHTTP := &ares_config.Config{}
	onlyHTTP.Security.APIKey = "http-key"
	if got := resolveServeAPIKey(onlyHTTP); got != "http-key" {
		t.Fatalf("http-only key = %q, want http-key", got)
	}
}

// TestResolveRunWait locks the `ares run` sync-wait resolution:
// tasks.wait_timeout wins when set, the 300s hard cap applies, and
// unset/unparseable values fall back to the CLI per-surface default.
func TestResolveRunWait(t *testing.T) {
	if got := resolveRunWait(nil); got != runWaitDefault {
		t.Fatalf("nil cfg wait = %v, want %v", got, runWaitDefault)
	}
	empty := &sdk.ConfigFile{}
	if got := resolveRunWait(empty); got != runWaitDefault {
		t.Fatalf("empty wait = %v, want %v", got, runWaitDefault)
	}
	set := &sdk.ConfigFile{}
	set.Tasks.WaitTimeout = "45s"
	if got := resolveRunWait(set); got != 45*time.Second {
		t.Fatalf("configured wait = %v, want 45s", got)
	}
	huge := &sdk.ConfigFile{}
	huge.Tasks.WaitTimeout = "2h"
	if got := resolveRunWait(huge); got != taskWaitMaxDuration {
		t.Fatalf("clamped wait = %v, want cap %v", got, taskWaitMaxDuration)
	}
	bad := &sdk.ConfigFile{}
	bad.Tasks.WaitTimeout = "soon"
	if got := resolveRunWait(bad); got != runWaitDefault {
		t.Fatalf("unparseable wait = %v, want fallback %v", got, runWaitDefault)
	}
}

// seedSessionPlanTask creates a session-scoped plan task in the fabric and
// drives it to the requested terminal state through the real lifecycle
// (Acquire → Start → Complete/Fail) — the fabric-side shape the L2
// execution core produces for an external submission.
func seedSessionPlanTask(t *testing.T, f *taskfabric.Fabric, planID, sessionID string, lastErr error) {
	t.Helper()
	env := taskfabric.NewCheckpointEnvelope(map[string]any{"input": "seeded"})
	env.SessionID = sessionID
	if err := f.Create(&taskfabric.Task{
		ID:          planID,
		Capability:  planCapability,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
		Checkpoint:  env,
	}); err != nil {
		t.Fatalf("create plan task: %v", err)
	}
	epoch, err := f.Acquire(planID, "coder", time.Minute)
	if err != nil {
		t.Fatalf("acquire plan task: %v", err)
	}
	if err := f.Start(planID, "coder", epoch); err != nil {
		t.Fatalf("start plan task: %v", err)
	}
	if lastErr != nil {
		if err := f.Fail(planID, "coder", epoch, lastErr); err != nil {
			t.Fatalf("fail plan task: %v", err)
		}
		return
	}
	step := taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
		SessionID:      sessionID,
		StepCheckpoint: map[string]any{"result": "ok"},
	})
	if err := f.CompleteWithCheckpoint(planID, "coder", epoch, step); err != nil {
		t.Fatalf("complete plan task: %v", err)
	}
}

// seedSessionAnswerTask creates the session's terminal answer task with the
// given content and drives it to the requested state — the L2 path's real
// user-facing output channel (items[0].Content).
func seedSessionAnswerTask(t *testing.T, f *taskfabric.Fabric, sessionID, content string, complete bool) {
	t.Helper()
	id := "sess/" + sessionID + "/d1/answer#0"
	cp := map[string]any{
		"items": []*models.RecommendItem{{Content: content}},
	}
	if err := f.Create(&taskfabric.Task{
		ID:          id,
		Capability:  answerCapability,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
		Checkpoint:  cp,
	}); err != nil {
		t.Fatalf("create answer task: %v", err)
	}
	epoch, err := f.Acquire(id, "coder", time.Minute)
	if err != nil {
		t.Fatalf("acquire answer task: %v", err)
	}
	if err := f.Start(id, "coder", epoch); err != nil {
		t.Fatalf("start answer task: %v", err)
	}
	if !complete {
		if err := f.Fail(id, "coder", epoch, errors.New("answer node died")); err != nil {
			t.Fatalf("fail answer task: %v", err)
		}
		return
	}
	if err := f.CompleteWithCheckpoint(id, "coder", epoch, map[string]any{"items": cp["items"]}); err != nil {
		t.Fatalf("complete answer task: %v", err)
	}
}

// externalKernel builds a handler over a bare fabric-backed kernel — no
// scheduler — so tests drive fabric state directly and assert the external
// view contract.
func externalKernel(f *taskfabric.Fabric) *actionHandler {
	kernel := &kernelHandle{fabric: f}
	return &actionHandler{kernel: kernel, apiKey: "test-key", taskWaitDefault: taskWaitDefaultDuration}
}

// getTaskView issues GET /api/tasks/{id} through the dispatcher and returns
// the decoded external view.
func getTaskView(t *testing.T, h *actionHandler, taskID string) taskStatusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodGet, "/api/tasks/"+taskID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var view taskStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	return view
}

// TestGetTaskResultChannel locks the P1 fix: the external view surfaces the
// SESSION ANSWER as result on the L2 path (not the plan task's step
// checkpoint), and surfaces a failure cause on FAILED.
func TestGetTaskResultChannel(t *testing.T) {
	t.Run("completed session answer wins over step checkpoint", func(t *testing.T) {
		f := taskfabric.NewFabric()
		h := externalKernel(f)
		seedSessionPlanTask(t, f, "peer-plan-ans", "sess-auto-ans", nil)
		seedSessionAnswerTask(t, f, "sess-auto-ans", "pong", true)
		view := getTaskView(t, h, "peer-plan-ans")
		if view.State != string(taskfabric.StateCompleted) {
			t.Fatalf("state = %q, want COMPLETED", view.State)
		}
		if view.Result != "pong" {
			t.Fatalf("result = %#v, want session answer \"pong\"", view.Result)
		}
		if view.Error != "" {
			t.Fatalf("error = %q, want empty on success", view.Error)
		}
	})

	t.Run("completed without answer falls back to step checkpoint", func(t *testing.T) {
		f := taskfabric.NewFabric()
		h := externalKernel(f)
		seedSessionPlanTask(t, f, "peer-plan-plain", "sess-auto-plain", nil)
		view := getTaskView(t, h, "peer-plan-plain")
		if view.State != string(taskfabric.StateCompleted) {
			t.Fatalf("state = %q, want COMPLETED", view.State)
		}
		sc, ok := view.Result.(map[string]any)
		if !ok || sc["result"] != "ok" {
			t.Fatalf("result = %#v, want step-checkpoint fallback map", view.Result)
		}
	})

	t.Run("failed task surfaces persisted quantum cause", func(t *testing.T) {
		f := taskfabric.NewFabric()
		h := externalKernel(f)
		seedSessionPlanTask(t, f, "peer-plan-fail", "sess-auto-fail", errors.New("provider returned empty response"))
		view := getTaskView(t, h, "peer-plan-fail")
		if view.State != string(taskfabric.StateFailed) {
			t.Fatalf("state = %q, want FAILED", view.State)
		}
		if view.Error != "provider returned empty response" {
			t.Fatalf("error = %q, want the persisted quantum cause", view.Error)
		}
	})

	t.Run("failed plan with failed answer node reports session answer failure", func(t *testing.T) {
		f := taskfabric.NewFabric()
		h := externalKernel(f)
		seedSessionPlanTask(t, f, "peer-plan-af", "sess-auto-af", errors.New("plan step died"))
		seedSessionAnswerTask(t, f, "sess-auto-af", "", false)
		view := getTaskView(t, h, "peer-plan-af")
		// The persisted quantum cause outranks the derived session message.
		if view.Error != "plan step died" {
			t.Fatalf("error = %q, want persisted cause \"plan step died\"", view.Error)
		}
	})

	t.Run("completed plan with failed answer node reports session answer failure", func(t *testing.T) {
		f := taskfabric.NewFabric()
		h := externalKernel(f)
		seedSessionPlanTask(t, f, "peer-plan-cf", "sess-auto-cf", nil)
		seedSessionAnswerTask(t, f, "sess-auto-cf", "", false)
		view := getTaskView(t, h, "peer-plan-cf")
		if view.State != string(taskfabric.StateCompleted) {
			t.Fatalf("state = %q, want COMPLETED", view.State)
		}
		if view.Error != "session answer task failed" {
			t.Fatalf("error = %q, want derived session-answer failure", view.Error)
		}
	})
}

// TestGetTaskCascadedFailureDerivesDependencyError locks the provenance
// fallback: with no persisted quantum cause, a cascade-failed task names the
// prerequisite whose failure doomed it.
func TestGetTaskCascadedFailureDerivesDependencyError(t *testing.T) {
	f := taskfabric.NewFabric()
	h := externalKernel(f)
	if err := f.Create(&taskfabric.Task{
		ID:          "dep-root",
		Capability:  planCapability,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
	}); err != nil {
		t.Fatalf("create dep-root: %v", err)
	}
	if err := f.Create(&taskfabric.Task{
		ID:           "dep-downstream",
		Capability:   planCapability,
		Dependencies: []string{"dep-root"},
		RetryPolicy:  taskfabric.RetryPolicy{MaxRetries: 0},
	}); err != nil {
		t.Fatalf("create downstream: %v", err)
	}
	epoch, err := f.Acquire("dep-root", "coder", time.Minute)
	if err != nil {
		t.Fatalf("acquire dep-root: %v", err)
	}
	if err := f.Start("dep-root", "coder", epoch); err != nil {
		t.Fatalf("start dep-root: %v", err)
	}
	if err := f.Fail("dep-root", "coder", epoch, nil); err != nil {
		t.Fatalf("fail dep-root: %v", err)
	}
	view := getTaskView(t, h, "dep-downstream")
	if view.State != string(taskfabric.StateFailed) {
		t.Fatalf("state = %q, want cascaded FAILED", view.State)
	}
	if view.Error != "dependency dep-root failed" {
		t.Fatalf("error = %q, want derived cascade provenance", view.Error)
	}
}

// TestWaitForTaskResultResolution locks the ?wait= result-channel contract:
// the wait resolves on the session answer (not merely the plan task's
// COMPLETED state), resolves on confirmed answer failure, resolves via the
// stall detector when no answer can ever come, and reports the readable task
// back on wait expiry so the handler can degrade to 202 with state.
func TestWaitForTaskResultResolution(t *testing.T) {
	t.Run("resolves when session answer lands", func(t *testing.T) {
		f := taskfabric.NewFabric()
		kernel := &kernelHandle{fabric: f}
		seedSessionPlanTask(t, f, "w-plan-ans", "sess-w-ans", nil)
		seedSessionAnswerTask(t, f, "sess-w-ans", "late answer", true)
		got, resolved := waitForTaskResult(context.Background(), kernel, "w-plan-ans", 2*time.Second)
		if !resolved || got == nil {
			t.Fatalf("waitForTaskResult = (%v, %v), want resolved task", got, resolved)
		}
	})

	t.Run("resolves on confirmed session answer failure", func(t *testing.T) {
		f := taskfabric.NewFabric()
		kernel := &kernelHandle{fabric: f}
		seedSessionPlanTask(t, f, "w-plan-af", "sess-w-af", nil)
		seedSessionAnswerTask(t, f, "sess-w-af", "", false)
		got, resolved := waitForTaskResult(context.Background(), kernel, "w-plan-af", 2*time.Second)
		if !resolved || got == nil {
			t.Fatalf("waitForTaskResult = (%v, %v), want resolved on answer failure", got, resolved)
		}
	})

	t.Run("resolves via stall detector when no answer can come", func(t *testing.T) {
		f := taskfabric.NewFabric()
		kernel := &kernelHandle{fabric: f}
		seedSessionPlanTask(t, f, "w-plan-stall", "sess-w-stall", nil)
		// SessionStalled requires at least one terminal task under the
		// session prefix (production Sessions.Admit always creates the
		// sess/<sid>/root node). Seed that terminal root: no answer task
		// exists and every session task is terminal — the dead-session
		// shape. StallConfirmations consecutive polls at the 200ms cadence
		// resolve well inside the 5s budget.
		rootID := "sess/sess-w-stall/root"
		if err := f.Create(&taskfabric.Task{
			ID:          rootID,
			Capability:  planCapability,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
		}); err != nil {
			t.Fatalf("create session root: %v", err)
		}
		epoch, err := f.Acquire(rootID, "coder", time.Minute)
		if err != nil {
			t.Fatalf("acquire session root: %v", err)
		}
		if err := f.Start(rootID, "coder", epoch); err != nil {
			t.Fatalf("start session root: %v", err)
		}
		if err := f.Complete(rootID, "coder", epoch); err != nil {
			t.Fatalf("complete session root: %v", err)
		}
		got, resolved := waitForTaskResult(context.Background(), kernel, "w-plan-stall", 5*time.Second)
		if !resolved || got == nil {
			t.Fatalf("waitForTaskResult = (%v, %v), want stall-resolved task", got, resolved)
		}
	})

	t.Run("wait expiry returns readable task unresolved", func(t *testing.T) {
		f := taskfabric.NewFabric()
		kernel := &kernelHandle{fabric: f}
		if err := f.Create(&taskfabric.Task{
			ID:          "w-plan-pending",
			Capability:  planCapability,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
		}); err != nil {
			t.Fatalf("create pending task: %v", err)
		}
		got, resolved := waitForTaskResult(context.Background(), kernel, "w-plan-pending", 30*time.Millisecond)
		if resolved {
			t.Fatal("pending task must not resolve")
		}
		if got == nil || got.ID != "w-plan-pending" {
			t.Fatalf("got = %v, want the readable pending task back for the 202 state degrade", got)
		}
	})
}

// TestHTTPSubmitTaskWaitTimeout202CarriesState pins the handler-level
// degrade contract: an expired ?wait= answers 202 (submission never fails
// on wait expiry) AND carries the task's current state so the client can
// resume polling GET.
func TestHTTPSubmitTaskWaitTimeout202CarriesState(t *testing.T) {
	kernel := buildNoSchedulerKernel(t)
	h := &actionHandler{kernel: kernel, apiKey: "test-key", taskWaitDefault: taskWaitDefaultDuration}

	body, _ := json.Marshal(map[string]any{"query": "never finishes"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authorizedExternalRequest(t, http.MethodPost, "/api/tasks?wait=1ms", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 on wait expiry (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != taskStatusSubmitted {
		t.Fatalf("status field = %v, want %q", resp["status"], taskStatusSubmitted)
	}
	state, _ := resp["state"].(string)
	if state == "" {
		t.Fatalf("202 degrade body must carry the current state, got %v", resp)
	}
}

// TestGetTaskStallResolvedViewSurfacesError locks the review-fix contract:
// a COMPLETED session-scoped task whose session produced no answer (stall
// verdict) must NOT present the quantum step placeholder as a successful
// result — the external view reports the stall as an error instead.
func TestGetTaskStallResolvedViewSurfacesError(t *testing.T) {
	f := taskfabric.NewFabric()
	h := externalKernel(f)
	seedSessionPlanTask(t, f, "peer-plan-dead", "sess-auto-dead", nil)
	rootID := "sess/sess-auto-dead/root"
	if err := f.Create(&taskfabric.Task{
		ID:          rootID,
		Capability:  planCapability,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
	}); err != nil {
		t.Fatalf("create session root: %v", err)
	}
	epoch, err := f.Acquire(rootID, "coder", time.Minute)
	if err != nil {
		t.Fatalf("acquire session root: %v", err)
	}
	if err := f.Start(rootID, "coder", epoch); err != nil {
		t.Fatalf("start session root: %v", err)
	}
	if err := f.Complete(rootID, "coder", epoch); err != nil {
		t.Fatalf("complete session root: %v", err)
	}
	view := getTaskView(t, h, "peer-plan-dead")
	if view.State != string(taskfabric.StateCompleted) {
		t.Fatalf("state = %q, want COMPLETED", view.State)
	}
	if view.Error != sessionStalledError {
		t.Fatalf("error = %q, want %q", view.Error, sessionStalledError)
	}
}
