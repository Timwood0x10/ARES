package taskfabric

import (
	"fmt"
	"time"
)

// Create registers a new READY task. The task is unowned and available for
// acquire. Idempotency: an existing id returns ErrTaskExists.
//
// Args:
//   - t: the task to register (ID must be non-empty).
//
// Returns:
//   - error: ErrTaskExists, or an error for an empty id.
func (f *Fabric) Create(t *Task) error {
	// Sample the attribution source BEFORE taking f.mu: the stamp fn talks to
	// the strategy control plane and must never run while the fabric state
	// machine is locked (deadlock avoidance), and it must be cheap.
	strategyID := f.strategyStampID()
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	return f.createLocked(t, strategyID, &pending)
}

// createLocked is Create's body for callers that already hold f.mu (the
// CompilePlan batch compiler). The caller owns flushing the accumulated
// pending appends after releasing the lock.
func (f *Fabric) createLocked(t *Task, strategyID string, pending *[]*pendingAppend) error {
	if t.ID == "" {
		return ErrTaskIDRequired
	}
	if _, exists := f.tasks[t.ID]; exists {
		return ErrTaskExists
	}
	// Copy the caller's *Task so the fabric owns an isolated instance —
	// the caller keeping (or reusing) its *t cannot then race the fabric's
	// snapshot/state reads. `cp := *t` isolates every scalar field; the
	// Dependencies slice is a reference type, so it is copied explicitly below
	// (otherwise cp.Dependencies still aliases the caller's backing array and
	// LeaseSnapshot/TaskSnapshot reading it would race a caller mutation).
	// Checkpoint (any) is intentionally NOT deep-copied: it may hold arbitrary
	// types with no generic clone, and a freshly-created task's checkpoint is
	// nil in practice — callers must not mutate a checkpoint they have handed
	// to Create.
	cp := *t
	if len(t.Dependencies) > 0 {
		cp.Dependencies = append([]string(nil), t.Dependencies...)
	}
	cp.State = StateReady
	cp.Owner = ""
	cp.Lease = nil
	cp.CreatedAt = f.now()
	cp.UpdatedAt = cp.CreatedAt
	// Stamp the submission-time strategy attribution onto the task's
	// checkpoint envelope (once per Create; a pre-stamped envelope wins).
	stampStrategyAttribution(&cp, strategyID)
	f.tasks[t.ID] = &cp
	*pending = append(*pending, f.recordLocked(&cp, EventTaskCreated))
	return nil
}

// Acquire is the CAS ownership claim. Only an unowned READY (or SUSPENDED —
// checkpoint preserved, cooperative re-acquisition) task can be leased; a
// concurrent or repeated acquire is rejected, so two agents competing for the
// same task see exactly one winner.
//
// Args:
//   - id: the task id.
//   - agentID: the acquiring agent.
//   - ttl: the lease TTL.
//
// Returns:
//   - uint64: the fencing token (lease epoch) the agent must present on every
//     subsequent ownership-carrying operation.
//   - error: ErrTaskNotFound / ErrTaskNotReady.
func (f *Fabric) Acquire(id, agentID string, ttl time.Duration) (uint64, error) {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return 0, ErrTaskNotFound
	}
	if agentID == "" {
		return 0, ErrAgentIDRequired
	}
	if t.State != StateReady && t.State != StateSuspended {
		return 0, ErrTaskNotReady
	}
	if !t.Deadline.IsZero() && f.now().After(t.Deadline) {
		// Race guard for ExpireDeadlines: the scan runs on the recovery tick, so
		// a task can pass its deadline between two ticks. Granting it a lease
		// here would hand out execution rights the deadline has already revoked.
		return 0, fmt.Errorf("task %s deadline %s passed: %w",
			t.ID, t.Deadline.UTC().Format(time.RFC3339), ErrTaskDeadlineExceeded)
	}
	if !t.NextAttemptAt.IsZero() && t.NextAttemptAt.After(f.now()) {
		// Backoff guard: the query gates (ReadyTasks/ResumableTasks) already hide
		// the task, but Acquire is a public grant point, so a direct caller must
		// not be able to start the retry early either.
		return 0, fmt.Errorf("task %s is in retry backoff until %s: %w",
			t.ID, t.NextAttemptAt.UTC().Format(time.RFC3339), ErrTaskNotReady)
	}
	f.epoch++
	// Build the lease on the FABRIC's clock (f.now), not wall time: expiry is
	// evaluated against f.now (CheckExpiredLeases), so a mixed clock pair made
	// every lease born-expired whenever a test/fixture advanced the fabric
	// clock past real time — recovery then requeued live runners mid-quantum.
	lease := Lease{
		Owner:     agentID,
		ExpiresAt: f.now().Add(ttl),
		Epoch:     f.epoch,
	}
	if err := t.transition(StateLeased); err != nil {
		return 0, err
	}
	t.Owner = agentID
	t.Lease = &lease
	pending = append(pending, f.recordLocked(t, EventTaskAcquired))
	return lease.Epoch, nil
}

// Start moves a LEASED task owned by agentID (at the fenced epoch) to RUNNING.
func (f *Fabric) Start(id, agentID string, epoch uint64) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateRunning); err != nil {
		return err
	}
	pending = append(pending, f.recordLocked(t, EventTaskStarted))
	return nil
}

// Yield is the quantum-boundary primitive: it
// hands execution back to the Runtime at a checkpoint. The state after yield
// is decided by the Scheduler (continue/suspend/preempt/handoff/complete);
// The default transition is SUSPENDED with the checkpoint preserved.
func (f *Fabric) Yield(id, agentID string, epoch uint64, checkpoint any) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateSuspended); err != nil {
		return err
	}
	// Only overwrite the checkpoint when the quantum provides a non-nil
	// value. A nil Yield (progress pause without new data) must not erase
	// the checkpoint saved by a previous quantum or commit envelope.
	if checkpoint != nil {
		t.Checkpoint = checkpoint
	}
	pending = append(pending, f.recordLocked(t, EventTaskYielded))
	if checkpoint != nil {
		pending = append(pending, f.recordLocked(t, EventTaskCheckpointed))
	}
	return nil
}

// Complete finalizes a RUNNING task owned by agentID (at the fenced epoch) as
// COMPLETED. The task's Checkpoint is preserved as-is: a quantum may have
// written progress (or a worker result) into it before completing.
func (f *Fabric) Complete(id, agentID string, epoch uint64) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateCompleted); err != nil {
		return err
	}
	pending = append(pending, f.recordLocked(t, EventTaskCompleted))
	return nil
}

// CompleteWithCheckpoint finalizes a RUNNING task as COMPLETED while storing
// the quantum's output in the task Checkpoint. The plain Complete keeps
// whatever checkpoint was already on the task; this variant overwrites it
// with the caller-supplied result so a worker outcome survives completion
// (the kernel dispatch reads it back from the completed task — the serve
// result-reflux fix). The scheduler calls this instead of Complete when the
// step's quantum produced a real result.
//
// The state transition is validated BEFORE the checkpoint is written: a task
// whose current state forbids COMPLETED (e.g. LEASED but never started)
// fails with ErrIllegalState and keeps its previous checkpoint — writing the
// checkpoint first and failing the transition afterwards left a
// never-completed task with the completed run's result embedded in it.
func (f *Fabric) CompleteWithCheckpoint(id, agentID string, epoch uint64, checkpoint any) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateCompleted); err != nil {
		return err
	}
	t.Checkpoint = checkpoint
	pending = append(pending, f.recordLocked(t, EventTaskCompleted))
	return nil
}

// Fail marks a RUNNING task FAILED, or requeues it to READY when the retry
// policy allows another attempt (Agent death ≠ Task death).
//
// cause is the quantum step error to stamp into the checkpoint's LastError
// on the TERMINAL failure path — it is what the external task view surfaces
// as "why did this fail". Pass nil when no error text exists (operator
// kill, recovery sweeps). A requeue leaves the checkpoint untouched: the
// task continues, and a stale failure cause must not shadow a later outcome.
//
// Terminal failure cascades to every transitive READY dependent: a FAILED
// predecessor can never satisfy the dependency gate again, so the downstream
// subgraph would otherwise sit unschedulable forever.
func (f *Fabric) Fail(id, agentID string, epoch uint64, cause error) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	t.RetryPolicy.Attempts++
	// A retry needs BOTH budget and a cause worth retrying: a permanent cause
	// goes terminal even with budget left (see classifyFailure). Unclassified
	// causes stay retryable, so tasks that never mark their causes keep 0.3.2
	// behaviour exactly.
	retryable := classifyFailure(cause)
	if retryable && t.CanRetry() {
		if err := t.transition(StateReady); err != nil {
			return err
		}
		// Schedule the next attempt before recording: task.failed is the
		// must-persist carrier of NextAttemptAt (task.ready is
		// observability-only), so the restore fold recovers the due time from it
		// — a restart must not turn a pending backoff into an immediate retry.
		// A zero delay leaves NextAttemptAt zero: immediately runnable, exactly
		// as before backoff existed.
		t.NextAttemptAt = nextAttemptAt(f.now(), t.RetryPolicy.Attempts, t.BackoffBase, t.BackoffMax)
		// Record the failure while the failing agent is still attached —
		// the terminal/requeue event must not lose the actor. Ownership is
		// cleared only after the event is captured, so the following
		// task.ready event reflects the unowned task.
		pending = append(pending, f.failedEventLocked(t, retryable))
		t.Owner = ""
		t.Lease = nil
		// task.ready is observability-only (never folded), but it is the event an
		// operator watches: carrying the due time is what distinguishes "a retry
		// is scheduled" from "the task is stuck". Absent when no delay applies,
		// so the default path keeps its exact payload.
		var readyExtras map[string]any
		if !t.NextAttemptAt.IsZero() {
			readyExtras = map[string]any{
				restoreKeyNextAttemptAt: t.NextAttemptAt.UTC().Format(time.RFC3339),
			}
		}
		pending = append(pending, f.recordWithExtrasLocked(t, EventTaskReady, readyExtras))
		return nil
	}
	return f.failTerminalLocked(t, cause, retryable, &pending)
}

// failedEventLocked records task.failed carrying the retry classification that
// produced it, so the event stream alone answers "will this be retried?".
// Caller must hold f.mu.
func (f *Fabric) failedEventLocked(t *Task, retryable bool) *pendingAppend {
	return f.recordWithExtrasLocked(t, EventTaskFailed, map[string]any{
		payloadKeyRetryable: retryable,
	})
}

// failTerminalLocked drives a task to terminal FAILED: transition, stamp the
// cause into the checkpoint, record the failure, then propagate to the READY
// dependents that can never run again. It is the single terminal-failure path,
// shared by Fail and ExpireDeadlines — the latter has no owning agent, so it
// cannot pass Fail's ownership check.
//
// Caller must hold f.mu. Ownership deliberately stays attached (as before the
// split): the terminal event must identify who held the task, and a FAILED task
// can no longer be acquired, so the stale lease is inert provenance.
func (f *Fabric) failTerminalLocked(t *Task, cause error, retryable bool, pending *[]*pendingAppend) error {
	if err := t.transition(StateFailed); err != nil {
		return err
	}
	// Replace (never mutate) the checkpoint pointer so off-lock snapshot
	// readers of the previous envelope stay race-free — the fabric's
	// documented checkpoint ownership rule.
	t.Checkpoint = checkpointWithCause(t.Checkpoint, cause)
	*pending = append(*pending, f.failedEventLocked(t, retryable))
	// Terminal failure propagates: every transitive READY dependent can never
	// become schedulable again, so fail it here instead of stranding the
	// subgraph (see cascadeFailureLocked).
	f.cascadeFailureLocked(t.ID, pending)
	return nil
}

// Renew extends the lease of a LEASED/RUNNING/SUSPENDED task owned by
// agentID at the fenced epoch. It is the heartbeat a long-running quantum
// sends so its own lease does not expire mid-execution: without renewal, any
// step longer than the TTL was requeued by CheckExpiredLeases while the
// original holder was still executing — duplicate concurrent execution of the
// same task (state stayed fenced-correct, but work and side effects doubled).
//
// Renewal fails (and callers must stop heartbeating) when the caller no
// longer owns the task: it was preempted, requeued after expiry, or finalized.
// It also fails once the lease has EXPIRED, even before the recovery sweep
// requeued the task: a holder that paused past its TTL and then heartbeated
// would otherwise resurrect the lease with an unchanged epoch — invisible to
// fencing — and keep a task the sweep was about to hand to another agent.
func (f *Fabric) Renew(id, agentID string, epoch uint64, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrInvalidTTL
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	// ownerLocked guarantees Lease != nil on success, so no nil check here
	// (the previous defensive branch was unreachable dead code).
	if t.Lease.IsExpired(f.now()) {
		return ErrLeaseExpired
	}
	t.Lease.ExpiresAt = f.now().Add(ttl)
	return nil
}

// Release returns a LEASED/RUNNING/SUSPENDED task to READY, clearing owner
// and lease so another agent can acquire it. The epoch fencing guarantees a
// stale holder (whose lease expired and was re-acquired by another agent)
// cannot release the task out from under the new owner.
func (f *Fabric) Release(id, agentID string, epoch uint64) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateReady); err != nil {
		return err
	}
	// Record the released event while the releasing agent is still
	// attached (provenance), then clear ownership so the task is unowned.
	pending = append(pending, f.recordLocked(t, EventTaskReleased))
	t.Owner = ""
	t.Lease = nil
	return nil
}

// CheckExpiredLeases requeues every task whose lease expired without renewal.
// This is the crash-recovery primitive: a dead agent's tasks return to READY
// and become acquirable again. Returns the ids of every requeued task so the
// recovery path can act on exactly the tasks that expired — not on all READY
// tasks (a task that is READY for the first time, or was released/steal-
// requeued, is not a recovery candidate and must not be treated as one).
func (f *Fabric) CheckExpiredLeases() []string {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	now := f.now()
	var requeued []string
	for _, t := range f.tasks {
		if t.Lease == nil || !t.Lease.IsExpired(now) {
			continue
		}
		// LEASED/RUNNING/SUSPENDED tasks with an expired lease are requeued
		// to READY. SUSPENDED is included: a dead agent's suspended task
		// (checkpoint preserved) must return to READY so another agent can
		// acquire and resume it (Agent death ≠ Task death).
		if t.State != StateLeased && t.State != StateRunning && t.State != StateSuspended {
			continue
		}
		if err := t.transition(StateReady); err != nil {
			continue
		}
		// Record the expiry while the dead agent is still attached — the
		// terminal event must identify whose lease expired. Ownership is
		// cleared only after the event is captured.
		pending = append(pending, f.recordLocked(t, EventTaskExpired))
		t.Owner = ""
		t.Lease = nil
		requeued = append(requeued, t.ID)
	}
	return requeued
}

// ExpireDeadlines fails every task whose absolute Deadline has passed,
// regardless of who holds it, and returns their ids so the recovery sweep can
// report exactly what it stopped.
//
// It deliberately does NOT reuse Fail: Fail begins with ownerLocked, which
// requires a matching Owner and lease epoch — a READY task has neither, and a
// LEASED/RUNNING task's lease may already have been requeued by
// CheckExpiredLeases. A deadline is the Runtime's constraint on the task's
// lifetime, not an agent's action, so it needs a terminal path that does not
// depend on holding execution rights (see failTerminalLocked).
//
// Two consequences worth keeping in mind:
//   - The retry budget is not spent: a task past its deadline is not retried,
//     even while Attempts < MaxRetries.
//   - Owner/Lease stay attached for provenance; the holder's next
//     Complete/Yield/Fail is rejected by ownerLocked, so a late writer cannot
//     race the expiry.
func (f *Fabric) ExpireDeadlines() []string {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	now := f.now()
	var expired []string
	for _, t := range f.tasks {
		if t.Deadline.IsZero() || !now.After(t.Deadline) {
			continue
		}
		// Only live states can be stopped. Terminal tasks are history: their
		// deadline passing later must not rewrite the outcome.
		switch t.State {
		case StateReady, StateLeased, StateRunning, StateSuspended:
		default:
			continue
		}
		if err := f.failTerminalLocked(t, ErrTaskDeadlineExceeded, false, &pending); err != nil {
			// An illegal transition means the state moved under us in this
			// tick (e.g. the holder completed): the deadline no longer applies,
			// so skip this task instead of failing the whole sweep.
			continue
		}
		expired = append(expired, t.ID)
	}
	return expired
}

// Delete removes a task from the fabric entirely (submitted
// submitted collaboration graphs are EPHEMERAL — results are harvested by the
// caller before deletion, so long-running kernels must not accumulate zombie
// entries from failed/timed-out graphs).
//
// Allowed only from states with no in-flight or resumable execution: READY,
// COMPLETED, FAILED. LEASED/RUNNING/SUSPENDED are refused with
// ErrTaskUndeletable — their quanta must finish or expire through the normal
// paths; callers retry deletion afterwards if needed.
//
// Deletion writes a must-persist tombstone (EventTaskDeleted) when a durable
// store is attached: the store already holds the task's task.created, so
// without a tombstone RestoreFromStore folds the deleted task back and the
// discarded work becomes READY again and re-executes after a restart.
func (f *Fabric) Delete(id string) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	return f.deleteWithTombstoneLocked(id, &pending)
}

// deleteWithTombstoneLocked records the tombstone for id and removes the task.
// Callers MUST hold f.mu and flush the returned pending appends.
func (f *Fabric) deleteWithTombstoneLocked(id string, pending *[]*pendingAppend) error {
	t, ok := f.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	switch t.State {
	case StateReady, StateCompleted, StateFailed:
		// Record while the task is still attached so the tombstone carries the
		// same provenance payload every other must-persist event does.
		*pending = append(*pending, f.recordLocked(t, EventTaskDeleted))
		delete(f.tasks, id)
		return nil
	default:
		return ErrTaskUndeletable
	}
}

// deleteLocked removes a task without emitting a tombstone. It is the
// CompilePlan batch compiler's rollback primitive and is paired with
// discardAppends: the batch's task.created appends are discarded because the
// tasks no longer exist, so publishing a task.deleted for a task the durable
// log never learned about would be a phantom of the mirror-image kind.
func (f *Fabric) deleteLocked(id string) error {
	t, ok := f.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	switch t.State {
	case StateReady, StateCompleted, StateFailed:
		delete(f.tasks, id)
		return nil
	default:
		return ErrTaskUndeletable
	}
}

// RestoreTask re-installs a task snapshot that Delete removed, preserving the
// captured state (including terminal states). It is the rollback primitive
// for delete-then-rebuild flows: the compile coordinator deletes the previous
// compile's tasks before compiling the replacement batch, and a failed
// compile restores the snapshots so the graph stays runnable instead of
// silently losing the deleted work.
//
// The snapshot must come from Task (a fabric-owned copy) and carry a non-empty
// ID; an occupied id is refused with ErrTaskExists. Like Delete, it emits no
// lifecycle event: it is bookkeeping that undoes bookkeeping (the durable log
// keeps the events of the task's first life), not a state transition.
func (f *Fabric) RestoreTask(t *Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t == nil || t.ID == "" {
		return ErrTaskIDRequired
	}
	if _, exists := f.tasks[t.ID]; exists {
		return ErrTaskExists
	}
	// Same aliasing discipline as Create: scalars by value, reference fields
	// copied explicitly so the fabric owns an isolated instance.
	cp := *t
	if len(t.Dependencies) > 0 {
		cp.Dependencies = append([]string(nil), t.Dependencies...)
	}
	if t.Lease != nil {
		l := *t.Lease
		cp.Lease = &l
	}
	f.tasks[t.ID] = &cp
	return nil
}
