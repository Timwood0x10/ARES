package taskfabric

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestFailStampsLastErrorOnTerminalFailure locks the external-view error
// contract: a terminal Fail carries the quantum cause into the checkpoint's
// LastError so GET /api/tasks/{id} can surface WHY the task failed.
func TestFailStampsLastErrorOnTerminalFailure(t *testing.T) {
	f := NewFabric()
	tk := newTask("t-fail")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 0}
	tk.Checkpoint = NewCheckpointEnvelope(map[string]any{"input": "do the thing"})
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t-fail", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := f.Start("t-fail", "agent-a", epoch); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cause := errors.New("llm step boom")
	if err := f.Fail("t-fail", "agent-a", epoch, cause); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	got, err := f.Task("t-fail")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if got.State != StateFailed {
		t.Fatalf("state = %s, want FAILED", got.State)
	}
	dc, err := DecodeCheckpoint(got.Checkpoint)
	if err != nil {
		t.Fatalf("DecodeCheckpoint: %v", err)
	}
	if dc.LastError != cause.Error() {
		t.Fatalf("LastError = %q, want %q", dc.LastError, cause.Error())
	}
	if dc.Payload["input"] != "do the thing" {
		t.Fatalf("payload must survive the cause stamp, got %v", dc.Payload)
	}
}

// TestFailRequeueLeavesCheckpointUntouched locks the requeue half of the
// contract: a retryable failure must not stamp LastError — the task
// continues, and a stale failure cause must not shadow a later outcome.
func TestFailRequeueLeavesCheckpointUntouched(t *testing.T) {
	f := NewFabric()
	tk := newTask("t-requeue")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 2}
	tk.Checkpoint = NewCheckpointEnvelope(map[string]any{"input": "retry me"})
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t-requeue", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := f.Start("t-requeue", "agent-a", epoch); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := f.Fail("t-requeue", "agent-a", epoch, errors.New("transient")); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	got, err := f.Task("t-requeue")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if got.State != StateReady {
		t.Fatalf("state = %s, want READY requeue", got.State)
	}
	dc, err := DecodeCheckpoint(got.Checkpoint)
	if err != nil {
		t.Fatalf("DecodeCheckpoint: %v", err)
	}
	if dc.LastError != "" {
		t.Fatalf("LastError = %q on a requeued task, want empty", dc.LastError)
	}
	if dc.Payload["input"] != "retry me" {
		t.Fatalf("payload must survive the requeue, got %v", dc.Payload)
	}
}

// TestRunQuantumPersistsStepErrorCause locks the production fail path: the
// step error RunQuantum converts into a fabric failure is the cause the
// external view later reads. A nil-cause Fail (operator kill) leaves
// LastError empty — absence is meaningful, not a zero-value bug.
func TestRunQuantumPersistsStepErrorCause(t *testing.T) {
	f := NewFabric()
	tk := newTask("t-quantum")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 0}
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t-quantum", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	stepErr := errors.New("provider returned empty response")
	err = f.RunQuantum("t-quantum", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, stepErr
	})
	if !errors.Is(err, stepErr) {
		t.Fatalf("RunQuantum err = %v, want wrapped step error", err)
	}
	got, taskErr := f.Task("t-quantum")
	if taskErr != nil {
		t.Fatalf("Task: %v", taskErr)
	}
	if got.State != StateFailed {
		t.Fatalf("state = %s, want FAILED", got.State)
	}
	dc, err := DecodeCheckpoint(got.Checkpoint)
	if err != nil {
		t.Fatalf("DecodeCheckpoint: %v", err)
	}
	if dc.LastError != stepErr.Error() {
		t.Fatalf("LastError = %q, want %q", dc.LastError, stepErr.Error())
	}
}

// TestRunQuantumCancellationDoesNotStampCause pins the cancellation carve-out:
// context.Canceled releases the task (not a failure) and must leave no
// failure cause behind — scheduler shutdown is not a task defect.
func TestRunQuantumCancellationDoesNotStampCause(t *testing.T) {
	f := NewFabric()
	tk := newTask("t-cancel")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 0}
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t-cancel", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	err = f.RunQuantum("t-cancel", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunQuantum err = %v, want context.Canceled", err)
	}
	got, taskErr := f.Task("t-cancel")
	if taskErr != nil {
		t.Fatalf("Task: %v", taskErr)
	}
	if got.State != StateReady {
		t.Fatalf("state = %s, want READY release on cancellation", got.State)
	}
	dc, err := DecodeCheckpoint(got.Checkpoint)
	if err != nil {
		t.Fatalf("DecodeCheckpoint: %v", err)
	}
	if dc.LastError != "" {
		t.Fatalf("LastError = %q on a cancelled task, want empty", dc.LastError)
	}
}

// TestDecodeCheckpointMapRoundTripKeepsScope locks the persistence-facing
// decode contract: the map form (checkpoint_json after a restore) must
// recover BOTH tenant scope and failure cause, so a decode→EncodeCheckpoint
// re-wrap (Fail's cause stamp on a post-restore task) preserves them instead
// of silently dropping attribution.
func TestDecodeCheckpointMapRoundTripKeepsScope(t *testing.T) {
	env := NewCheckpointEnvelope(map[string]any{"input": "restored"})
	env.SessionID = "sess-restore-1"
	env.TenantID = "tenant-acme"
	env.LastError = "boom after restore"
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	dc, err := DecodeCheckpoint(asMap)
	if err != nil {
		t.Fatalf("DecodeCheckpoint(map): %v", err)
	}
	if dc.TenantID != "tenant-acme" {
		t.Fatalf("TenantID = %q, want tenant-acme", dc.TenantID)
	}
	if dc.LastError != "boom after restore" {
		t.Fatalf("LastError = %q, want boom after restore", dc.LastError)
	}
	if dc.SessionID != "sess-restore-1" {
		t.Fatalf("SessionID = %q, want sess-restore-1", dc.SessionID)
	}
	rewrapped := EncodeCheckpoint(dc)
	if rewrapped.TenantID != "tenant-acme" || rewrapped.LastError != "boom after restore" {
		t.Fatalf("re-wrap lost fields: tenant=%q lastError=%q", rewrapped.TenantID, rewrapped.LastError)
	}
}
