package taskfabric

import (
	"errors"
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
	// ErrTaskIDRequired: Create was called without an id.
	ErrTaskIDRequired = errors.New("taskfabric: task id required")
	// ErrAgentIDRequired: Acquire was called without an owning agent id.
	ErrAgentIDRequired = errors.New("taskfabric: agent id required")
	// ErrInvalidTTL: a lease operation was called with a non-positive TTL.
	ErrInvalidTTL = errors.New("taskfabric: lease ttl must be positive")
	// ErrLeaseExpired: a heartbeat (Renew) arrived after the lease had
	// already expired. The holder must treat this as lost ownership and stop
	// — extending an expired lease would resurrect a task the crash-recovery
	// sweep is about to requeue, with an unchanged epoch, so fencing could
	// not reveal the resurrection.
	ErrLeaseExpired = errors.New("taskfabric: lease expired")
	// ErrTaskNotMutable: an operation that rewrites a task's compiled shape
	// (SetDependencies / UpdatePayload) was attempted on a task in a state
	// that forbids it. Unlike ErrTaskUndeletable this is a *soft* refusal:
	// the caller (incremental compiler) records it and moves on, because the
	// graph change is still real — only its projection onto this one task
	// has to wait for the task to reach a mutable state.
	ErrTaskNotMutable = errors.New("taskfabric: task is not mutable in its current state")
)

// Fabric owns Tasks and their leases (ares-runtime.md:
// Acquire / Release / Yield / Checkpoint). It is the scheduler's substrate:
// agents compete for tasks via CAS ownership, never via a leader's dispatch.
// Every ownership-carrying operation is fenced by the lease epoch (fencing
// token) so a stale holder can never act on a task it no longer owns.
// maxInMemoryEvents bounds the in-memory lifecycle log (preventing unbounded growth).
// The log is compacted to this size only when it reaches 2× the bound, so the
// amortized cost of the cap is O(1) per append and the resident log stays
// within 2× the bound. The durable event store (when attached) keeps the FULL
// history; the in-memory log is a bounded, convenience view for replay.
const maxInMemoryEvents = 10000

type Fabric struct {
	mu         sync.Mutex
	tasks      map[string]*Task
	events     []TaskEvent
	store      ares_events.EventStore // optional persistent event sink; guarded by mu
	confidence ConfidenceSource       // experience-derived confidence (Skill-first); guarded by mu
	now        func() time.Time       // injectable clock for lease tests
	epoch      uint64
	// strategyStamp is the submission-time attribution source: called
	// once per Create to stamp the checkpoint envelope's StrategyID. Guarded
	// by mu; nil means "no strategy deployed / wiring absent", which reads as
	// the active-strategy fallback downstream.
	strategyStamp func() string

	// flushSeq/flushedSeq gate durable appends into strict causal order
	// (concurrent flushAppends must not land out of order in the store's
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

// WithConfidenceSource wires the experience-derived confidence
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

// PriorConfidence reports the wired experience prior for a task pattern,
// 0 when no source is wired or no prior is recorded for the pattern. The
// kernel scheduler reads it to decide whether an unmeasured (history-less)
// candidate's confidence should yield to the prior (M4.4 read side) or keep
// the neutral default — without this signal the tracker's neutral 1.0 would
// permanently mask the prior.
func (f *Fabric) PriorConfidence(taskPattern string) float64 {
	f.mu.Lock()
	src := f.confidence
	f.mu.Unlock()
	if src == nil {
		return 0
	}
	return src.Confidence(taskPattern)
}

// WithStrategyStamp wires the submission-time attribution source (evolution
// loop closure). The fabric calls it once per Create to stamp the task's
// checkpoint envelope with the strategy that was active at submission, so
// runtime fitness samples stay attributed to the strategy that actually
// produced them — even when a promote happens mid-flight. It must be cheap
// and non-blocking (it runs on the submission path); returning "" means "no
// strategy deployed", which downstream reads as the active-strategy fallback.
// Nil detaches. Guarded by mu.
//
// Args:
//   - fn: the attribution source (may be nil to detach).
//
// Returns:
//   - *Fabric: the fabric for chaining.
func (f *Fabric) WithStrategyStamp(fn func() string) *Fabric {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strategyStamp = fn
	return f
}

// strategyStampID samples the attribution source once. Callers that create
// task BATCHES (CompilePlan) call it a single time so every task of the batch
// carries the same attribution even if the active strategy changes
// mid-compilation. Returns "" when no stamp source is wired.
func (f *Fabric) strategyStampID() string {
	f.mu.Lock()
	fn := f.strategyStamp
	f.mu.Unlock()
	if fn == nil {
		return ""
	}
	return fn()
}

// stampStrategyAttribution stamps strategyID onto a freshly built task's
// checkpoint envelope. Only *CheckpointEnvelope checkpoints can carry the
// attribution (the versioned protocol); other shapes are left untouched so a
// raw progress checkpoint keeps its meaning. An explicit caller-provided
// StrategyID wins — the fabric only fills an EMPTY field, so batch creators
// that sampled the stamp once (CompilePlan) keep their per-batch consistency.
// The envelope is shallow-copied before stamping: the caller's envelope is
// never mutated through the fabric's back door.
func stampStrategyAttribution(t *Task, strategyID string) {
	if strategyID == "" {
		return
	}
	switch env := t.Checkpoint.(type) {
	case nil:
		t.Checkpoint = &CheckpointEnvelope{
			SchemaVersion: CurrentCheckpointSchemaVersion,
			StrategyID:    strategyID,
		}
	case *CheckpointEnvelope:
		if env == nil || env.StrategyID != "" {
			return
		}
		cp := *env
		cp.StrategyID = strategyID
		t.Checkpoint = &cp
	}
}

// WithEventStore attaches a persistent event sink: every
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
// the scheduler's priority-preemption decision (Preempt was
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
//
// `snap := *t` only isolates the scalar fields; the reference-typed fields
// (Lease pointer, Dependencies slice) would still alias the live task, so a
// caller reading snap.Lease.ExpiresAt off-lock would race Renew/Acquire
// writing the SAME *Lease under f.mu. Both reference fields are therefore
// copied explicitly so the returned snapshot shares no mutable memory with
// the fabric. Checkpoint (any) is intentionally left aliased: it may hold
// arbitrary un-cloneable types, and the fabric only ever replaces the whole
// Checkpoint pointer (never mutates through it), so reading the old value
// off-lock stays safe.
func (f *Fabric) Task(id string) (*Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	snap := *t
	if t.Lease != nil {
		l := *t.Lease
		snap.Lease = &l
	}
	if len(t.Dependencies) > 0 {
		snap.Dependencies = append([]string(nil), t.Dependencies...)
	}
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

// TaskView is the full task row for the Tasks page: unlike
// LeaseEntry it includes terminal states, the accumulated quantum count, and
// the owner across the whole lifecycle — so the UI can render the task board
// (Ready/Running/Done) and the DAG from one source.
type TaskView struct {
	// TaskID is the fabric task identifier.
	TaskID string `json:"taskID"`
	// Capability is the required capability.
	Capability string `json:"capability"`
	// State is the current lifecycle state (including terminal).
	State TaskState `json:"state"`
	// Priority drives preemption decisions.
	Priority int `json:"priority"`
	// Owner is the current lease holder ("" when unowned/terminal).
	Owner string `json:"owner"`
	// Quantum is the total execution quanta across all holders.
	Quantum int `json:"quantum"`
	// HasCheckpoint reports whether durable progress exists.
	HasCheckpoint bool `json:"hasCheckpoint"`
	// Dependencies are the task's prerequisite IDs (copied).
	Dependencies []string `json:"dependencies"`
	// Origin is the creating agent id ("" = root/user-submitted).
	Origin string `json:"origin"`
	// CreatedAt / UpdatedAt are the lifecycle timestamps.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TaskSnapshot returns a point-in-time copy of EVERY task, including terminal
// ones, ordered by TaskID. It powers the Tasks page board + DAG
// ("see the real task dependency graph"). Terminal tasks are included so the Done column
// and the dependency closure render correctly. Purely read-only: everything
// is copied under f.mu, no write path fires.
func (f *Fabric) TaskSnapshot() []TaskView {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]TaskView, 0, len(f.tasks))
	for id, t := range f.tasks {
		v := TaskView{
			TaskID:        id,
			Capability:    t.Capability,
			State:         t.State,
			Priority:      t.Priority,
			Owner:         t.Owner,
			Quantum:       t.Quantum,
			HasCheckpoint: t.Checkpoint != nil,
			Origin:        t.Origin,
			CreatedAt:     t.CreatedAt,
			UpdatedAt:     t.UpdatedAt,
		}
		if len(t.Dependencies) > 0 {
			v.Dependencies = append([]string(nil), t.Dependencies...)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}
