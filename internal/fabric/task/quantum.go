package taskfabric

import (
	"context"
	"errors"
	"fmt"
)

// QuantumStep is one Agent Step executed inside a quantum
// (ares-runtime.md): reasoning → tool call → observation. It returns the
// durable checkpoint (progress so far) and whether the task is complete.
type QuantumStep func() (checkpoint any, done bool, err error)

// RunQuantum executes a single execution quantum: start the leased task, run
// one agent step, then decide at the quantum boundary (yield is the execution
// boundary, not a state decision):
//
//	done  → COMPLETED (preserving the checkpoint's worker result)
//	err   → FAILED (or requeued to READY per retry policy); a cancellation
//	        (context.Canceled) is RELEASED instead — READY, unowned, retry
//	        budget untouched, previous checkpoint preserved
//	!done → SUSPENDED with the checkpoint preserved
//
// SUSPENDED semantics:
// the agent's execution quantum ended, but the task's durable intent is NOT
// yet complete — this is not "the agent was suspended". Task suspended ≠
// Agent suspended ≠ Execution yielded. Continue is the Scheduler's decision
// via re-acquire; the state machine never does RUNNING→RUNNING directly. The
// fencing token (epoch) is verified on every step so a stale holder cannot
// drive a task it no longer owns.
//
// Panic boundary: a panic inside the step is recovered HERE and converted
// into a step error so the fabric's own state machine stays consistent —
// the task is failed/requeued through the normal retry policy instead of
// being left RUNNING with a live lease until TTL expiry. Pre-fix the panic
// propagated straight through RunQuantum, skipping the Fail transition
// entirely; every caller had to install its own recover or crash.
//
// The done→COMPLETED path preserves the checkpoint (worker result) so the
// kernel dispatch can read it back: kernelTaskDispatcher.Dispatch subscribes
// to EventTaskCompleted, infers the task is done, then reads the completed
// task's Checkpoint for the worker's real items/reason (serve result-reflux).
//
// Args:
//   - taskID: the task id.
//   - agentID: the executing agent (must hold the lease).
//   - epoch: the fencing token returned by Acquire.
//   - step: the agent step to run inside this quantum.
//
// Returns:
//   - error: ErrNotOwner / ErrEpochMismatch / ErrIllegalState, the step's own
//     error (after the FAIL or Release transition was applied), or the
//     outcome of the quantum transition.
//
// Error propagation contract: when step returns an error, RunQuantum applies
// a fabric transition and then RETURNS the step error instead of swallowing
// it. Callers (kernelscheduler outcome attribution, dispatch logging) must
// observe failures as failures — a swallowed error made the scheduler record
// failed quanta as successes, inflating agent confidence and hiding every
// task failure from logs.
//
// Cancellation is not a failure: a step error that wraps context.Canceled is
// released (READY, unowned, retry budget untouched) rather than failed. The
// kernel cancels the drain context on scheduler shutdown, so every in-flight
// step returns context.Canceled — routing that through Fail burned
// RetryPolicy.Attempts and, for a zero-retry task (the Create default),
// finalized it FAILED and cascaded the whole downstream subgraph. The
// must-persist task.failed event then made the loss permanent across
// restart, even though nothing had actually failed. The kernel's own outcome
// attribution already exempted context.Canceled from confidence scoring
// (scheduler_quantum.go); this is the matching fabric-side half.
// QuantumResult reports one quantum along the dimensions that must never be conflated.
//
// A single error cannot carry this. "The step succeeded but the completion was refused" and "the
// step failed and the failure could not be recorded" both look like an error to a caller reading
// only that error, and it has to guess which — which is how a storage refusal gets charged to an
// agent whose execution succeeded.
type QuantumResult struct {
	// Executed is false when the step never ran: Start was rejected (ownership, fencing, missing
	// task) or the quantum was preempted at the boundary. A non-executing quantum is never the
	// executor's failure.
	Executed bool
	// Discarded reports that the step DID produce a result and the task did not take it, because
	// ownership moved while it was running (preempted, then re-acquired by a new holder or a new
	// epoch of the same one). That is neither a success nor a failure for this executor: the work was
	// fenced out. Scoring it rewards an agent for a result the task never used, or punishes it for a
	// step nobody was waiting for any more.
	Discarded bool
	// StepErr is the executor's own failure. Nil when the step produced a result, even if that
	// result could not be persisted.
	StepErr error
	// CommitErr is the storage outcome of the terminal transition. Orthogonal to StepErr: a step can
	// succeed and its completion be refused, and a failed step can fail to be recorded.
	CommitErr error
	// RetriesConsumed is the retry this quantum actually spent, recorded at the transition that
	// decided it — never derived from a cumulative counter or from the shape of an error chain.
	RetriesConsumed int
}

// Succeeded reports whether the executor ran and produced a result THE TASK TOOK.
//
// A discarded result is excluded by construction: it is neither a verdict on the executor nor on the
// work, and a caller that asks "did this succeed" about a fenced result would be asking the wrong
// question — but it would get a true answer if this ignored Discarded.
func (r QuantumResult) Succeeded() bool {
	return r.Executed && !r.Discarded && r.StepErr == nil
}

// classifyTerminal separates a runtime rejection from a storage outcome.
//
// A fencing error (ownership or epoch moved) means the result was discarded, not that the store
// failed: there was no store write to judge. Reporting it as CommitErr made the scheduler treat a
// late result from a stale holder as a real verdict.
func classifyTerminal(err error) (commitErr error, discarded bool) {
	switch {
	case err == nil:
		return nil, false
	case errors.Is(err, ErrNotOwner), errors.Is(err, ErrEpochMismatch):
		return nil, true
	default:
		return err, false
	}
}

// Err joins the dimensions, for logging and for callers that predate the split.
//
// A single set dimension is returned AS ITSELF rather than wrapped: callers compare sentinels with
// errors.Is and, in a few places, by identity, and wrapping one error in a join would change the
// value they see for a case that is not ambiguous at all.
func (r QuantumResult) Err() error {
	switch {
	case r.StepErr == nil:
		return r.CommitErr
	case r.CommitErr == nil:
		return r.StepErr
	default:
		return errors.Join(r.StepErr, r.CommitErr)
	}
}

// RunQuantumResult runs one quantum and reports each dimension separately.
func (f *Fabric) RunQuantumResult(taskID, agentID string, epoch uint64, step QuantumStep) QuantumResult {
	if err := f.Start(taskID, agentID, epoch); err != nil {
		// Never executed: fencing, ownership loss or a missing task. The scheduler reports these
		// neutrally, because they are not the executor's verdict on the work.
		return QuantumResult{Executed: false, StepErr: err}
	}

	f.mu.Lock()
	if t, ok := f.tasks[taskID]; ok {
		t.Quantum++
		t.UpdatedAt = f.now()
	}
	f.mu.Unlock()

	checkpoint, done, stepErr := runStepRecovered(step)

	f.mu.Lock()
	preempted := false
	if t, ok := f.tasks[taskID]; ok && t.PreemptRequested {
		preempted = true
		t.PreemptRequested = false
	}
	f.mu.Unlock()
	if preempted {
		// The step's result is fenced out on purpose: neither a failure nor a success, and the task
		// is READY to resume. Reporting it as either is the mistake this type exists to avoid.
		return QuantumResult{Executed: false}
	}

	if stepErr != nil {
		if isCancellation(stepErr) {
			releaseErr, releaseDiscarded := classifyTerminal(f.Release(taskID, agentID, epoch))
			return QuantumResult{Executed: true, StepErr: stepErr, CommitErr: releaseErr, Discarded: releaseDiscarded}
		}
		attempts, rawFailErr := f.failWithAttempts(taskID, agentID, epoch, stepErr)
		failErr, failDiscarded := classifyTerminal(rawFailErr)
		if failDiscarded {
			// The failure could not be recorded because the task is no longer ours: the verdict is
			// discarded with the result, and no retry was consumed.
			return QuantumResult{Executed: true, StepErr: stepErr, Discarded: true}
		}
		// Recorded where the counter was modified, not inferred afterwards. The scorer's retry
		// component counts a failed attempt as one consumed retry — the same unit as before — but it
		// is now a fact from the transition rather than a guess about the error chain, which is what
		// let a commit failure riding on the same error zero it out.
		consumed := 0
		if attempts > 0 {
			consumed = 1
		}
		return QuantumResult{Executed: true, StepErr: stepErr, CommitErr: failErr, RetriesConsumed: consumed}
	}

	if done {
		var rawCompleteErr error
		if checkpoint != nil {
			rawCompleteErr = f.CompleteWithCheckpoint(taskID, agentID, epoch, checkpoint)
		} else {
			rawCompleteErr = f.Complete(taskID, agentID, epoch)
		}
		commitErr, discarded := classifyTerminal(rawCompleteErr)
		// StepErr stays nil: the executor did its job. A refused completion is a storage fact; a
		// fenced one is not a fact about this executor at all.
		return QuantumResult{Executed: true, CommitErr: commitErr, Discarded: discarded}
	}
	yieldErr, yieldDiscarded := classifyTerminal(f.Yield(taskID, agentID, epoch, checkpoint))
	return QuantumResult{Executed: true, CommitErr: yieldErr, Discarded: yieldDiscarded}
}

func (f *Fabric) RunQuantum(taskID, agentID string, epoch uint64, step QuantumStep) error {
	// Kept for the callers that only need "did this quantum report a problem": the scheduler uses
	// RunQuantumResult, because it needs the dimensions kept apart.
	return f.RunQuantumResult(taskID, agentID, epoch, step).Err()
}

// isCancellation reports whether err is a deliberate stop rather than a task
// failure: context.Canceled anywhere in the chain (scheduler shutdown,
// operator cancel). context.DeadlineExceeded is deliberately NOT treated as
// cancellation — a step that ran out its own budget is a real failure the
// retry policy must see.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled)
}

// runStepRecovered executes one step closure with a panic boundary. A panic
// is reported as an error naming the panicked value — the fabric converts it
// through the normal Fail path (retry budget or terminal FAILED) instead of
// unwinding past the state machine.
func runStepRecovered(step QuantumStep) (checkpoint any, done bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			checkpoint, done, err = nil, false, fmt.Errorf("taskfabric: quantum step panicked: %v", r)
		}
	}()
	return step()
}
