package taskfabric

import (
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

// Yield is the quantum-boundary primitive (design §4 correction 2): it
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
// Terminal failure cascades to every transitive READY dependent: a FAILED
// predecessor can never satisfy the dependency gate again, so the downstream
// subgraph would otherwise sit unschedulable forever.
func (f *Fabric) Fail(id, agentID string, epoch uint64) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	t.RetryPolicy.Attempts++
	if t.CanRetry() {
		if err := t.transition(StateReady); err != nil {
			return err
		}
		// Record the failure while the failing agent is still attached —
		// the terminal/requeue event must not lose the actor. Ownership is
		// cleared only after the event is captured, so the following
		// task.ready event reflects the unowned task.
		pending = append(pending, f.recordLocked(t, EventTaskFailed))
		t.Owner = ""
		t.Lease = nil
		pending = append(pending, f.recordLocked(t, EventTaskReady))
		return nil
	}
	if err := t.transition(StateFailed); err != nil {
		return err
	}
	pending = append(pending, f.recordLocked(t, EventTaskFailed))
	// Terminal failure propagates: every transitive READY dependent can never
	// become schedulable again, so fail it here instead of stranding the
	// subgraph (see cascadeFailureLocked).
	f.cascadeFailureLocked(t.ID, &pending)
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
// Deletion emits NO event on purpose: it is housekeeping for graphs whose
// results were already harvested, not a durable-state transition. The memory
// store therefore cannot replay these tasks after a restart — accepted,
// because replay value of harvested ephemeral work is nil.
func (f *Fabric) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleteLocked(id)
}

// deleteLocked is Delete's body for callers that already hold f.mu (the
// CompilePlan batch compiler's rollback).
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
