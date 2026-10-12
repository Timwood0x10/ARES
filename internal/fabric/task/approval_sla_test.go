package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// --- approval SLA (ExpireApprovals) ---

// newClockedFabric returns a fabric whose clock is driven by *clk so SLA
// tests are deterministic without time.Sleep (code_rules_v2 §7.3).
func newClockedFabric(clk *time.Time) *Fabric {
	return NewFabric().WithClock(func() time.Time { return *clk })
}

// TestExpireApprovals_AutoRejectsPastSLA verifies that a pending gate whose
// ApprovalTimeout has passed is driven to FAILED by ExpireApprovals, stamped
// as a system auto-reject, while a gate with no SLA (zero timeout) and a gate
// still within its SLA are left untouched.
func TestExpireApprovals_AutoRejectsPastSLA(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk)
	require.NoError(t, f.Create(&Task{
		ID: "expired", Capability: "rust",
		RequiresApproval: true, ApprovalTimeout: clk.Add(1 * time.Minute),
	}))
	require.NoError(t, f.Create(&Task{
		ID: "within", Capability: "rust",
		RequiresApproval: true, ApprovalTimeout: clk.Add(10 * time.Minute),
	}))
	require.NoError(t, f.Create(&Task{
		ID: "no-sla", Capability: "rust",
		RequiresApproval: true, // zero ApprovalTimeout → never expires
	}))

	clk = clk.Add(2 * time.Minute) // past "expired", before "within"
	got := f.ExpireApprovals()
	assert.Equal(t, []string{"expired"}, got)

	ex, err := f.Task("expired")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, ex.State)
	assert.Equal(t, ApprovalDecisionRejected, ex.ApprovalDecision)
	assert.Equal(t, approvalSLAPrincipal, ex.ApprovalBy)

	// A gate still within SLA and a gate with no SLA stay pending.
	within, err := f.Task("within")
	require.NoError(t, err)
	assert.Equal(t, "", within.ApprovalDecision)
	noSLA, err := f.Task("no-sla")
	require.NoError(t, err)
	assert.Equal(t, "", noSLA.ApprovalDecision)
}

// TestExpireApprovals_CascadesLikeManualReject verifies that an SLA
// auto-reject cascades to downstream dependents with the same semantics as a
// manual reject: strict dependents fail, AllowPartial dependents survive.
func TestExpireApprovals_CascadesLikeManualReject(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk)
	require.NoError(t, f.Create(&Task{
		ID: "root", Capability: "rust",
		RequiresApproval: true, ApprovalTimeout: clk.Add(1 * time.Minute),
	}))
	require.NoError(t, f.Create(&Task{ID: "strict", Capability: "rust", Dependencies: []string{"root"}}))
	require.NoError(t, f.Create(&Task{ID: "partial", Capability: "rust", Dependencies: []string{"root"}, AllowPartial: true}))

	clk = clk.Add(2 * time.Minute)
	require.Equal(t, []string{"root"}, f.ExpireApprovals())

	strict, err := f.Task("strict")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, strict.State, "strict dependent must cascade to FAILED")

	partial, err := f.Task("partial")
	require.NoError(t, err)
	assert.Equal(t, StateReady, partial.State, "AllowPartial dependent must survive")
	assert.Contains(t, partial.DegradedInputs, "root")
}

// TestExpireApprovals_SurvivesRestore verifies that the ApprovalTimeout rides
// the event log so a restart does not lose the SLA: a gate that would expire
// is still expirable after a store replay.
func TestExpireApprovals_SurvivesRestore(t *testing.T) {
	clk := time.Now()
	store := ares_events.NewMemoryEventStore()
	f := newClockedFabric(&clk).WithEventStore(store)
	require.NoError(t, f.Create(&Task{
		ID: "gated", Capability: "rust",
		RequiresApproval: true, ApprovalTimeout: clk.Add(1 * time.Minute),
	}))

	f2 := newClockedFabric(&clk).WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))
	restored, err := f2.Task("gated")
	require.NoError(t, err)
	require.False(t, restored.ApprovalTimeout.IsZero(), "ApprovalTimeout must survive restore")

	clk = clk.Add(2 * time.Minute)
	assert.Equal(t, []string{"gated"}, f2.ExpireApprovals())
}

// TestCompilePlan_ApprovalTimeoutResolvesToAbsolute verifies CompilePlan
// converts a relative PlanStep.ApprovalTimeout into an absolute
// Task.ApprovalTimeout anchored on the fabric clock.
func TestCompilePlan_ApprovalTimeoutResolvesToAbsolute(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk)
	_, err := f.CompilePlan(context.Background(), []PlanStep{{
		ID: "step1", Capability: "rust",
		RequiresApproval: true, ApprovalTimeout: 5 * time.Minute,
	}})
	require.NoError(t, err)
	tk, err := f.Task("step1")
	require.NoError(t, err)
	assert.WithinDuration(t, clk.Add(5*time.Minute), tk.ApprovalTimeout, time.Second)
}

// --- blast radius ---

// buildBlastGraph creates root (gated) → strict → strictLeaf and
// root → partial (AllowPartial). Returns the fabric.
func buildBlastGraph(t *testing.T) *Fabric {
	t.Helper()
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "root", Capability: "rust", RequiresApproval: true}))
	require.NoError(t, f.Create(&Task{ID: "strict", Capability: "rust", Dependencies: []string{"root"}}))
	require.NoError(t, f.Create(&Task{ID: "strictLeaf", Capability: "rust", Dependencies: []string{"strict"}}))
	require.NoError(t, f.Create(&Task{ID: "partial", Capability: "rust", Dependencies: []string{"root"}, AllowPartial: true}))
	return f
}

// TestApprovalBlastRadius_CountsStrictAndPartial verifies the preview counts
// transitive strict dependents as cascaded and AllowPartial dependents as
// degraded (traversal stops at an AllowPartial survivor).
func TestApprovalBlastRadius_CountsStrictAndPartial(t *testing.T) {
	f := buildBlastGraph(t)
	br := f.ApprovalBlastRadius("root")
	assert.Equal(t, 2, br.CascadedFail, "strict + strictLeaf cascade")
	assert.Equal(t, 1, br.AllowPartialDegraded, "partial survives")
}

// TestResolveApprovalWithBlastRadius_MatchesActualCascade verifies the blast
// radius returned by the atomic resolve equals the dependents actually
// cascaded (no TOCTOU drift), and that approve returns a zero blast radius.
func TestResolveApprovalWithBlastRadius_MatchesActualCascade(t *testing.T) {
	f := buildBlastGraph(t)
	br, err := f.ResolveApprovalWithBlastRadius("root", ApprovalDecisionRejected, "too risky", "r1")
	require.NoError(t, err)
	assert.Equal(t, 2, br.CascadedFail)
	assert.Equal(t, 1, br.AllowPartialDegraded)

	// Reported counts must match the actual post-reject states.
	cascaded := 0
	for _, id := range []string{"strict", "strictLeaf"} {
		tk, err := f.Task(id)
		require.NoError(t, err)
		if tk.State == StateFailed {
			cascaded++
		}
	}
	assert.Equal(t, br.CascadedFail, cascaded)
	partial, err := f.Task("partial")
	require.NoError(t, err)
	assert.Equal(t, StateReady, partial.State)
}

// TestResolveApprovalWithBlastRadius_ApproveZeroBlast verifies approve yields
// a zero blast radius and keeps the task schedulable, and that an idempotent
// repeat is a no-op with a zero blast radius.
func TestResolveApprovalWithBlastRadius_ApproveZeroBlast(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))

	br, err := f.ResolveApprovalWithBlastRadius("gated", ApprovalDecisionApproved, "ok", "r1")
	require.NoError(t, err)
	assert.Equal(t, BlastRadius{}, br)
	assert.True(t, contains(f.ReadyTasks(), "gated"))

	// Idempotent repeat → no-op, zero blast.
	br2, err := f.ResolveApprovalWithBlastRadius("gated", ApprovalDecisionApproved, "ok", "r1")
	require.NoError(t, err)
	assert.Equal(t, BlastRadius{}, br2)
}

// --- approve-to-execution wake ---

// TestResolveApproval_ApproveEmitsTaskReady verifies that approving a gate
// records a task.ready event so a scheduler subscribed to lifecycle events
// drains the newly-schedulable task without waiting for a sweep. A gated task
// emits no task.ready before approval.
func TestResolveApproval_ApproveEmitsTaskReady(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))

	assert.Equal(t, 0, countEvents(t, store, ares_events.EventTaskReady),
		"a gated task must not be announced ready before approval")

	require.NoError(t, f.ResolveApproval("gated", ApprovalDecisionApproved, "", "r1"))
	assert.Equal(t, 1, countEvents(t, store, ares_events.EventTaskReady),
		"approve must emit exactly one task.ready wake event")
}

// countEvents reads the whole store and counts events of the given type.
func countEvents(t *testing.T, store *ares_events.MemoryEventStore, typ ares_events.EventType) int {
	t.Helper()
	evs, err := store.ReadAll(context.Background(), ares_events.ReadOptions{})
	require.NoError(t, err)
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// --- default approval SLA (Create-path safety net) ---

// TestCreate_DefaultApprovalSLAApplied verifies that a RequiresApproval task
// created directly via Create (no per-task ApprovalTimeout) inherits the
// fabric default SLA and is therefore expirable — closing the gap where the
// Create path left the gate with no SLA at all.
func TestCreate_DefaultApprovalSLAApplied(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk).WithDefaultApprovalSLA(1 * time.Minute)
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))

	tk, err := f.Task("gated")
	require.NoError(t, err)
	assert.WithinDuration(t, clk.Add(1*time.Minute), tk.ApprovalTimeout, time.Second,
		"a gate created without its own SLA must inherit the fabric default")

	clk = clk.Add(2 * time.Minute)
	assert.Equal(t, []string{"gated"}, f.ExpireApprovals())
}

// TestCreate_PerTaskTimeoutWinsOverDefault verifies an explicit per-task
// ApprovalTimeout is never overridden by the fabric default.
func TestCreate_PerTaskTimeoutWinsOverDefault(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk).WithDefaultApprovalSLA(1 * time.Hour)
	explicit := clk.Add(5 * time.Minute)
	require.NoError(t, f.Create(&Task{
		ID: "gated", Capability: "rust",
		RequiresApproval: true, ApprovalTimeout: explicit,
	}))
	tk, err := f.Task("gated")
	require.NoError(t, err)
	assert.Equal(t, explicit, tk.ApprovalTimeout, "per-task SLA must win over the fabric default")
}

// TestCreate_NoDefaultLeavesInfinite verifies that with no fabric default and
// no per-task timeout the gate stays non-expirable (deliberate "wait forever").
func TestCreate_NoDefaultLeavesInfinite(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk) // no default SLA
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))

	tk, err := f.Task("gated")
	require.NoError(t, err)
	assert.True(t, tk.ApprovalTimeout.IsZero(), "no default + no per-task → no SLA")

	clk = clk.Add(1000 * time.Hour)
	assert.Empty(t, f.ExpireApprovals(), "a gate with no SLA must never auto-expire")
}

// TestCompilePlan_DefaultSLAFillsMissingTimeout verifies the default net also
// covers the CompilePlan path when a step omits its own ApprovalTimeout.
func TestCompilePlan_DefaultSLAFillsMissingTimeout(t *testing.T) {
	clk := time.Now()
	f := newClockedFabric(&clk).WithDefaultApprovalSLA(1 * time.Minute)
	_, err := f.CompilePlan(context.Background(), []PlanStep{{
		ID: "step1", Capability: "rust", RequiresApproval: true, // no ApprovalTimeout
	}})
	require.NoError(t, err)
	tk, err := f.Task("step1")
	require.NoError(t, err)
	assert.WithinDuration(t, clk.Add(1*time.Minute), tk.ApprovalTimeout, time.Second)
}

// --- preflight: invalid decision guard shared by both entries (P1.3) ---

// TestResolveApproval_InvalidDecisionRejectedByBothEntries verifies the
// decision validation lives in the shared preflight so BOTH ResolveApproval
// and ResolveApprovalWithBlastRadius reject an invalid decision identically
// and leave the task untouched (no drift between the two entries).
func TestResolveApproval_InvalidDecisionRejectedByBothEntries(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))

	assert.ErrorIs(t, f.ResolveApproval("gated", "banana", "", "r1"), ErrInvalidApprovalDecision)
	_, err := f.ResolveApprovalWithBlastRadius("gated", "banana", "", "r1")
	assert.ErrorIs(t, err, ErrInvalidApprovalDecision)

	tk, err := f.Task("gated")
	require.NoError(t, err)
	assert.Equal(t, "", tk.ApprovalDecision, "invalid decision must not be stored")
	assert.Equal(t, StateReady, tk.State, "invalid decision must not change state")
}

// --- reject audit durability on the authoritative task.failed event (P1.4) ---

// TestReject_AuditOnTaskFailedEvent verifies a reject stamps the approval
// decision/by BEFORE the terminal transition so the authoritative task.failed
// event (not only the interrupt.resolved event) carries who rejected and why.
func TestReject_AuditOnTaskFailedEvent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))
	require.NoError(t, f.ResolveApproval("gated", ApprovalDecisionRejected, "too risky", "reviewer-1"))

	failed := firstEvent(t, store, ares_events.EventTaskFailed)
	require.NotNil(t, failed, "reject must emit task.failed")
	assert.Equal(t, ApprovalDecisionRejected, failed.Payload[restoreKeyApprovalDecision],
		"task.failed must carry the approval decision (audit durability)")
	assert.Equal(t, "reviewer-1", failed.Payload[restoreKeyApprovalBy],
		"task.failed must carry who rejected")
	assert.Equal(t, "too risky", failed.Payload[restoreKeyApprovalFeedback])
}

// TestReject_AuditSurvivesRestore verifies the decision + approver survive a
// restart (ApprovalBy is now persisted, not memory-only).
func TestReject_AuditSurvivesRestore(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{ID: "gated", Capability: "rust", RequiresApproval: true}))
	require.NoError(t, f.ResolveApproval("gated", ApprovalDecisionRejected, "no", "reviewer-1"))

	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))
	tk, err := f2.Task("gated")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, tk.State)
	assert.Equal(t, ApprovalDecisionRejected, tk.ApprovalDecision)
	assert.Equal(t, "reviewer-1", tk.ApprovalBy, "approver must survive restart")
}

// firstEvent returns the first event of the given type in the store, or nil.
func firstEvent(t *testing.T, store *ares_events.MemoryEventStore, typ ares_events.EventType) *ares_events.Event {
	t.Helper()
	evs, err := store.ReadAll(context.Background(), ares_events.ReadOptions{})
	require.NoError(t, err)
	for _, ev := range evs {
		if ev.Type == typ {
			return ev
		}
	}
	return nil
}
