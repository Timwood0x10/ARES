package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/ares_config"
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
