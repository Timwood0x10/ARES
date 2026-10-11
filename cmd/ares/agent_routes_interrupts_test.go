package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_security"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// --- M3: HTTP transport tests for HITL approval gate ---

// interruptsTestKernel assembles a minimal kernelHandle with a fabric that
// has pending approval tasks seeded. Returns the handler and kernel for
// test interaction.
func interruptsTestKernel(t *testing.T) *kernelHandle {
	t.Helper()
	kernel := &kernelHandle{}
	kernel.fabric = taskfabric.NewFabric()

	// Seed a pending approval task.
	if err := kernel.fabric.Create(&taskfabric.Task{
		ID:               "gate-1",
		Capability:       planCapability,
		RequiresApproval: true,
		ApprovalMessage:  "needs human review",
	}); err != nil {
		t.Fatalf("fabric.Create gate-1: %v", err)
	}
	// Seed a second pending approval task.
	if err := kernel.fabric.Create(&taskfabric.Task{
		ID:               "gate-2",
		Capability:       planCapability,
		RequiresApproval: true,
		ApprovalMessage:  "second pending",
	}); err != nil {
		t.Fatalf("fabric.Create gate-2: %v", err)
	}
	// Seed a plain task (no approval) to verify it's excluded.
	if err := kernel.fabric.Create(&taskfabric.Task{
		ID:         "plain-1",
		Capability: planCapability,
	}); err != nil {
		t.Fatalf("fabric.Create plain-1: %v", err)
	}
	return kernel
}

// TestRouteListInterrupts_ReturnsPending verifies that GET /api/interrupts
// returns only tasks with pending (unresolved) HITL approval gates.
func TestRouteListInterrupts_ReturnsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	kernel := interruptsTestKernel(t)

	h := &actionHandler{inner: http.NotFoundHandler(), kernel: kernel}

	req := httptest.NewRequest(http.MethodGet, "/api/interrupts", nil)
	req.RemoteAddr = "127.0.0.1:55555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/interrupts = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Interrupts []taskfabric.ApprovalView `json:"interrupts"`
		Count      int                       `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("expected 2 pending interrupts, got %d", resp.Count)
	}
	// Must be sorted by TaskID.
	if resp.Interrupts[0].TaskID != "gate-1" || resp.Interrupts[1].TaskID != "gate-2" {
		t.Fatalf("interrupt order = %s, %s; want gate-1, gate-2",
			resp.Interrupts[0].TaskID, resp.Interrupts[1].TaskID)
	}
	if resp.Interrupts[0].Message != "needs human review" {
		t.Fatalf("gate-1 message = %q, want 'needs human review'", resp.Interrupts[0].Message)
	}
}

// TestRouteListInterrupts_NilKernel503 verifies that a nil kernel returns 503.
func TestRouteListInterrupts_NilKernel503(t *testing.T) {
	h := &actionHandler{inner: http.NotFoundHandler(), kernel: nil}

	req := httptest.NewRequest(http.MethodGet, "/api/interrupts", nil)
	req.RemoteAddr = "127.0.0.1:55555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/interrupts with nil kernel = %d, want 503", rec.Code)
	}
}

// TestRouteResolveInterrupt_RequiresAuthWrite verifies that
// POST /api/interrupts/{id}/resolve is denied without write credentials.
func TestRouteResolveInterrupt_RequiresAuthWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	kernel := interruptsTestKernel(t)

	secret := []byte("m3-test-secret")

	t.Run("no credentials denied", func(t *testing.T) {
		h := &actionHandler{
			inner:  http.NotFoundHandler(),
			kernel: kernel,
			auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
		}
		body := jsonBody(t, map[string]any{"approved": true})
		req := httptest.NewRequest(http.MethodPost, "/api/interrupts/gate-1/resolve", body)
		req.RemoteAddr = "127.0.0.1:55555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST resolve without credentials = %d, want 401", rec.Code)
		}
	})

	t.Run("read-only jwt rejected at write gate", func(t *testing.T) {
		token, err := ares_security.SignJWT(secret, "agent-3", string(ares_security.RoleAgent), time.Hour, time.Now())
		if err != nil {
			t.Fatalf("issue jwt: %v", err)
		}
		h := &actionHandler{
			inner:  http.NotFoundHandler(),
			kernel: kernel,
			auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
		}
		body := jsonBody(t, map[string]any{"approved": true})
		req := httptest.NewRequest(http.MethodPost, "/api/interrupts/gate-1/resolve", body)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST resolve with agent-role jwt = %d, want 403", rec.Code)
		}
	})

	// Verify the task is still pending (not side-effected).
	tk, err := kernel.fabric.Task("gate-1")
	if err != nil {
		t.Fatalf("fabric.Task: %v", err)
	}
	if tk.ApprovalDecision != "" {
		t.Fatalf("gate-1 must still be pending after denied resolve, got %q", tk.ApprovalDecision)
	}
}

// TestRouteResolveInterrupt_ApproveSchedules verifies that an authorized
// POST /api/interrupts/{id}/resolve with approved=true clears the gate and
// makes the task schedulable.
func TestRouteResolveInterrupt_ApproveSchedules(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	kernel := interruptsTestKernel(t)

	secret := []byte("m3-test-secret")
	token, err := ares_security.SignJWT(secret, "approver-1", string(ares_security.RoleOperator), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}

	h := &actionHandler{
		inner:  http.NotFoundHandler(),
		kernel: kernel,
		auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
	}

	body := jsonBody(t, map[string]any{"approved": true, "feedback": "looks good"})
	req := httptest.NewRequest(http.MethodPost, "/api/interrupts/gate-1/resolve", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST resolve approved = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		TaskID   string `json:"task_id"`
		Decision string `json:"decision"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TaskID != "gate-1" {
		t.Fatalf("task_id = %q, want gate-1", resp.TaskID)
	}
	if resp.Decision != taskfabric.ApprovalDecisionApproved {
		t.Fatalf("decision = %q, want %s", resp.Decision, taskfabric.ApprovalDecisionApproved)
	}

	// Task must now be schedulable.
	tk, err := kernel.fabric.Task("gate-1")
	if err != nil {
		t.Fatalf("fabric.Task: %v", err)
	}
	if tk.ApprovalDecision != taskfabric.ApprovalDecisionApproved {
		t.Fatalf("approval decision = %q, want approved", tk.ApprovalDecision)
	}
	if tk.ApprovalBy != "approver-1" {
		t.Fatalf("approval by = %q, want approver-1", tk.ApprovalBy)
	}
}

// TestRouteResolveInterrupt_RejectFails verifies that an authorized POST
// with approved=false drives the task to FAILED.
func TestRouteResolveInterrupt_RejectFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	kernel := interruptsTestKernel(t)

	secret := []byte("m3-test-secret")
	token, err := ares_security.SignJWT(secret, "approver-2", string(ares_security.RoleOperator), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}

	h := &actionHandler{
		inner:  http.NotFoundHandler(),
		kernel: kernel,
		auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
	}

	body := jsonBody(t, map[string]any{"approved": false, "feedback": "too risky"})
	req := httptest.NewRequest(http.MethodPost, "/api/interrupts/gate-2/resolve", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST resolve rejected = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	tk, err := kernel.fabric.Task("gate-2")
	if err != nil {
		t.Fatalf("fabric.Task: %v", err)
	}
	if tk.State != taskfabric.StateFailed {
		t.Fatalf("rejected task state = %s, want FAILED", tk.State)
	}
	if tk.ApprovalDecision != taskfabric.ApprovalDecisionRejected {
		t.Fatalf("approval decision = %q, want rejected", tk.ApprovalDecision)
	}
	if tk.ApprovalFeedback != "too risky" {
		t.Fatalf("feedback = %q, want 'too risky'", tk.ApprovalFeedback)
	}
}

// TestRouteResolveInterrupt_NotFound404 verifies that resolving a nonexistent
// task returns 404.
func TestRouteResolveInterrupt_NotFound404(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	kernel := interruptsTestKernel(t)

	secret := []byte("m3-test-secret")
	token, err := ares_security.SignJWT(secret, "approver-3", string(ares_security.RoleOperator), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}

	h := &actionHandler{
		inner:  http.NotFoundHandler(),
		kernel: kernel,
		auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
	}

	body := jsonBody(t, map[string]any{"approved": true})
	req := httptest.NewRequest(http.MethodPost, "/api/interrupts/nonexistent/resolve", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST resolve nonexistent = %d, want 404", rec.Code)
	}
}

// TestRouteResolveInterrupt_AlreadyResolved409 verifies that resolving an
// already-resolved task returns 409 Conflict.
func TestRouteResolveInterrupt_AlreadyResolved409(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	kernel := interruptsTestKernel(t)

	// First resolve as approved.
	if err := kernel.fabric.ResolveApproval("gate-1", taskfabric.ApprovalDecisionApproved, "ok", "r1"); err != nil {
		t.Fatalf("fabric.ResolveApproval: %v", err)
	}

	secret := []byte("m3-test-secret")
	token, err := ares_security.SignJWT(secret, "approver-4", string(ares_security.RoleOperator), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}

	h := &actionHandler{
		inner:  http.NotFoundHandler(),
		kernel: kernel,
		auth:   ares_security.NewAuthMiddleware(secret, ares_security.PermWrite),
	}

	// Try to reject the already-approved task.
	body := jsonBody(t, map[string]any{"approved": false})
	req := httptest.NewRequest(http.MethodPost, "/api/interrupts/gate-1/resolve", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("POST resolve already-resolved = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
}
