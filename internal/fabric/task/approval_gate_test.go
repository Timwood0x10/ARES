package taskfabric

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// --- M1: approval gate core tests ---

// TestApprovalGate_PendingNotSchedulable verifies that a task with
// RequiresApproval=true and ApprovalDecision="" does NOT appear in
// ReadyTasks or ResumableTasks.
func TestApprovalGate_PendingNotSchedulable(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
		ApprovalMessage:  "needs human review",
	}))

	if ids := f.ReadyTasks(); contains(ids, "gated") {
		t.Fatal("pending approval task must not appear in ReadyTasks")
	}
	if ids := f.ResumableTasks(); contains(ids, "gated") {
		t.Fatal("pending approval task must not appear in ResumableTasks")
	}
}

// TestApprovalGate_AcquireRefusesPending verifies that Acquire on a pending
// approval task returns ErrTaskAwaitingApproval.
func TestApprovalGate_AcquireRefusesPending(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))

	_, err := f.Acquire("gated", "agent-a", time.Minute)
	if !errors.Is(err, ErrTaskAwaitingApproval) {
		t.Fatalf("Acquire must return ErrTaskAwaitingApproval, got %v", err)
	}
}

// TestApprovalGate_ApproveThenSchedulable verifies that after
// ResolveApproval("approved") the task appears in ReadyTasks and can be
// acquired.
func TestApprovalGate_ApproveThenSchedulable(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))

	require.NoError(t, f.ResolveApproval("gated", "approved", "ok", "reviewer"))

	if ids := f.ReadyTasks(); !contains(ids, "gated") {
		t.Fatal("approved task must appear in ReadyTasks")
	}
	if ids := f.ResumableTasks(); !contains(ids, "gated") {
		t.Fatal("approved task must appear in ResumableTasks")
	}

	epoch, err := f.Acquire("gated", "agent-a", time.Minute)
	require.NoError(t, err)
	assert.Greater(t, epoch, uint64(0))
}

// TestApprovalGate_RejectFailsAndCascades verifies that reject drives the
// task to FAILED and cascades to strict dependents.
func TestApprovalGate_RejectFailsAndCascades(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))
	require.NoError(t, f.Create(&Task{
		ID:           "downstream",
		Capability:   "rust",
		Dependencies: []string{"gated"},
	}))

	require.NoError(t, f.ResolveApproval("gated", "rejected", "too risky", "reviewer"))

	gated, _ := f.Task("gated")
	if gated.State != StateFailed {
		t.Fatalf("rejected task must be FAILED, got %s", gated.State)
	}

	ds, _ := f.Task("downstream")
	if ds.State != StateFailed {
		t.Fatalf("strict downstream must cascade to FAILED, got %s", ds.State)
	}
	if ds.FailedDependency != "gated" {
		t.Fatalf("downstream FailedDependency must be 'gated', got %q", ds.FailedDependency)
	}
}

// TestApprovalGate_RejectAllowPartialDownstreamNotCascaded verifies that an
// AllowPartial dependent is NOT cascaded when the gated task is rejected.
func TestApprovalGate_RejectAllowPartialDownstreamNotCascaded(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))
	require.NoError(t, f.Create(&Task{
		ID:           "degraded",
		Capability:   "rust",
		Dependencies: []string{"gated"},
		AllowPartial: true,
	}))

	require.NoError(t, f.ResolveApproval("gated", "rejected", "no", "reviewer"))

	d, _ := f.Task("degraded")
	if d.State == StateFailed {
		t.Fatal("AllowPartial downstream must NOT cascade to FAILED")
	}
	assert.Contains(t, d.DegradedInputs, "gated")
}

// TestApprovalGate_Idempotent verifies that resolving with the same decision
// is a no-op, and resolving with a different decision returns
// ErrApprovalAlreadyResolved.
func TestApprovalGate_Idempotent(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))

	// Same decision twice → no-op.
	require.NoError(t, f.ResolveApproval("gated", "approved", "", "r1"))
	require.NoError(t, f.ResolveApproval("gated", "approved", "", "r1"))

	// Different decision → ErrApprovalAlreadyResolved.
	err := f.ResolveApproval("gated", "rejected", "", "r2")
	if !errors.Is(err, ErrApprovalAlreadyResolved) {
		t.Fatalf("opposite decision must return ErrApprovalAlreadyResolved, got %v", err)
	}
}

// TestApprovalGate_NotRequiredReturnsError verifies that ResolveApproval on a
// task without RequiresApproval returns ErrApprovalNotRequired.
func TestApprovalGate_NotRequiredReturnsError(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "plain", Capability: "rust"}))

	err := f.ResolveApproval("plain", "approved", "", "r1")
	if !errors.Is(err, ErrApprovalNotRequired) {
		t.Fatalf("expected ErrApprovalNotRequired, got %v", err)
	}
}

// TestApprovalGate_InvalidDecision verifies that an invalid decision string
// returns ErrInvalidApprovalDecision.
func TestApprovalGate_InvalidDecision(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))

	err := f.ResolveApproval("gated", "maybe", "", "r1")
	if !errors.Is(err, ErrInvalidApprovalDecision) {
		t.Fatalf("expected ErrInvalidApprovalDecision, got %v", err)
	}
}

// TestApprovalGate_NotFound verifies that resolving an unknown task returns
// ErrTaskNotFound.
func TestApprovalGate_NotFound(t *testing.T) {
	f := NewFabric()
	err := f.ResolveApproval("nonexistent", "approved", "", "r1")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

// TestApprovalGate_DefaultPathUnaffected verifies that a task without
// RequiresApproval behaves exactly as before: schedulable and acquirable.
func TestApprovalGate_DefaultPathUnaffected(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "plain", Capability: "rust"}))

	if ids := f.ReadyTasks(); !contains(ids, "plain") {
		t.Fatal("plain task must appear in ReadyTasks")
	}
	epoch, err := f.Acquire("plain", "agent-a", time.Minute)
	require.NoError(t, err)
	assert.Greater(t, epoch, uint64(0))
}

// TestApprovalGate_PendingApprovalsQuery verifies PendingApprovals returns
// only pending tasks.
func TestApprovalGate_PendingApprovalsQuery(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:               "p1",
		Capability:       "rust",
		RequiresApproval: true,
		ApprovalMessage:  "review p1",
	}))
	require.NoError(t, f.Create(&Task{
		ID:               "p2",
		Capability:       "rust",
		RequiresApproval: true,
		ApprovalMessage:  "review p2",
	}))
	require.NoError(t, f.Create(&Task{
		ID:               "p3",
		Capability:       "rust",
		RequiresApproval: true,
	}))
	// Approve p2 → it should no longer be pending.
	require.NoError(t, f.ResolveApproval("p2", "approved", "", "r"))

	pending := f.PendingApprovals()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
	// Must be sorted by TaskID.
	assert.Equal(t, "p1", pending[0].TaskID)
	assert.Equal(t, "p3", pending[1].TaskID)
	assert.Equal(t, "review p1", pending[0].Message)
}

// --- M1: ProjectStep mapping tests ---

// TestProjectStep_MapsInterrupt is in the planprojection package — covered
// by a separate test there. Here we test the PlanStep → Task path.

// TestCompilePlan_ApprovalFieldsPropagated verifies that CompilePlan
// propagates RequiresApproval/ApprovalMessage/ApprovalPayload from PlanStep
// to the created Task.
func TestCompilePlan_ApprovalFieldsPropagated(t *testing.T) {
	f := NewFabric()
	payload := map[string]any{"risk": "high"}
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{
			ID:               "step1",
			Capability:       "rust",
			RequiresApproval: true,
			ApprovalMessage:  "approve before deploy",
			ApprovalPayload:  payload,
		},
	})
	require.NoError(t, err)

	tk, err := f.Task("step1")
	require.NoError(t, err)
	assert.True(t, tk.RequiresApproval)
	assert.Equal(t, "approve before deploy", tk.ApprovalMessage)
	assert.Equal(t, payload, tk.ApprovalPayload)

	// Must be blocked by the gate.
	if ids := f.ReadyTasks(); contains(ids, "step1") {
		t.Fatal("approval-required task must not appear in ReadyTasks")
	}
}

// --- M2: persistence tests ---

// TestRestore_PendingStaysBlocked verifies that after an event-store replay
// a pending approval task is still blocked (not schedulable).
func TestRestore_PendingStaysBlocked(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
		ApprovalMessage:  "review needed",
	}))

	// Restore into a fresh fabric.
	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))

	tk, err := f2.Task("gated")
	require.NoError(t, err)
	assert.True(t, tk.RequiresApproval)
	assert.Equal(t, "", tk.ApprovalDecision, "pending decision must be empty after restore")

	// Must still be blocked.
	if ids := f2.ReadyTasks(); contains(ids, "gated") {
		t.Fatal("restored pending approval task must not appear in ReadyTasks")
	}
}

// TestRestore_ApprovedStaysCleared verifies that an approved task remains
// schedulable after restore.
func TestRestore_ApprovedStaysCleared(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))
	require.NoError(t, f.ResolveApproval("gated", "approved", "ok", "reviewer"))

	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))

	tk, err := f2.Task("gated")
	require.NoError(t, err)
	assert.True(t, tk.RequiresApproval)
	assert.Equal(t, "approved", tk.ApprovalDecision)

	// Must be schedulable.
	if ids := f2.ReadyTasks(); !contains(ids, "gated") {
		t.Fatal("restored approved task must appear in ReadyTasks")
	}
}

// TestRestore_RejectedStaysFailed verifies that a rejected task remains
// FAILED after restore.
func TestRestore_RejectedStaysFailed(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{
		ID:               "gated",
		Capability:       "rust",
		RequiresApproval: true,
	}))
	require.NoError(t, f.ResolveApproval("gated", "rejected", "no", "reviewer"))

	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))

	tk, err := f2.Task("gated")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, tk.State)
	assert.Equal(t, "rejected", tk.ApprovalDecision)
	assert.Equal(t, "no", tk.ApprovalFeedback)
}

// --- helpers ---

// contains reports whether ids contains v.
func contains(ids []string, v string) bool {
	for _, id := range ids {
		if id == v {
			return true
		}
	}
	return false
}
