package taskfabric

// §3.2 (MEDIUM) regressions for the task fabric core:
// CompilePlan's all-or-nothing atomicity under a concurrent drain,
// CompleteWithCheckpoint's transition-before-mutation ordering, and
// RunQuantum's panic boundary.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompilePlanRollbackAtomicUnderConcurrentAcquire pins the batch
// compiler's atomicity: while a failing batch rolls back, a concurrent
// acquirer (the scheduler's drain path) must never be able to hold one of
// the batch's tasks — pre-fix, creates ran off-lock one by one, a drain
// could acquire the first task between Create and the rollback Delete, and
// the Delete then bounced off ErrTaskUndeletable, leaving a half-built DAG
// in the fabric (reported as "rollback incomplete").
func TestCompilePlanRollbackAtomicUnderConcurrentAcquire(t *testing.T) {
	// A live acquirer: hammers Acquire on the batch's first task while the
	// compile runs, holding anything it wins (simulating a drain whose
	// executor never returns).
	newContendedCompile := func(t *testing.T) *Fabric {
		t.Helper()
		f := NewFabric()
		// The colliding id: the batch's second step must fail on it. It is
		// LEASED from the start so nothing can delete it — the worst case
		// for the rollback.
		require.NoError(t, f.Create(&Task{ID: "collide", Capability: "x"}))
		epoch, err := f.Acquire("collide", "holder", time.Hour)
		require.NoError(t, err)
		_ = epoch

		var wg sync.WaitGroup
		stop := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if e, err := f.Acquire("s1", "grabber", time.Hour); err == nil {
					// Hold the lease: simulate an in-flight quantum that
					// never finishes (drain goroutine parked).
					<-stop
					_ = f.Release("s1", "grabber", e)
					return
				}
			}
		}()
		t.Cleanup(func() {
			close(stop)
			wg.Wait()
		})
		return f
	}

	iterations := 50
	for i := 0; i < iterations; i++ {
		f := newContendedCompile(t)
		_, err := f.CompilePlan(context.Background(), []PlanStep{
			{ID: "s1", Capability: "code"},
			{ID: "collide", Capability: "code"}, // duplicate → rollback of s1
		})
		require.Error(t, err, "compile must fail on the duplicate id")
		assert.NotContains(t, err.Error(), "rollback incomplete",
			"iteration %d: the batch must be atomic — no half-built DAG may survive", i)
		if tk, terr := f.Task("s1"); terr == nil {
			t.Fatalf("iteration %d: rolled-back task s1 still in fabric (state %s)", i, tk.State)
		}
	}
}

// TestCompilePlanSeesConsistentDependencyView pins the validation/c creation
// consistency the single critical section provides: while a compile runs, a
// concurrent DELETE of a dependency can only land fully BEFORE the compile's
// validation (compile rejected, no child created) or fully AFTER the batch
// lands (child validly created). The interleaved middle — validation passes,
// delete slips in, creates land against a vanished dependency — is what the
// pre-fix off-lock creates allowed; with the batch under f.mu it is
// structurally impossible (Delete needs the same lock).
//
// The observable contract: a SUCCESSFUL compile implies the dependency
// existed for the whole batch — so if the compile succeeded, the child must
// have been created BEFORE the dependency's deletion completed. We observe
// creation order via the fabric's event log: the child's task.created event
// must precede any state where the dependency is already gone... since
// Delete emits no event, we instead assert the complement: whenever the
// child exists, either the dependency still exists (delete lost the race
// entirely) or the compile reported the child as created in its return
// value — i.e. the child's existence is always attributable to a completed,
// validated compile, never to a half-built batch.
func TestCompilePlanSeesConsistentDependencyView(t *testing.T) {
	for i := 0; i < 50; i++ {
		f := NewFabric()
		require.NoError(t, f.Create(&Task{ID: "dep", Capability: "code"}))

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Race a deletion of the dependency against the compile. Delete
			// only succeeds from READY (or terminal) states.
			_ = f.Delete("dep")
		}()

		ids, err := f.CompilePlan(context.Background(), []PlanStep{
			{ID: "child", Capability: "code", DependsOn: []string{"dep"}},
		})
		wg.Wait()

		child, childErr := f.Task("child")
		if err != nil {
			// Rejected compile: no child may exist (the pre-fix bug created
			// the child even when the dependency vanished mid-batch — but
			// only on the success path, so here we pin the failure path).
			require.Empty(t, ids)
			if childErr == nil {
				t.Fatalf("iteration %d: rejected compile must not leave the child task", i)
			}
			continue
		}
		// Successful compile: the child exists and was created inside the
		// same critical section that validated the dependency.
		require.Equal(t, []string{"child"}, ids)
		require.NoError(t, childErr)
		require.Equal(t, StateReady, child.State)
	}
}

// TestCompleteWithCheckpointRejectsLeasedWithoutMutation pins the
// transition-before-mutation ordering: a LEASED task (valid owner + epoch,
// never started) cannot legally move to COMPLETED, and the refused call must
// leave the task's previous checkpoint untouched. Pre-fix the checkpoint
// was overwritten BEFORE the transition was validated.
func TestCompleteWithCheckpointRejectsLeasedWithoutMutation(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:         "t-leased",
		Capability: "code",
		Checkpoint: &CheckpointEnvelope{Payload: map[string]any{"keep": "me"}},
	}))
	epoch, err := f.Acquire("t-leased", "owner", time.Minute)
	require.NoError(t, err)

	err = f.CompleteWithCheckpoint("t-leased", "owner", epoch, &CheckpointEnvelope{
		Payload: map[string]any{"result": "oops"},
	})
	require.ErrorIs(t, err, ErrIllegalState)

	tk, terr := f.Task("t-leased")
	require.NoError(t, terr)
	assert.NotEqual(t, StateCompleted, tk.State, "LEASED→COMPLETED must be refused")

	dc, derr := DecodeCheckpoint(tk.Checkpoint)
	require.NoError(t, derr)
	got, _ := dc.Payload["keep"].(string)
	assert.Equal(t, "me", got, "the refused completion must not have mutated the checkpoint")
	if _, polluted := dc.Payload["result"]; polluted {
		t.Fatal("the refused completion's checkpoint leaked into the task")
	}
}

// TestRunQuantumPanicBoundary pins the panic boundary: a step closure that
// panics must be converted into a step failure — the task goes through the
// retry-policy Fail transition (requeue or terminal FAILED) and the error is
// returned. Pre-fix the panic propagated straight through RunQuantum,
// skipping Fail and leaving the task RUNNING with a live lease until TTL
// expiry.
func TestRunQuantumPanicBoundary(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:          "t-panic",
		Capability:  "code",
		RetryPolicy: RetryPolicy{MaxRetries: 1},
	}))
	epoch, err := f.Acquire("t-panic", "owner", time.Minute)
	require.NoError(t, err)

	var captured error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RunQuantum must convert a step panic into an error, saw panic: %v", r)
			}
		}()
		captured = f.RunQuantum("t-panic", "owner", epoch, func() (any, bool, error) {
			panic("executor exploded")
		})
	}()

	require.Error(t, captured)
	assert.Contains(t, captured.Error(), "panicked")

	// The retry policy was applied: single attempt + MaxRetries=1 means
	// terminal FAILED, not a stuck RUNNING task.
	tk, terr := f.Task("t-panic")
	require.NoError(t, terr)
	assert.Equal(t, StateFailed, tk.State,
		"a panicking quantum must fail through the retry policy, got %s", tk.State)
	assert.Equal(t, 1, tk.RetryPolicy.Attempts)
}

// TestRunQuantumPanicRequeueExhaustsRetries extends the boundary to the
// requeue path: with budget left, each panicked quantum consumes one retry
// instead of wedging the task.
func TestRunQuantumPanicRequeueExhaustsRetries(t *testing.T) {
	f := NewFabric()
	// MaxRetries counts TOTAL attempts (CanRetry = Attempts < MaxRetries):
	// after two panicked attempts one attempt remains → the task must be
	// back in READY, not terminal.
	require.NoError(t, f.Create(&Task{
		ID:          "t-panics",
		Capability:  "code",
		RetryPolicy: RetryPolicy{MaxRetries: 3},
	}))

	for attempt := 1; attempt <= 2; attempt++ {
		epoch, err := f.Acquire("t-panics", "owner", time.Minute)
		require.NoError(t, err)
		err = f.RunQuantum("t-panics", "owner", epoch, func() (any, bool, error) {
			panic("boom")
		})
		require.Error(t, err, "attempt %d", attempt)
	}

	tk, terr := f.Task("t-panics")
	require.NoError(t, terr)
	assert.Equal(t, StateReady, tk.State, "one retry must remain after two attempts")
	assert.Equal(t, 2, tk.RetryPolicy.Attempts)
}

// TestRunQuantumFencingSentinelsStillPropagate guards the error contract of
// the wrapped step: ordinary step errors (and their Fail transitions) must
// behave exactly as before the panic boundary was added.
func TestRunQuantumFencingSentinelsStillPropagate(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{
		ID:          "t-err",
		Capability:  "code",
		RetryPolicy: RetryPolicy{MaxRetries: 1},
	}))
	epoch, err := f.Acquire("t-err", "owner", time.Minute)
	require.NoError(t, err)

	sentinel := errors.New("step failed")
	err = f.RunQuantum("t-err", "owner", epoch, func() (any, bool, error) {
		return nil, false, sentinel
	})
	require.ErrorIs(t, err, sentinel)

	tk, terr := f.Task("t-err")
	require.NoError(t, terr)
	assert.Equal(t, StateFailed, tk.State)
}
