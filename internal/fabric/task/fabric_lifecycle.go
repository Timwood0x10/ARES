package taskfabric

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// CreateOption configures a single Create call. No options keeps the
// permissive default documented on Create.
type CreateOption func(*createOptions)

// createOptions holds the resolved CreateOptions of one Create call.
type createOptions struct {
	// resolveDependencies requires every dependency id to name a task that is
	// already in the fabric. Off by default: a graph submitted in one call may
	// carry a forward reference (runCollabGraph creates a graph's nodes in
	// submission order, so an edge may name a node created later in the same
	// loop), and Create cannot tell that from an id that will never exist.
	resolveDependencies bool
}

// WithResolvedDependencies makes Create reject a task whose dependency ids do
// not all name live tasks — plus an empty id and a self-reference. It is the
// option for runtime graph growth: one node installed against nodes that
// already exist, with no batch closure to validate against (unlike
// CompilePlan, whose resolveDependencies checks the batch first and then the
// live index).
//
// The check runs in the SAME critical section as the insertion, so a
// concurrent Delete of a dependency cannot slip between validation and create
// and leave the edge dangling.
//
// Why it matters: depsSatisfiedLocked treats a missing dependency as
// unsatisfied forever (see ReferencedDependencies), so an accepted dangling
// edge does not fail its task — it parks the task, and every task behind it, in
// READY-forever limbo with nothing reported.
func WithResolvedDependencies() CreateOption {
	return func(o *createOptions) { o.resolveDependencies = true }
}

// Create registers a new READY task. The task is unowned and available for
// acquire. Idempotency: an existing id returns ErrTaskExists.
//
// Dependencies are copied and, by default, NOT validated: an id that never
// resolves leaves the task permanently unready and unreported. Callers that
// install a node against already-existing nodes must pass
// WithResolvedDependencies instead of relying on this default.
//
// Args:
//   - t: the task to register (ID must be non-empty).
//   - opts: optional CreateOptions (see WithResolvedDependencies).
//
// Returns:
//   - error: ErrTaskExists, an error for an empty id, or
//     ErrDependencyUnresolved (only under WithResolvedDependencies).
func (f *Fabric) Create(t *Task, opts ...CreateOption) error {
	var cfg createOptions
	for _, opt := range opts {
		// A nil option is skipped rather than invoked: this runs on the
		// scheduler's create path, and a panic there is exactly what
		// "panic is for unrecoverable initialisation only" forbids.
		if opt == nil {
			continue
		}
		opt(&cfg)
	}
	// Sample the attribution source BEFORE taking f.mu: the stamp fn talks to
	// the strategy control plane and must never run while the fabric state
	// machine is locked (deadlock avoidance), and it must be cheap.
	strategyID := f.strategyStampID()
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	if cfg.resolveDependencies {
		if err := resolvedDependenciesLocked(f.tasks, t); err != nil {
			return err
		}
	}
	return f.createLocked(t, strategyID, &pending)
}

// DependencyResolutionError reports the dependency ids of a task that could not
// be resolved: an id that names no task in the fabric, an empty id, or a
// self-reference.
//
// It carries ALL of them, not just the first, because the usual caller turns
// this into a message for a model that will retry with corrected ids — naming
// one id per attempt costs that model a full quantum each time. It satisfies
// errors.Is(ErrDependencyUnresolved), so a caller that only cares about the
// class keeps using the sentinel and never needs this type.
type DependencyResolutionError struct {
	// TaskID is the task whose dependency list was rejected.
	TaskID string
	// Missing lists the offending ids in the caller's order.
	Missing []string
}

// Error implements error.
func (e *DependencyResolutionError) Error() string {
	return fmt.Sprintf("%s: task %q has %d unresolvable dependency id(s): %v",
		ErrDependencyUnresolved.Error(), e.TaskID, len(e.Missing), e.Missing)
}

// Is reports the class of every instance: ErrDependencyUnresolved.
func (e *DependencyResolutionError) Is(target error) bool {
	return target == ErrDependencyUnresolved
}

// resolvedDependenciesLocked is resolveDependencies' single-task form: every
// dependency id must name a task already in the fabric. It reports ALL of the
// ids that fail, an empty id, or a self-reference.
//
// A self-reference is checked explicitly because it is the one cycle shape this
// path can still be handed: every other edge must point at a task that already
// exists, which makes creation order itself a topological order, so no cycle
// through a freshly created node can close.
//
// Callers must hold f.mu.
func resolvedDependenciesLocked(existing map[string]*Task, t *Task) error {
	var missing []string
	for _, dep := range t.Dependencies {
		if dep == "" || dep == t.ID {
			missing = append(missing, dep)
			continue
		}
		if _, ok := existing[dep]; !ok {
			missing = append(missing, dep)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &DependencyResolutionError{TaskID: t.ID, Missing: missing}
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
	// M4 backpressure: reject if the OUTSTANDING count would exceed the configured cap. Zero means
	// unlimited (the default). The check runs under f.mu so a concurrent Create cannot slip past
	// the cap.
	//
	// Outstanding is READY plus SUSPENDED, not READY alone: a suspended task is scheduled again by
	// the next drain (ResumableTasks), so a workload that yields between steps would otherwise
	// bypass the cap entirely. Terminal tasks (COMPLETED/FAILED) do not count — they are not
	// schedulable and will be reaped.
	if f.maxReadyTasks > 0 {
		readyCount := 0
		for _, existing := range f.tasks {
			if existing.State == StateReady || existing.State == StateSuspended {
				readyCount++
			}
		}
		if readyCount >= f.maxReadyTasks {
			return fmt.Errorf("taskfabric: ready queue full (depth %d, max %d): %w",
				readyCount, f.maxReadyTasks, ErrReadyQueueFull)
		}
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
	cp.ReadyAt = cp.CreatedAt
	// HITL SLA safety net: a RequiresApproval task created without its own
	// ApprovalTimeout inherits the fabric default. This is the single choke
	// point for EVERY create path (direct Create AND CompilePlan, which both
	// land here), so a gate can no longer be born with no SLA just because the
	// caller forgot. A per-task ApprovalTimeout (e.g. from PlanStep) is already
	// set by now and wins; a zero fabric default leaves it zero = wait forever.
	if cp.RequiresApproval && cp.ApprovalTimeout.IsZero() && f.defaultApprovalSLA > 0 {
		cp.ApprovalTimeout = cp.CreatedAt.Add(f.defaultApprovalSLA)
	}
	// Make the "no safety net" case observable instead of silent: a gate that
	// requires approval but has neither a per-task ApprovalTimeout nor a fabric
	// default will wait forever (ExpireApprovals skips a zero timeout). The net
	// is off by design (backward compatible), so this is a one-shot WARN at
	// birth — not a per-sweep gauge — to avoid log spam for a persistent state
	// while still surfacing "this gate can hang indefinitely" to operators.
	if cp.RequiresApproval && cp.ApprovalTimeout.IsZero() {
		log.Warn(context.Background(), "createLocked",
			"approval gate created without an SLA; it will wait indefinitely until a human resolves it — set a per-step interrupt timeout or kernel.approval_sla to bound it",
			"task_id", cp.ID)
	}
	// Stamp the submission-time strategy attribution onto the task's
	// checkpoint envelope (once per Create; a pre-stamped envelope wins).
	stampStrategyAttribution(&cp, strategyID)
	f.tasks[t.ID] = &cp
	*pending = append(*pending, f.recordLocked(&cp, EventTaskCreated))
	// HITL: when the task requires human approval, emit interrupt.created so
	// the approval lifecycle is observable from creation. The event is
	// observability-only — the approval POLICY is persisted via the
	// task.created payload (see fabric_events.go); this event only signals
	// "a gate exists" to bus subscribers. Default (RequiresApproval=false)
	// does not emit → non-HITL path unchanged.
	if cp.RequiresApproval {
		*pending = append(*pending, f.recordWithExtrasLocked(&cp,
			EventType(ares_events.EventInterruptCreated), map[string]any{
				"execution_id": cp.Origin,
				"step_id":      cp.ID,
				"message":      cp.ApprovalMessage,
			}))
	}
	return nil
}

// Acquire is the CAS ownership claim. Only an unowned READY (or SUSPENDED —
// checkpoint preserved, cooperative re-acquisition) task can be leased; a
// concurrent or repeated acquire is rejected, so two agents competing for the
// same task see exactly one winner.
//
// It also enforces the two time-based constraints, because it is a public grant
// point: a passed deadline (ErrTaskDeadlineExceeded) and an unserved retry
// backoff (ErrTaskNotReady) refuse the lease, closing the gap between a sweep and
// the grant. Dependencies are deliberately NOT checked here — "what may run" is
// the scheduler's question (ResumableTasks/ReadyTasks go through
// dependencySatisfied), and Acquire stays the ownership claim so callers such as
// recovery can re-acquire a task without re-litigating its DAG position.
//
// Args:
//   - id: the task id.
//   - agentID: the acquiring agent.
//   - ttl: the lease TTL.
//
// Returns:
//   - uint64: the fencing token (lease epoch) the agent must present on every
//     subsequent ownership-carrying operation.
//   - error: ErrTaskNotFound / ErrTaskNotReady / ErrTaskDeadlineExceeded.
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
	// HITL approval guard: placed after
	// deadline and backoff so those errors take precedence (D3 decision: a
	// passed deadline is more actionable than "awaiting approval"). A task
	// that requires approval but has not been approved must not be acquired —
	// this is the third gate (the first two are ReadyTasks/ResumableTasks).
	if t.RequiresApproval && t.ApprovalDecision != ApprovalDecisionApproved {
		return 0, ErrTaskAwaitingApproval
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
	// M5: clear the preemption flag on re-acquire — the task is starting a
	// fresh quantum and should not be treated as preempted.
	t.PreemptRequested = false
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
		t.ReadyAt = f.now()
		// Schedule the next attempt before recording: task.failed is the
		// must-persist carrier of NextAttemptAt (task.ready is
		// observability-only), so the restore fold recovers the due time from it
		// — a restart must not turn a pending backoff into an immediate retry.
		// A zero delay leaves NextAttemptAt zero: immediately runnable, exactly
		// as before backoff existed.
		t.NextAttemptAt = nextAttemptAt(f.now(), t.RetryPolicy.Attempts, t.BackoffBase, t.BackoffMax)
		// A retry is a continuation too (defect A6): the next attempt may land on
		// another agent, with only the checkpoint carried over. Durable via the
		// task.failed event recorded just below (its retryable flag is the fold
		// signal), unlike the observability-only task.ready that follows.
		t.Requeued = true
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
		// Not terminal: a retry is scheduled, so nothing is lost yet.
		f.logFailureVisibilityLocked(t, cause, retryable, false)
		return nil
	}
	return f.failTerminalLocked(t, cause, retryable, &pending)
}

// logFailureVisibilityLocked writes the escalation line when an attempt ended in a
// way an operator should know about (see failureVisibilityReason).
//
// Caller must hold f.mu, matching the other *_locked log sites in this package: the
// line names the attempt count and the classification that produced it, so it has to
// be read from the same state the transition just used.
func (f *Fabric) logFailureVisibilityLocked(t *Task, cause error, retryable, terminal bool) {
	reason := failureVisibilityReason(failureContext{
		Attempts:   t.RetryPolicy.Attempts,
		MaxRetries: t.RetryPolicy.MaxRetries,
		Retryable:  retryable,
		Terminal:   terminal,
		ReplyTask:  f.carriesCollaborationReply(t),
	})
	if reason == "" {
		return
	}
	log.Warn(context.Background(), "logFailureVisibilityLocked",
		"taskfabric: "+reason,
		"task", t.ID,
		"attempts", t.RetryPolicy.Attempts,
		"max_retries", t.RetryPolicy.MaxRetries,
		"retryable", retryable,
		"terminal", terminal,
		"cause", failureCauseText(cause))
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
	t.Checkpoint = f.checkpointWithCause(t.Checkpoint, cause)
	*pending = append(*pending, f.failedEventLocked(t, retryable))
	// Terminal failures are shared by Fail and ExpireDeadlines, so the escalation
	// belongs here rather than at either call site: a budget that ran out on an
	// unclassified cause must be visible whichever sweep ended the task.
	f.logFailureVisibilityLocked(t, cause, retryable, true)
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

// payloadKeyReleaseReason is the task.released payload key carrying WHY the
// holder stepped back (an agentfabric.StopReason code such as "budget_reserved"
// or "capability_unfit"). It is an event-only annotation: the release fold only
// needs the state transition, so there is deliberately no restore key. Without
// it a reclaim was indistinguishable from an ordinary release — the exact gap
// that made a budget-starved agent and a missing executor look identical.
const payloadKeyReleaseReason = "reason"

// Release returns a LEASED/RUNNING/SUSPENDED task to READY, clearing owner
// and lease so another agent can acquire it. The epoch fencing guarantees a
// stale holder (whose lease expired and was re-acquired by another agent)
// cannot release the task out from under the new owner.
//
// It is ReleaseReason with no attributed cause. There is exactly one
// implementation of the transition, so plain release and reclaim can never
// drift apart.
func (f *Fabric) Release(id, agentID string, epoch uint64) error {
	return f.ReleaseReason(id, agentID, epoch, "")
}

// ReleaseReason returns a task to READY exactly as Release does, and records
// the agentfabric.StopReason that caused it on the task.released event. The
// scheduler's reclaim path calls it when the holder can no longer serve the
// task (budget reserve/exhaustion, deadline, no executable body): the task
// keeps its checkpoint and the reason, so "why did this task go back to the
// queue" is answerable from the event log alone instead of being inferred from
// a throttled scheduler log line.
//
// reason == "" keeps the payload byte-identical to a plain Release, so
// unattributed callers cost nothing.
func (f *Fabric) ReleaseReason(id, agentID string, epoch uint64, reason string) error {
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
	t.ReadyAt = f.now()
	// Record the released event while the releasing agent is still
	// attached (provenance), then clear ownership so the task is unowned.
	if reason == "" {
		pending = append(pending, f.recordLocked(t, EventTaskReleased))
	} else {
		pending = append(pending, f.recordWithExtrasLocked(t, EventTaskReleased, map[string]any{
			payloadKeyReleaseReason: reason,
		}))
	}
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
		t.ReadyAt = now
		// The work is now continued rather than started (defect A6): the task
		// keeps its checkpoint but lost its owner, so whoever acquires it next
		// resumes someone else's progress. The task.expired event below is what
		// makes this durable — restore folds the flag straight back from it.
		t.Requeued = true
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

// ApprovalDecisionApproved and ApprovalDecisionRejected are the two allowed
// decision values for ResolveApproval. They are the string forms stored in
// Task.ApprovalDecision.
//
// Approval-gate sub-state transition table (Task.ApprovalDecision), only
// meaningful while Task.RequiresApproval is true:
//
//	decision   → allowed transitions
//	────────   ──────────────────────────────────────────────
//	""          (pending) → "approved" (gate opens, task stays READY)
//	""          (pending) → "rejected" (task driven READY→FAILED + cascade)
//	"approved"  (terminal for the gate; task runs normally)
//	"rejected"  (terminal for the gate; task is FAILED)
//
// The gate never reopens: once non-empty, the only legal re-resolve is the
// identical decision (idempotent no-op). A different decision after one is
// set returns ErrApprovalAlreadyResolved.
const (
	ApprovalDecisionApproved = "approved"
	ApprovalDecisionRejected = "rejected"
)

// payloadKeyApproved is the EventInterruptResolved payload key carrying the
// boolean approval outcome (true = approved, false = rejected).
const payloadKeyApproved = "approved"

// payloadKeyFeedback is the EventInterruptResolved payload key carrying the
// human feedback text (reject reason / approve note / auto-reject message).
const payloadKeyFeedback = "feedback"

// ResolveApproval is the human-in-the-loop entry point for the HITL approval
// gate. It does NOT require ownership — like ExpireDeadlines, it is an
// out-of-band decision that operates under f.mu directly.
//
// approve: the task stays READY (no state change) and becomes schedulable
// (approvalCleared now returns true). reject: the task is driven to FAILED
// via failTerminalLocked (READY→FAILED is a legal transition) with the usual
// cascade. The mutation + event ordering lives in resolveApprovalLocked, the
// single source shared with ResolveApprovalWithBlastRadius.
//
// Idempotency: resolving with the same decision is a no-op. A different
// decision after one is already set, or a task that is already terminal,
// returns ErrApprovalAlreadyResolved.
//
// Args:
//   - id: the task id.
//   - decision: "approved" or "rejected".
//   - feedback: optional human feedback text (reject reason / approve note).
//   - by: the principal identifier of the approver (for audit).
//
// Returns:
//   - error: ErrTaskNotFound / ErrApprovalNotRequired /
//     ErrInvalidApprovalDecision / ErrApprovalAlreadyResolved.
func (f *Fabric) ResolveApproval(id, decision, feedback, by string) error {
	pending := make([]*pendingAppend, 0, 2)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, proceed, err := f.resolveApprovalPreflightLocked(id, decision)
	if err != nil || !proceed {
		return err
	}
	return f.resolveApprovalLocked(t, decision, feedback, by, &pending)
}

// resolveApprovalPreflightLocked validates that task id may be resolved with
// decision. Caller must hold f.mu. It returns (t, proceed, err): proceed is
// false with a nil error when the call is an idempotent no-op (the same
// decision is already recorded), so both public wrappers short-circuit
// without duplicating the guard logic (and thus cannot drift apart).
func (f *Fabric) resolveApprovalPreflightLocked(id, decision string) (*Task, bool, error) {
	// Decision validation lives HERE (not in the public wrappers) so both
	// ResolveApproval and ResolveApprovalWithBlastRadius are guaranteed to
	// reject an invalid decision identically — the single-guard promise this
	// helper exists for. A wrapper that validated on its own could drift.
	if decision != ApprovalDecisionApproved && decision != ApprovalDecisionRejected {
		return nil, false, ErrInvalidApprovalDecision
	}
	t, ok := f.tasks[id]
	if !ok {
		return nil, false, ErrTaskNotFound
	}
	if !t.RequiresApproval {
		return nil, false, ErrApprovalNotRequired
	}
	// Idempotency: same decision → no-op.
	if t.ApprovalDecision == decision {
		return t, false, nil
	}
	// Already resolved with a different decision, or task is terminal.
	if t.ApprovalDecision != "" || t.State == StateCompleted || t.State == StateFailed {
		return nil, false, ErrApprovalAlreadyResolved
	}
	return t, true, nil
}

// resolveApprovalLocked applies a validated, non-no-op approval decision to
// task t and records the lifecycle events. Caller must hold f.mu and must
// have passed resolveApprovalPreflightLocked. It is the single source of
// truth for the approve/reject mutation so ResolveApproval and
// ResolveApprovalWithBlastRadius can never drift apart.
//
// Ordering (reject): failTerminalLocked runs FIRST; only on its success are
// the approval fields stamped and interrupt.resolved recorded, so a failed
// transition never leaves a half-mutated task with a durable event that
// contradicts its actual state. Approve stamps the fields, records
// interrupt.resolved, then emits task.ready so a scheduler subscribed to
// lifecycle events drains immediately instead of waiting up to one sweep.
func (f *Fabric) resolveApprovalLocked(t *Task, decision, feedback, by string, pending *[]*pendingAppend) error {
	// Stamp the approval fields BEFORE the terminal transition so the
	// authoritative task.failed event (recorded inside failTerminalLocked
	// through the must-persist payload) carries WHO rejected and WHY — the
	// durable audit chain, not only the interrupt.resolved event. The
	// "only a pending READY gate is resolvable" preflight guard makes
	// READY→FAILED legal, so the rollback is a safety net that should never
	// run; if the transition ever did fail, the fields are restored so the
	// task is never left half-mutated (§3.6 state consistency).
	prevDecision, prevFeedback := t.ApprovalDecision, t.ApprovalFeedback
	prevBy, prevAt := t.ApprovalBy, t.ApprovalAt
	t.ApprovalDecision = decision
	t.ApprovalFeedback = feedback
	t.ApprovalBy = by
	t.ApprovalAt = f.now()
	if decision == ApprovalDecisionRejected {
		if err := f.failTerminalLocked(t, ErrInterruptRejected, false, pending); err != nil {
			t.ApprovalDecision, t.ApprovalFeedback = prevDecision, prevFeedback
			t.ApprovalBy, t.ApprovalAt = prevBy, prevAt
			return err
		}
	}
	extras := map[string]any{payloadKeyApproved: decision == ApprovalDecisionApproved}
	if decision == ApprovalDecisionRejected {
		extras[payloadKeyFeedback] = feedback
	}
	*pending = append(*pending, f.recordWithExtrasLocked(t,
		EventType(ares_events.EventInterruptResolved), extras))
	if decision == ApprovalDecisionApproved {
		// task.ready signals "newly schedulable" without a state change so a
		// scheduler subscribed to lifecycle events drains without a sweep
		// delay. Observability-only (never folded by restore).
		*pending = append(*pending, f.recordLocked(t, EventTaskReady))
	}
	return nil
}

// ApprovalView is the read-only snapshot of one pending approval gate,
// returned by PendingApprovals for the HTTP transport layer.
type ApprovalView struct {
	TaskID     string         `json:"task_id"`
	Capability string         `json:"capability"`
	Message    string         `json:"message"`
	Payload    map[string]any `json:"payload,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	// ApprovalTimeout is the absolute deadline for a human decision. Zero
	// when no SLA is set.
	ApprovalTimeout time.Time `json:"approval_timeout,omitempty"`
}

// PendingApprovals returns a snapshot of every task that requires approval and
// has not yet been resolved (ApprovalDecision == ""). Ordered by TaskID for
// stable rendering. This is the query the HTTP GET /api/interrupts handler
// consumes.
func (f *Fabric) PendingApprovals() []ApprovalView {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ApprovalView
	for _, t := range f.tasks {
		if !t.RequiresApproval || t.ApprovalDecision != "" {
			continue
		}
		out = append(out, ApprovalView{
			TaskID:          t.ID,
			Capability:      t.Capability,
			Message:         t.ApprovalMessage,
			Payload:         t.ApprovalPayload,
			CreatedAt:       t.CreatedAt,
			ApprovalTimeout: t.ApprovalTimeout,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// BlastRadius is the downstream impact preview for a potential reject of a
// pending approval gate. It tells the approver how many tasks will be
// cascade-failed if they reject, broken down by strict vs. AllowPartial
// dependents (R3: blast radius transparency).
type BlastRadius struct {
	// CascadedFail is the count of strict downstream tasks that would be
	// driven to FAILED by cascadeFailureLocked.
	CascadedFail int `json:"cascaded_fail"`
	// AllowPartialDegraded is the count of AllowPartial downstream tasks
	// that would survive (recorded as degraded inputs, not failed).
	AllowPartialDegraded int `json:"allow_partial_degraded"`
}

// ApprovalBlastRadius computes the downstream impact of rejecting the
// approval gate on task id. It walks the transitive READY dependents the
// same way cascadeFailureLocked would, classifying each as strict (would
// be cascaded to FAILED) or AllowPartial (would survive with a degraded
// input). The caller (HTTP resolve handler) surfaces this to the approver
// BEFORE the reject decision so they know the blast radius.
//
// Returns the zero value for a task that does not require approval or is
// not pending (the blast radius is only meaningful before resolve).
func (f *Fabric) ApprovalBlastRadius(id string) BlastRadius {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok || !t.RequiresApproval || t.ApprovalDecision != "" {
		return BlastRadius{}
	}
	return f.blastRadiusLocked(id)
}

// ResolveApprovalWithBlastRadius resolves an approval gate and returns the
// blast radius that was computed atomically under the SAME lock as the
// resolve. This eliminates the TOCTOU window between a separate
// ApprovalBlastRadius call and a ResolveApproval call: the blast radius
// reported to the approver is exactly what the reject cascaded.
//
// For approve the blast radius is always zero (no cascade). For reject it
// reflects the dependents that were cascaded to FAILED by this very call.
// Returns ErrApprovalAlreadyResolved (with a zero BlastRadius) when the
// task is already resolved, so the caller can distinguish a no-op from a
// real resolve.
func (f *Fabric) ResolveApprovalWithBlastRadius(id, decision, feedback, by string) (BlastRadius, error) {
	pending := make([]*pendingAppend, 0, 2)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, proceed, err := f.resolveApprovalPreflightLocked(id, decision)
	if err != nil || !proceed {
		return BlastRadius{}, err
	}
	// Compute the blast radius BEFORE the mutation, under the same lock: it
	// is the exact set of dependents a reject will cascade, so the number
	// reported to the approver matches what actually cascades (no TOCTOU).
	var br BlastRadius
	if decision == ApprovalDecisionRejected {
		br = f.blastRadiusLocked(id)
	}
	if err := f.resolveApprovalLocked(t, decision, feedback, by, &pending); err != nil {
		return BlastRadius{}, err
	}
	return br, nil
}

// blastRadiusLocked computes the blast radius for task id. Caller must hold
// f.mu. It mirrors cascadeFailureLocked's traversal but is read-only: it
// counts dependents instead of failing them.
func (f *Fabric) blastRadiusLocked(rootID string) BlastRadius {
	dependents := make(map[string][]string, len(f.tasks))
	for taskID, t := range f.tasks {
		for _, dep := range t.Dependencies {
			dependents[dep] = append(dependents[dep], taskID)
		}
	}
	var br BlastRadius
	queue := []string{rootID}
	visited := make(map[string]bool)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, taskID := range dependents[cur] {
			if visited[taskID] {
				continue
			}
			visited[taskID] = true
			t := f.tasks[taskID]
			if t == nil || t.State != StateReady {
				continue
			}
			if t.AllowPartial {
				br.AllowPartialDegraded++
				// Do not traverse past an AllowPartial dependent: it
				// survives, so its own dependents are not cascaded.
				continue
			}
			br.CascadedFail++
			queue = append(queue, taskID)
		}
	}
	return br
}

// approvalTimeoutFeedback is the single source of truth for the timeout
// rejection text. Used in the ApprovalFeedback field, the interrupt.resolved
// event payload, and matches ErrApprovalExpired's message. Keeping it in one
// place prevents the three from drifting apart.
const approvalTimeoutFeedback = "approval gate timed out"

// approvalSLAPrincipal is the ApprovalBy value stamped by the auto-reject
// sweep so an audit trail can distinguish system SLA expiry from a human
// reject.
const approvalSLAPrincipal = "system:approval-sla"

// ExpireApprovals auto-rejects every pending approval gate whose
// ApprovalTimeout has passed. It is the approval-SLA enforcement mechanism
// (R1): a task whose gate has been open too long is driven to FAILED via
// failTerminalLocked (READY→FAILED is a legal transition), with the same
// cascade semantics as a manual reject. Returns the ids of every expired
// task so the recovery sweep can report what it auto-rejected.
//
// A task with a zero ApprovalTimeout is never expired by this sweep — its
// gate stays open until a human resolves it. Only pending tasks
// (ApprovalDecision == "" and still in StateReady) are eligible. The
// approval gate blocks Acquire, so a RequiresApproval task that has not
// been approved can never leave READY — listing other states (LEASED/
// RUNNING/SUSPENDED) would be a latent bug if the gate were ever bypassed,
// because the state machine does not allow SUSPENDED→FAILED and
// failTerminalLocked would return error after the task fields had already
// been mutated, leaving a zombie (decision set, event persisted, but not
// FAILED). Only StateReady is processed so the transition is always legal.
//
// Ordering: failTerminalLocked runs FIRST. Only on its success are the
// approval fields set and the interrupt.resolved event recorded — so a
// failed transition never leaves the task in a half-mutated state.
//
// Iteration safety: failTerminalLocked calls cascadeFailureLocked which
// mutates sibling tasks' State (transition to FAILED) but does NOT add or
// remove keys from f.tasks, so the range over f.tasks is safe under Go's
// map iteration semantics. If cascadeFailureLocked ever gains the ability
// to create tasks, the range must be converted to a pre-snapshot of keys.
func (f *Fabric) ExpireApprovals() []string {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	now := f.now()
	var expired []string
	for _, t := range f.tasks {
		if !t.RequiresApproval || t.ApprovalDecision != "" {
			continue
		}
		if t.ApprovalTimeout.IsZero() || !now.After(t.ApprovalTimeout) {
			continue
		}
		// Only StateReady is eligible: the approval gate blocks Acquire, so
		// a pending approval task cannot reach LEASED/RUNNING/SUSPENDED.
		// Limiting it to StateReady keeps READY→FAILED legal, so the stamp
		// below is never rolled back in practice.
		if t.State != StateReady {
			continue
		}
		// Stamp the auto-reject fields BEFORE the terminal transition so the
		// authoritative task.failed event carries WHO (system SLA) and WHY —
		// the same durable audit chain as a manual reject. On the (guarded-
		// impossible) transition failure the fields are rolled back so the
		// task is never left half-mutated.
		t.ApprovalDecision = ApprovalDecisionRejected
		t.ApprovalFeedback = approvalTimeoutFeedback
		t.ApprovalBy = approvalSLAPrincipal
		t.ApprovalAt = now
		if err := f.failTerminalLocked(t, ErrApprovalExpired, false, &pending); err != nil {
			t.ApprovalDecision, t.ApprovalFeedback = "", ""
			t.ApprovalBy, t.ApprovalAt = "", time.Time{}
			continue
		}
		// Transition succeeded: record the interrupt.resolved event AFTER
		// task.failed so the durable log order is failed → resolved.
		pending = append(pending, f.recordWithExtrasLocked(t,
			EventType(ares_events.EventInterruptResolved), map[string]any{
				payloadKeyApproved: false,
				payloadKeyFeedback: approvalTimeoutFeedback,
				"auto":             true,
			}))
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
