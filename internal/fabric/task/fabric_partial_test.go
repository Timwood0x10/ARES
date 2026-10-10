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

// partialFixture builds the plan §3.3 acceptance shape: t2 fails permanently,
// t3 must complete, t4 depends on both and may opt into AllowPartial, and t5
// sits downstream of t4 without any opt-in.
func partialFixture(t *testing.T, allowPartial bool) (*Fabric, *ares_events.MemoryEventStore) {
	t.Helper()
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	for _, tk := range []*Task{
		{ID: "t2", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}},
		{ID: "t3", Capability: "rust"},
		{ID: "t4", Capability: "rust", Dependencies: []string{"t2", "t3"}, AllowPartial: allowPartial},
		{ID: "t5", Capability: "rust", Dependencies: []string{"t4"}},
	} {
		require.NoError(t, f.Create(tk), "Create %s", tk.ID)
	}
	return f, store
}

// failPermanently drives a task to a terminal FAILED (no retry budget left).
func failPermanently(t *testing.T, f *Fabric, id, cause string) {
	t.Helper()
	epoch, err := f.Acquire(id, "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start(id, "agent-a", epoch))
	require.NoError(t, f.Fail(id, "agent-a", epoch, errors.New(cause)))
}

// runToCompletion drives a task through its normal successful lifecycle.
func runToCompletion(t *testing.T, f *Fabric, id string) {
	t.Helper()
	epoch, err := f.Acquire(id, "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start(id, "agent-a", epoch))
	require.NoError(t, f.Complete(id, "agent-a", epoch))
}

// TestAllowPartial_DownstreamRunsWithAQueryableGap is the M3 core contract
// (acceptances ①, ②, ④ and ⑤): the cascade spares an opted-in dependent, the
// gap is recorded and durable, the task still waits for its OTHER dependency,
// and once that lands it is genuinely schedulable and completes with the gap
// visible in its payload.
func TestAllowPartial_DownstreamRunsWithAQueryableGap(t *testing.T) {
	f, store := partialFixture(t, true)

	failPermanently(t, f, "t2", "root cause: tool unavailable")

	// The cascade spared the opted-in dependent: READY, not FAILED, with the gap
	// attributed to the predecessor that actually failed.
	t4, err := f.Task("t4")
	require.NoError(t, err)
	require.Equal(t, StateReady, t4.State, "an AllowPartial dependent must not be failed by the cascade")
	assert.Equal(t, []string{"t2"}, t4.DegradedInputs)
	assert.Empty(t, t4.FailedDependency, "a degraded task did not fail; it has a failed predecessor")
	t4cp, err := DecodeCheckpoint(t4.Checkpoint)
	require.NoError(t, err)
	assert.Empty(t, t4cp.LastError, "the reason is not copied: t2's own task.failed is the record")
	// The root itself is terminal, and carries its own cause.
	t2, err := f.Task("t2")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, t2.State)
	t2cp, err := DecodeCheckpoint(t2.Checkpoint)
	require.NoError(t, err)
	assert.Contains(t, t2cp.LastError, "tool unavailable")

	// Acceptance ②: AllowPartial is not a blanket "run anyway" — the second
	// dependency is still outstanding, so the task must not be handed out.
	assert.NotContains(t, f.ResumableTasks(), "t4", "a pending second dependency must still block")
	assert.NotContains(t, f.ReadyTasks(), "t4")

	// The strict task downstream of t4 is untouched: t4 has not failed.
	t5, err := f.Task("t5")
	require.NoError(t, err)
	assert.Equal(t, StateReady, t5.State)

	// Acceptance ①: once the remaining dependency completes, the degraded task
	// enters the schedulable set — and Acquire agrees, so it can really run.
	runToCompletion(t, f, "t3")
	assert.Contains(t, f.ResumableTasks(), "t4", "a degraded task must be schedulable")
	assert.Contains(t, f.ReadyTasks(), "t4")
	assert.True(t, mustIsReady(t, f, "t4"))

	// Acceptance ④: the gap reaches the task's payload, so a consumer of the
	// output can tell "not checked" from "checked, nothing found".
	runToCompletion(t, f, "t4")
	done, err := f.Task("t4")
	require.NoError(t, err)
	dc, err := DecodeCheckpoint(done.Checkpoint)
	require.NoError(t, err)
	assert.Equal(t, []string{"t2"}, dc.Payload[payloadKeyDegradedInputs])

	// The event stream says it too: the ready event that unblocked t4 carries
	// the gap, which is what makes the degradation visible without reading state.
	assert.Equal(t, []string{"t2"}, readyPayload(t, store, "t4")[restoreKeyDegradedInputs])

	// Acceptance ⑤: degradation does not poison the downstream graph — t5 runs
	// once its own dependency (t4) completes.
	assert.Contains(t, f.ResumableTasks(), "t5", "the successor must become runnable")
}

// TestAllowPartial_StrictDependentsStillCascade is acceptance ③: the opt-in is
// what changes the outcome, not a global relaxation.
func TestAllowPartial_StrictDependentsStillCascade(t *testing.T) {
	f, _ := partialFixture(t, false)

	failPermanently(t, f, "t2", "root cause")

	t4, err := f.Task("t4")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, t4.State, "without the opt-in a failed dependency still kills the dependent")
	assert.Equal(t, "t2", t4.FailedDependency)
	assert.Empty(t, t4.DegradedInputs)

	t5, err := f.Task("t5")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, t5.State, "and the failure keeps propagating downstream")
	assert.Equal(t, "t4", t5.FailedDependency)
}

// TestAllowPartial_EveryFailedPredecessorIsRecorded pins the "one gap at a time"
// behaviour: with two failing predecessors the task stays blocked until BOTH are
// recorded, then runs — so a partial input is never silently incomplete.
func TestAllowPartial_EveryFailedPredecessorIsRecorded(t *testing.T) {
	f := NewFabric()
	for _, tk := range []*Task{
		{ID: "t2", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}},
		{ID: "t6", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}},
		{ID: "t4", Capability: "rust", Dependencies: []string{"t2", "t6"}, AllowPartial: true},
	} {
		require.NoError(t, f.Create(tk), "Create %s", tk.ID)
	}

	failPermanently(t, f, "t2", "first root cause")
	assert.NotContains(t, f.ResumableTasks(), "t4", "one recorded gap is not enough while t6 is unresolved")

	failPermanently(t, f, "t6", "second root cause")
	got, err := f.Task("t4")
	require.NoError(t, err)
	assert.Equal(t, []string{"t2", "t6"}, got.DegradedInputs)
	assert.Contains(t, f.ResumableTasks(), "t4", "with every predecessor accounted for, the task runs")
}

// TestRestore_PreservesPartialPolicyAndRecordedGaps is the memory-store half of
// acceptance ④: a restart must not re-strand a task the cascade already
// unblocked, so both the policy and the recorded gaps have to survive.
func TestRestore_PreservesPartialPolicyAndRecordedGaps(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	for _, tk := range []*Task{
		{ID: "t2", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}},
		{ID: "t3", Capability: "rust"},
		{ID: "t4", Capability: "rust", Dependencies: []string{"t2", "t3"}, AllowPartial: true},
	} {
		require.NoError(t, f.Create(tk), "Create %s", tk.ID)
	}

	failPermanently(t, f, "t2", "root cause")
	runToCompletion(t, f, "t3")
	require.Contains(t, f.ResumableTasks(), "t4", "fixture sanity: schedulable before the restart")

	rebuilt := NewFabric().WithEventStore(store)
	require.NoError(t, rebuilt.RestoreFromStore(context.Background()))

	got, err := rebuilt.Task("t4")
	require.NoError(t, err)
	assert.True(t, got.AllowPartial, "the policy must survive, or the rebuilt fabric re-fails the task")
	assert.Equal(t, []string{"t2"}, got.DegradedInputs, "the recorded gap must survive with it")
	assert.Equal(t, StateReady, got.State)
	assert.Contains(t, rebuilt.ResumableTasks(), "t4", "the rebuilt fabric must still schedule the degraded task")
}

// TestAcquire_IsNotTheDependencyGate documents the contract that was checked
// while implementing M3, so nobody "fixes" it twice: Acquire is the CAS ownership
// claim, and the dependency judgment lives in the scheduler-facing queries.
// Adding a dependency guard to Acquire was implemented, run against the suite,
// and reverted — four harnesses (agent/planner_cognition_test.go,
// planprojection/incremental_compile_test.go, fabric_lease_snapshot_test.go,
// restore_test.go) drive a specific node directly, which the contract allows.
// What Acquire must never do is contradict the query gates about the states it
// does own (deadline, backoff): those are asserted in the M1/M2 tests.
func TestAcquire_IsNotTheDependencyGate(t *testing.T) {
	f, _ := partialFixture(t, true)

	failPermanently(t, f, "t2", "root cause")

	// The scheduler-facing views are the admission control: t4 is still blocked
	// while its second input is outstanding...
	assert.NotContains(t, f.ResumableTasks(), "t4")
	assert.NotContains(t, f.ReadyTasks(), "t4")
	assert.False(t, mustIsReady(t, f, "t4"))

	// ...but Acquire itself grants the lease, by design: callers that already
	// decided what to run (recovery, tests) can drive a specific node.
	epoch, err := f.Acquire("t4", "agent-x", time.Minute)
	require.NoError(t, err, "Acquire stays the ownership claim, not the dependency gate")
	assert.Positive(t, epoch)
}

// TestCompilePlan_PropagatesPartialPolicyAndBackoff pins the config surface
// (plan §3.2 ⑤ and §3.3 ③): a PlanStep's policy reaches the compiled Task, and a
// step that sets nothing keeps the strict, no-backoff defaults.
func TestCompilePlan_PropagatesPartialPolicyAndBackoff(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "root", Capability: "code"},
		{
			ID: "partial", Capability: "code", DependsOn: []string{"root"},
			AllowPartial: true, BackoffBase: 2 * time.Second, BackoffMax: 8 * time.Second,
		},
		{ID: "strict", Capability: "code", DependsOn: []string{"root"}},
	})
	require.NoError(t, err)

	partial, err := f.Task("partial")
	require.NoError(t, err)
	assert.True(t, partial.AllowPartial)
	assert.Equal(t, 2*time.Second, partial.BackoffBase)
	assert.Equal(t, 8*time.Second, partial.BackoffMax)

	strict, err := f.Task("strict")
	require.NoError(t, err)
	assert.False(t, strict.AllowPartial, "an unset step must stay strict")
	assert.Zero(t, strict.BackoffBase, "and must keep the immediate-requeue default")
	assert.Zero(t, strict.BackoffMax)
}

// TestDefaultPathPayloadHasNoNewKeys is the Definition-of-Done evidence that a
// task which uses none of the 0.3.3 capabilities persists exactly what 0.3.2
// persisted: every policy/runtime key added by M1–M3 stays out of the payload
// unless it actually carries information.
func TestDefaultPathPayloadHasNoNewKeys(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(&Task{ID: "plain", Capability: "rust"}))
	runToCompletion(t, f, "plain")

	events := readEvents(t, store, "plain")
	require.NotEmpty(t, events)
	newKeys := []string{
		restoreKeyNextAttemptAt, restoreKeyBackoffBaseMS, restoreKeyBackoffMaxMS,
		restoreKeyAllowPartial, restoreKeyDegradedInputs,
	}
	for _, ev := range events {
		for _, key := range newKeys {
			assert.NotContains(t, ev.Payload, key,
				"event %s must not carry %q on the default path", ev.Type, key)
		}
	}
}

// mustIsReady asserts the IsReady view agrees with the query gates, so a caller
// that admits work through IsReady cannot see a different answer.
func mustIsReady(t *testing.T, f *Fabric, id string) bool {
	t.Helper()
	ready, err := f.IsReady(id)
	require.NoError(t, err)
	return ready
}
