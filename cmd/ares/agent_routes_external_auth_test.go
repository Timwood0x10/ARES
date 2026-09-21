package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_security"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestExternalTaskSurfaceWriteGateFailClosed pins the POST /api/tasks auth
// contract at the route-registry dispatcher: the write gate is
// deny-by-default — no credential layer configured means 401 even on
// loopback, and a read-only (agent-role) JWT earns 403, not submission.
func TestExternalTaskSurfaceWriteGateFailClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kernel, _ := buildTestPeerKernel(t, ctx)

	t.Run("no credentials denied even on loopback", func(t *testing.T) {
		h := &actionHandler{inner: http.NotFoundHandler(), kernel: kernel}
		req := httptest.NewRequest(http.MethodPost, "/api/tasks",
			jsonBody(t, map[string]any{"query": "x"}))
		req.RemoteAddr = "127.0.0.1:55555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST /api/tasks without credentials = %d, want 401", rec.Code)
		}
	})

	t.Run("read-only jwt rejected at write gate", func(t *testing.T) {
		secret := []byte("m2-test-secret")
		token, err := ares_security.SignJWT(secret, "agent-7", string(ares_security.RoleAgent), time.Hour, time.Now())
		if err != nil {
			t.Fatalf("issue jwt: %v", err)
		}
		h := &actionHandler{
			inner:  http.NotFoundHandler(),
			kernel: kernel,
			auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
		}
		req := httptest.NewRequest(http.MethodPost, "/api/tasks",
			jsonBody(t, map[string]any{"query": "x"}))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST /api/tasks with agent-role jwt = %d, want 403", rec.Code)
		}
	})
}

// TestExternalTaskSurfaceReadGateFailClosed pins GET /api/tasks/{task_id}
// at the dispatcher: with any credential layer configured the read gate
// requires it (loopback included); with no layer configured only direct
// loopback passes and non-loopback clients are denied.
func TestExternalTaskSurfaceReadGateFailClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kernel, _ := buildTestPeerKernel(t, ctx)

	// Seed one task so credentialed reads can assert 200.
	if err := kernel.fabric.Create(&taskfabric.Task{ID: "gate-task-1", Capability: planCapability}); err != nil {
		t.Fatalf("fabric.Create: %v", err)
	}

	secret := []byte("m2-test-secret")
	authedHandler := func() *actionHandler {
		return &actionHandler{
			inner:    http.NotFoundHandler(),
			kernel:   kernel,
			apiKey:   "m2-api-key",
			auth:     ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
			readAuth: ares_security.NewAuthMiddleware(secret, ares_security.PermRead),
		}
	}

	t.Run("configured auth denies loopback without token", func(t *testing.T) {
		h := authedHandler()
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/gate-task-1", nil)
		req.RemoteAddr = "127.0.0.1:55555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET /api/tasks/{id} loopback with auth configured = %d, want 401", rec.Code)
		}
	})

	t.Run("invalid token denied", func(t *testing.T) {
		h := authedHandler()
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/gate-task-1", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET with invalid token = %d, want 401", rec.Code)
		}
	})

	t.Run("valid read jwt reaches the task view", func(t *testing.T) {
		token, err := ares_security.SignJWT(secret, "reader-1", string(ares_security.RoleAgent), time.Hour, time.Now())
		if err != nil {
			t.Fatalf("issue jwt: %v", err)
		}
		h := authedHandler()
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/gate-task-1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET with read jwt = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		var view taskStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatalf("decode view: %v", err)
		}
		if view.TaskID != "gate-task-1" {
			t.Fatalf("view.task_id = %q, want gate-task-1", view.TaskID)
		}
	})

	t.Run("api key grants read", func(t *testing.T) {
		h := authedHandler()
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/gate-task-1", nil)
		req.Header.Set("Authorization", "Bearer m2-api-key")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET with api key = %d, want 200", rec.Code)
		}
	})

	t.Run("unconfigured auth denies non-loopback", func(t *testing.T) {
		h := &actionHandler{inner: http.NotFoundHandler(), kernel: kernel}
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/gate-task-1", nil)
		// httptest default RemoteAddr is 192.0.2.1:1234 (non-loopback).
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET non-loopback with no credentials = %d, want 401", rec.Code)
		}
	})

	t.Run("unconfigured auth keeps loopback local-dev open", func(t *testing.T) {
		h := &actionHandler{inner: http.NotFoundHandler(), kernel: kernel}
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/gate-task-1", nil)
		req.RemoteAddr = "127.0.0.1:55555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET loopback with no credentials = %d, want 200 (local-dev posture, body: %s)",
				rec.Code, rec.Body.String())
		}
	})
}

// jsonBody marshals v into a request body reader.
func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(raw)
}
