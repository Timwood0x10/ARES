package taskfabric

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

var (
	// ErrTaskNotFound: the task id is unknown.
	ErrTaskNotFound = errors.New("taskfabric: task not found")
	// ErrNotOwner: the agent does not hold this task's lease.
	ErrNotOwner        = errors.New("taskfabric: agent does not own this task")
	ErrTaskUndeletable = errors.New("taskfabric: task is not deletable in its current state")
	// ErrEpochMismatch: the operation carried a stale fencing token (lease
	// epoch) — the task is now owned by a newer lease holder. This is the
	// guard against "A lease expired → B acquire → A late release" killing
	// B's ownership.
	ErrEpochMismatch = errors.New("taskfabric: lease epoch mismatch")
	// ErrIllegalState: the requested state transition is not allowed.
	ErrIllegalState = errors.New("taskfabric: illegal state transition")
	// ErrTaskNotReady: the task cannot be acquired in its current state.
	ErrTaskNotReady = errors.New("taskfabric: task not ready for acquire")
	// ErrTaskExists: a task with this id already exists.
	ErrTaskExists = errors.New("taskfabric: task already exists")
	// ErrNoCapableCandidate: no candidate scored > 0 for the task's required
	// capability, so Schedule could not pick an executor.
	ErrNoCapableCandidate = errors.New("taskfabric: no capable candidate")
)

// Fabric owns Tasks and their leases (design §6 of ares-runtime.md:
// Acquire / Release / Yield / Checkpoint). It is the scheduler's substrate:
// agents compete for tasks via CAS ownership, never via a leader's dispatch.
// Every ownership-carrying operation is fenced by the lease epoch (fencing
// token) so a stale holder can never act on a task it no longer owns.
type Fabric struct {
	mu         sync.Mutex
	tasks      map[string]*Task
	events     []TaskEvent
	store      ares_events.EventStore // optional persistent event sink (P2-C); guarded by mu
	confidence ConfidenceSource       // experience-derived confidence (§8 Skill-first); guarded by mu
	now        func() time.Time       // injectable clock for lease tests
	epoch      uint64
}

// NewFabric creates an empty Task Fabric.
func NewFabric() *Fabric {
	return &Fabric{tasks: make(map[string]*Task), now: time.Now}
}

// WithClock injects a controllable clock for deterministic lease-expiry tests.
// Cross-package callers (e.g. aresrecovery) use this to advance time without
// real sleeping. Nil falls back to time.Now.
func (f *Fabric) WithClock(now func() time.Time) *Fabric {
	f.mu.Lock()
	defer f.mu.Unlock()
	if now != nil {
		f.now = now
	}
	return f
}

// WithConfidenceSource wires the experience-derived confidence (design §8:
// Skill-first — Score's Confidence comes from ares_skills.Experience
// BestMatch SuccessRate). Schedule fills candidates that do not declare a
// confidence with the provider's prior. Nil detaches. Guarded by mu.
//
// Args:
//   - src: the confidence provider (may be nil to detach).
//
// Returns:
//   - *Fabric: the fabric for chaining.
func (f *Fabric) WithConfidenceSource(src ConfidenceSource) *Fabric {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confidence = src
	return f
}

// WithEventStore attaches a persistent event sink (ares-runtime P2-C): every
// task lifecycle transition is appended to the store on the task's stream, in
// addition to the in-memory log, so scheduler/task/lease state can be rebuilt
// across restarts. Nil detaches. Guarded by mu.
//
// Args:
//   - store: the event store to publish task.* events to.
//
// Returns:
//   - *Fabric: the fabric for chaining.
func (f *Fabric) WithEventStore(store ares_events.EventStore) *Fabric {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store = store
	return f
}

// Create registers a new READY task. The task is unowned and available for
// acquire. Idempotency: an existing id returns ErrTaskExists.
//
// Args:
//   - t: the task to register (ID must be non-empty).
//
// Returns:
//   - error: ErrTaskExists, or an error for an empty id.
func (f *Fabric) Create(t *Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.ID == "" {
		return errors.New("taskfabric: task id required")
	}
	if _, exists := f.tasks[t.ID]; exists {
		return ErrTaskExists
	}
	t.State = StateReady
	t.Owner = ""
	t.Lease = nil
	f.tasks[t.ID] = t
	f.record(t, EventTaskCreated)
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
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return 0, ErrTaskNotFound
	}
	if agentID == "" {
		return 0, errors.New("taskfabric: agent id required")
	}
	if t.State != StateReady && t.State != StateSuspended {
		return 0, ErrTaskNotReady
	}
	f.epoch++
	lease := NewLease(agentID, ttl, f.epoch)
	if err := t.transition(StateLeased); err != nil {
		return 0, err
	}
	t.Owner = agentID
	t.Lease = &lease
	f.record(t, EventTaskAcquired)
	return lease.Epoch, nil
}

// Start moves a LEASED task owned by agentID (at the fenced epoch) to RUNNING.
func (f *Fabric) Start(id, agentID string, epoch uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateRunning); err != nil {
		return err
	}
	f.record(t, EventTaskStarted)
	return nil
}

// Yield is the quantum-boundary primitive (design §4 correction 2): it
// hands execution back to the Runtime at a checkpoint. The state after yield
// is decided by the Scheduler (continue/suspend/preempt/handoff/complete);
// P0's default transition is SUSPENDED with the checkpoint preserved.
func (f *Fabric) Yield(id, agentID string, epoch uint64, checkpoint any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateSuspended); err != nil {
		return err
	}
	t.Checkpoint = checkpoint
	f.record(t, EventTaskYielded)
	if checkpoint != nil {
		f.record(t, EventTaskCheckpointed)
	}
	return nil
}

// Complete finalizes a RUNNING task owned by agentID (at the fenced epoch) as
// COMPLETED. The task's Checkpoint is preserved as-is: a quantum may have
// written progress (or a worker result) into it before completing.
func (f *Fabric) Complete(id, agentID string, epoch uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateCompleted); err != nil {
		return err
	}
	f.record(t, EventTaskCompleted)
	return nil
}

// CompleteWithCheckpoint finalizes a RUNNING task as COMPLETED while storing
// the quantum's output in the task Checkpoint. The plain Complete keeps
// whatever checkpoint was already on the task; this variant overwrites it
// with the caller-supplied result so a worker outcome survives completion
// (the kernel dispatch reads it back from the completed task — the serve
// result-reflux fix). The scheduler calls this instead of Complete when the
// step's quantum produced a real result.
func (f *Fabric) CompleteWithCheckpoint(id, agentID string, epoch uint64, checkpoint any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	t.Checkpoint = checkpoint
	if err := t.transition(StateCompleted); err != nil {
		return err
	}
	f.record(t, EventTaskCompleted)
	return nil
}

// Fail marks a RUNNING task FAILED, or requeues it to READY when the retry
// policy allows another attempt (Agent 死亡 ≠ Task 死亡).
func (f *Fabric) Fail(id, agentID string, epoch uint64) error {
	f.mu.Lock()
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
		t.Owner = ""
		t.Lease = nil
		f.record(t, EventTaskFailed)
		f.record(t, EventTaskReady)
		return nil
	}
	if err := t.transition(StateFailed); err != nil {
		return err
	}
	f.record(t, EventTaskFailed)
	return nil
}

// Release returns a LEASED/RUNNING/SUSPENDED task to READY, clearing owner
// and lease so another agent can acquire it. The epoch fencing guarantees a
// stale holder (whose lease expired and was re-acquired by another agent)
// cannot release the task out from under the new owner.
func (f *Fabric) Release(id, agentID string, epoch uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateReady); err != nil {
		return err
	}
	t.Owner = ""
	t.Lease = nil
	f.record(t, EventTaskReleased)
	return nil
}

// CheckExpiredLeases requeues every task whose lease expired without renewal.
// This is the crash-recovery primitive: a dead agent's tasks return to READY
// and become acquirable again. Returns the ids of every requeued task so the
// recovery path can act on exactly the tasks that expired — not on all READY
// tasks (a task that is READY for the first time, or was released/steal-
// requeued, is not a recovery candidate and must not be treated as one).
func (f *Fabric) CheckExpiredLeases() []string {
	f.mu.Lock()
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
		// acquire and resume it (Agent 死亡 ≠ Task 死亡).
		if t.State != StateLeased && t.State != StateRunning && t.State != StateSuspended {
			continue
		}
		if err := t.transition(StateReady); err != nil {
			continue
		}
		t.Owner = ""
		t.Lease = nil
		f.record(t, EventTaskExpired)
		requeued = append(requeued, t.ID)
	}
	return requeued
}

// Schedule picks the best capable candidate for a task and acquires it on its
// behalf (design §8: capability-aware scheduling — "who is the best executor",
// not merely "who is idle"). D2 (2026-08-16): the Scheduler orchestrates
// uniformly — ReadyTasks → Schedule → execute; idle agents Steal → Acquire.
// The scoring (capability overlap × (1-load) × confidence) comes from
// scheduler.go; Experience supplies confidence.
//
// Args:
//   - taskID: the task id.
//   - candidates: the agents competing to execute the task.
//   - ttl: the lease TTL granted to the winner.
//
// Returns:
//   - string: the winning agent id.
//   - uint64: the fencing token (lease epoch) the winner must present on
//     subsequent ownership-carrying operations.
//   - error: ErrNoCapableCandidate / ErrTaskNotFound / ErrTaskNotReady.
func (f *Fabric) Schedule(taskID string, candidates []Candidate, ttl time.Duration) (string, uint64, error) {
	t, err := f.Task(taskID)
	if err != nil {
		return "", 0, err
	}
	// Design §8 (Skill-first): the experience prior supplies confidence for
	// candidates that do not declare one — Score's Confidence comes from the
	// wired ConfidenceSource (ares_skills.Experience BestMatch SuccessRate).
	f.mu.Lock()
	src := f.confidence
	f.mu.Unlock()
	if src != nil {
		if conf := src.Confidence(t.Capability); conf > 0 {
			for i := range candidates {
				if candidates[i].Confidence <= 0 {
					candidates[i].Confidence = conf
				}
			}
		}
	}
	best := Pick(t.Capability, candidates)
	if best == nil {
		return "", 0, ErrNoCapableCandidate
	}
	epoch, err := f.Acquire(taskID, best.AgentID, ttl)
	if err != nil {
		return "", 0, err
	}
	return best.AgentID, epoch, nil
}

// Preempt cooperatively preempts a RUNNING task at a quantum boundary
// (architecture invariant #9: cooperative — never OS-style hard preemption).
// The task returns to READY with its checkpoint preserved, so another agent
// can acquire and resume it. The priority comparison itself is the caller's
// (Scheduler's) decision — Preempt is the primitive that hands the task back
// at the boundary; the fencing token ensures only the current holder can
// preempt its own task.
//
// Args:
//   - taskID: the task id.
//   - agentID: the preempting agent (must hold the lease).
//   - epoch: the fencing token returned by Acquire.
//   - reason: debug reason for the preemption (recorded in the event).
//
// Returns:
//   - error: ErrNotOwner / ErrEpochMismatch / ErrIllegalState.
func (f *Fabric) Preempt(taskID, agentID string, epoch uint64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(taskID, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateReady); err != nil {
		return err
	}
	t.Owner = ""
	t.Lease = nil
	f.record(t, EventTaskPreempted)
	return nil
}

// RunningTask is a snapshot of one currently-RUNNING task, for the
// scheduler's preemption decision (the Scheduler decides WHO is preempted;
// Preempt is the primitive that hands the task back).
type RunningTask struct {
	// ID is the task id.
	ID string
	// Owner is the current lease holder (must be the preempting agent).
	Owner string
	// Epoch is the fencing token the holder must present to Preempt.
	Epoch uint64
	// Priority is the task's scheduling priority (higher wins).
	Priority int
}

// RunningTasks returns a snapshot of every currently-RUNNING task. It feeds
// the scheduler's priority-preemption decision (v0.3.0 review: Preempt was
// production-unused); the caller must not hold any fabric lock while calling
// Preempt with the returned epochs.
func (f *Fabric) RunningTasks() []RunningTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RunningTask, 0, len(f.tasks))
	for _, t := range f.tasks {
		if t.State != StateRunning || t.Lease == nil {
			continue
		}
		out = append(out, RunningTask{
			ID:       t.ID,
			Owner:    t.Owner,
			Epoch:    t.Lease.Epoch,
			Priority: t.Priority,
		})
	}
	return out
}

// Task returns a copy of a task (ErrTaskNotFound when unknown). It returns a
// snapshot, never the internal pointer: callers may read the returned task
// freely while the fabric mutates the live task (state transitions under the
// fabric lock), so a caller that holds the result across its own reads cannot
// race with the fabric's writes.
func (f *Fabric) Task(id string) (*Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	snap := *t
	return &snap, nil
}

// Events returns a copy of the lifecycle event log — the state-rebuild source.
func (f *Fabric) Events() []TaskEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]TaskEvent, len(f.events))
	copy(out, f.events)
	return out
}

// ownerLocked returns the task and verifies agentID holds its lease at the
// fenced epoch. A mismatch between the presented epoch and the current lease
// epoch returns ErrEpochMismatch — the fencing token guard.
func (f *Fabric) ownerLocked(id, agentID string, epoch uint64) (*Task, error) {
	t, ok := f.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if t.Owner == "" || t.Owner != agentID {
		return nil, ErrNotOwner
	}
	if t.Lease == nil || t.Lease.Epoch != epoch {
		return nil, ErrEpochMismatch
	}
	return t, nil
}

// record appends one lifecycle event.
func (f *Fabric) record(t *Task, typ EventType) {
	ev := TaskEvent{
		Type:       typ,
		TaskID:     t.ID,
		AgentID:    t.Owner,
		Origin:     t.Origin,
		State:      t.State,
		Checkpoint: t.Checkpoint,
		At:         f.now(),
	}
	f.events = append(f.events, ev)
	if f.store == nil {
		return
	}
	// W3 Durability: must-persist events (TaskCreated, TaskCheckpointed,
	// TaskCompleted, TaskFailed, TaskExpired) carry state the runtime relies
	// on for recovery and replay. A failed append for these events is no
	// longer silently swallowed — it is logged so a durable-state divergence
	// (in-memory vs event log) is detectable. The in-memory state machine
	// still stays authoritative within a process (the append failure does not
	// roll back the transition), so the state machine is never broken by a
	// store fault. Observability events (Trace, Ready, etc.) remain
	// best-effort and silent on failure.
	if err := f.store.Append(context.Background(), t.ID, []*ares_events.Event{{
		Type:       taskEventType(typ),
		StreamID:   t.ID,
		ModuleName: "taskfabric",
		Payload: map[string]any{
			"task_id":  t.ID,
			"agent_id": t.Owner,
			"origin":   t.Origin,
			"state":    string(t.State),
		},
		Timestamp: ev.At,
	}}, 0); err != nil {
		if isMustPersistEvent(typ) {
			// W3: log the divergence so it is detectable. A must-persist event
			// that fails to append means the event log is out of sync with the
			// in-memory state — a recovery replay from the event log would miss
			// this transition. The runtime continues (in-memory is
			// authoritative), but the operator can see the gap.
			log.Printf("taskfabric: must-persist event %s for task %s append failed (durable log diverges from memory): %v", typ, t.ID, err)
		}
		// Observability events: silent best-effort (unchanged).
	}
}

// isMustPersistEvent reports whether a lifecycle event is a must-persist
// transition (W3): the runtime's recovery/replay correctness depends on these
// events being in the durable log. Other events (Ready, Acquired, Started,
// Yielded, Preempted, Released, Stolen) are observability-only: they enrich
// the trace but are not required for state rebuild.
func isMustPersistEvent(typ EventType) bool {
	switch typ {
	case EventTaskCreated, EventTaskCheckpointed, EventTaskCompleted,
		EventTaskFailed, EventTaskExpired:
		return true
	default:
		return false
	}
}

// taskEventType maps the fabric's internal event type to the ares_events
// task.* event type. Unknown types map to "" and are never published.
func taskEventType(typ EventType) ares_events.EventType {
	switch typ {
	case EventTaskCreated:
		return ares_events.EventTaskCreated
	case EventTaskReady:
		return ares_events.EventTaskReady
	case EventTaskAcquired:
		return ares_events.EventTaskAcquired
	case EventTaskStarted:
		return ares_events.EventTaskStarted
	case EventTaskYielded:
		return ares_events.EventTaskYielded
	case EventTaskCheckpointed:
		return ares_events.EventTaskCheckpointed
	case EventTaskPreempted:
		return ares_events.EventTaskPreempted
	case EventTaskReleased:
		return ares_events.EventTaskReleased
	case EventTaskCompleted:
		return ares_events.EventTaskCompleted
	case EventTaskFailed:
		return ares_events.EventTaskFailed
	case EventTaskExpired:
		return ares_events.EventTaskExpired
	case EventTaskStolen:
		return ares_events.EventTaskStolen
	default:
		return ""
	}
}

// Delete removes a task from the fabric entirely (fusion plan C4 review #2:
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
