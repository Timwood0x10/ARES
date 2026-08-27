package taskfabric

import (
	"context"
	"errors"
	"log"
	"sort"
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
// maxInMemoryEvents bounds the in-memory lifecycle log (N8: unbounded growth).
// The log is compacted to this size only when it reaches 2× the bound, so the
// amortized cost of the cap is O(1) per append and the resident log stays
// within 2× the bound. The durable event store (when attached) keeps the FULL
// history; the in-memory log is a bounded, convenience view for replay.
const maxInMemoryEvents = 10000

type Fabric struct {
	mu         sync.Mutex
	tasks      map[string]*Task
	events     []TaskEvent
	store      ares_events.EventStore // optional persistent event sink (P2-C); guarded by mu
	confidence ConfidenceSource       // experience-derived confidence (§8 Skill-first); guarded by mu
	now        func() time.Time       // injectable clock for lease tests
	epoch      uint64

	// flushSeq/flushedSeq gate durable appends into strict causal order (N7:
	// concurrent flushAppends must not land out of order in the store's
	// version sequence). flushSeq is assigned under f.mu in recordLocked —
	// the same lock that serializes every state transition — so the sequence
	// order IS the causal order. flushCond waits until all earlier sequences
	// have been flushed, making store.Append calls across goroutines land in
	// record order regardless of which goroutine reaches flushAppends first.
	flushCond  *sync.Cond
	flushSeq   uint64 // next sequence; guarded by mu
	flushedSeq uint64 // last sequence durably appended; guarded by flushCond.L
}

// NewFabric creates an empty Task Fabric.
func NewFabric() *Fabric {
	f := &Fabric{tasks: make(map[string]*Task), now: time.Now}
	f.flushCond = sync.NewCond(&sync.Mutex{})
	return f
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
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
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
	pending = append(pending, f.recordLocked(t, EventTaskCreated))
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
		return 0, errors.New("taskfabric: agent id required")
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
// P0's default transition is SUSPENDED with the checkpoint preserved.
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
	t.Checkpoint = checkpoint
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
func (f *Fabric) CompleteWithCheckpoint(id, agentID string, epoch uint64, checkpoint any) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	t.Checkpoint = checkpoint
	if err := t.transition(StateCompleted); err != nil {
		return err
	}
	pending = append(pending, f.recordLocked(t, EventTaskCompleted))
	return nil
}

// Fail marks a RUNNING task FAILED, or requeues it to READY when the retry
// policy allows another attempt (Agent 死亡 ≠ Task 死亡).
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
		// N8: record the failure while the failing agent is still attached —
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
func (f *Fabric) Renew(id, agentID string, epoch uint64, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("taskfabric: renew ttl must be positive")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.ownerLocked(id, agentID, epoch)
	if err != nil {
		return err
	}
	if t.Lease == nil {
		return ErrTaskNotFound
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
	// N8: record the released event while the releasing agent is still
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
		// acquire and resume it (Agent 死亡 ≠ Task 死亡).
		if t.State != StateLeased && t.State != StateRunning && t.State != StateSuspended {
			continue
		}
		if err := t.transition(StateReady); err != nil {
			continue
		}
		// N8: record the expiry while the dead agent is still attached — the
		// terminal event must identify whose lease expired. Ownership is
		// cleared only after the event is captured.
		pending = append(pending, f.recordLocked(t, EventTaskExpired))
		t.Owner = ""
		t.Lease = nil
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
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
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
	pending = append(pending, f.recordLocked(t, EventTaskPreempted))
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

// pendingAppend is one durable-store write deferred until after f.mu is
// released. recordLocked builds it under the lock (cheap, in-memory only);
// flushAppends performs the actual store.Append I/O off-lock so a slow or
// blocking event store never stalls the fabric's CAS/state-machine mutex.
type pendingAppend struct {
	store  ares_events.EventStore // captured under lock — never read via f.store off-lock
	typ    EventType
	taskID string
	event  *ares_events.Event
	// seq is the fabric-wide monotonic sequence assigned under f.mu at record
	// time (N7). flushAppends waits for seq contiguity so durable appends from
	// concurrent fabric calls land in causal order.
	seq uint64
}

// recordLocked appends one lifecycle event to the in-memory log (the only
// part that needs f.mu) and, when a store is attached, returns the durable
// write to be flushed AFTER the lock is released. It never performs I/O.
// Callers MUST be holding f.mu and MUST flush the returned value (via a
// deferred flushAppends) once unlocked. Returns nil when there is nothing to
// persist (no store, or an unmapped event type).
func (f *Fabric) recordLocked(t *Task, typ EventType) *pendingAppend {
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
	// Cap the in-memory event log (N8: unbounded growth). Only compact when
	// the log exceeds 2×max so the amortized cost is O(1) per append.
	if max := maxInMemoryEvents; max > 0 && len(f.events) > 2*max {
		copy(f.events, f.events[len(f.events)-max:])
		f.events = f.events[:max]
	}
	if f.store == nil {
		return nil
	}
	et := taskEventType(typ)
	if et == "" {
		return nil
	}
	f.flushSeq++
	return &pendingAppend{
		store:  f.store,
		typ:    typ,
		taskID: t.ID,
		event: &ares_events.Event{
			Type:       et,
			StreamID:   t.ID,
			ModuleName: "taskfabric",
			Payload: map[string]any{
				"task_id":  t.ID,
				"agent_id": t.Owner,
				"origin":   t.Origin,
				"state":    string(t.State),
			},
			Timestamp: ev.At,
		},
		seq: f.flushSeq,
	}
}

// flushAppends performs the deferred durable writes off-lock. It is registered
// with `defer f.flushAppends(&pending)` BEFORE `defer f.mu.Unlock()` so, by
// LIFO defer order, the unlock runs first and this flush runs immediately
// after — still within the same call (so W3 divergence logging stays
// synchronous with the mutating method) but with f.mu already released (so the
// store I/O never blocks other fabric operations). Takes a pointer so it reads
// the slice's final value populated during the method body.
//
// W3 Durability: must-persist events (TaskCreated, TaskCheckpointed,
// TaskCompleted, TaskFailed, TaskExpired) carry state the runtime relies on
// for recovery and replay. A failed append for these events is not silently
// swallowed — it is logged so a durable-state divergence (in-memory vs event
// log) is detectable. The in-memory state machine stays authoritative within a
// process (the append failure does not roll back the transition). Observability
// events remain best-effort and silent on failure.
func (f *Fabric) flushAppends(pending *[]*pendingAppend) {
	for _, p := range *pending {
		if p == nil {
			continue
		}
		// N7: wait until every earlier-recorded durable event has been
		// appended, so concurrent fabric calls flush in causal (record) order
		// and the store's per-stream version sequence never inverts.
		f.flushCond.L.Lock()
		for p.seq > f.flushedSeq+1 {
			f.flushCond.Wait()
		}
		var appendErr error
		if p.store != nil {
			appendErr = p.store.Append(context.Background(), p.taskID, []*ares_events.Event{p.event}, 0)
		}
		f.flushedSeq++
		f.flushCond.L.Unlock()
		f.flushCond.Broadcast()
		if appendErr != nil {
			if isMustPersistEvent(p.typ) {
				log.Printf("taskfabric: must-persist event %s for task %s append failed (durable log diverges from memory): %v", p.typ, p.taskID, appendErr)
			}
		}
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

// IDs returns a snapshot of every task id in the fabric (any state). Used by
// housekeeping sweeps — e.g. the collaboration-graph janitor that deletes
// stale terminal tasks from previous submissions.
func (f *Fabric) IDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.tasks))
	for id := range f.tasks {
		out = append(out, id)
	}
	return out
}

// LeaseEntry is one non-terminal task's scheduling-relevant state — the
// read-only view the runtime introspection panel consumes (monitoring.md
// Domain B: which tasks hold leases, how long until expiry, checkpoint
// progress, dependency posture).
type LeaseEntry struct {
	// TaskID is the fabric task identifier.
	TaskID string `json:"taskID"`
	// Capability is the required capability.
	Capability string `json:"capability"`
	// State is the current lifecycle state (never terminal in a snapshot).
	State TaskState `json:"state"`
	// Priority drives preemption decisions.
	Priority int `json:"priority"`
	// Owner is the lease-holding agent; empty when the task is unowned.
	Owner string `json:"owner"`
	// Epoch is the lease acquisition counter (stale-renew observability).
	Epoch uint64 `json:"epoch"`
	// ExpiresAt is the lease expiry; zero when unowned.
	ExpiresAt time.Time `json:"expiresAt"`
	// HasCheckpoint reports whether durable progress exists.
	HasCheckpoint bool `json:"hasCheckpoint"`
	// Dependencies are the task's prerequisite IDs (copied).
	Dependencies []string `json:"dependencies"`
}

// LeaseSnapshot returns a point-in-time copy of every non-terminal task,
// ordered by TaskID for stable rendering. Terminal tasks (COMPLETED/FAILED)
// are excluded so the snapshot stays bounded by live work rather than
// accumulating history. Purely read-only: everything is copied under f.mu and
// no transition/renew side effects can fire (unlike CheckExpiredLeases,
// which is a WRITE path and must never be used for observation).
func (f *Fabric) LeaseSnapshot() []LeaseEntry {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]LeaseEntry, 0, len(f.tasks))
	for id, t := range f.tasks {
		if t.State == StateCompleted || t.State == StateFailed {
			continue
		}
		e := LeaseEntry{
			TaskID:        id,
			Capability:    t.Capability,
			State:         t.State,
			Priority:      t.Priority,
			Owner:         t.Owner,
			HasCheckpoint: t.Checkpoint != nil,
		}
		if t.Lease != nil {
			e.Epoch = t.Lease.Epoch
			e.ExpiresAt = t.Lease.ExpiresAt
		}
		if len(t.Dependencies) > 0 {
			e.Dependencies = append([]string(nil), t.Dependencies...)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}
