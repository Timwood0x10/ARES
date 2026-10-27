package taskfabric

import "time"

// Task is the durable-intent object (docs/zh/architecture/ares-runtime.md).
// Agents are disposable; a Task survives its owner via lease expiry and
// preserved checkpoints.
type Task struct {
	// ID is the stable task identifier.
	ID string
	// Capability is the required capability (e.g. "rust/unsafe-analysis");
	// the capability-aware scheduler scores agents against it.
	Capability string
	// State is the current lifecycle state.
	State TaskState
	// Priority drives preemption decisions (higher wins).
	Priority int
	// Owner is the current lease holder ("" when unowned).
	Owner string
	// Lease is the current TaskLease (nil when unowned).
	Lease *Lease
	// Checkpoint is durable progress preserved across preemption/requeue.
	Checkpoint any
	// Dependencies are prerequisite task IDs; is_ready = all completed.
	Dependencies []string
	// Deadline is the latest acceptable completion time. It is enforced by the
	// fabric, not advisory: ExpireDeadlines fails any live task past it
	// (terminal, retry budget unspent), and Acquire refuses to grant execution
	// rights to a task whose deadline already passed. Zero means "no deadline".
	Deadline time.Time
	// RetryPolicy carries the retry budget.
	RetryPolicy RetryPolicy
	// BackoffBase is the delay before the first retry; every further retry
	// doubles it, capped by BackoffMax. Zero disables backoff, which keeps a
	// requeued task immediately runnable — the pre-0.3.3 behaviour, so an
	// unconfigured task is unaffected by this field existing.
	BackoffBase time.Duration
	// BackoffMax caps the doubling of BackoffBase. Zero means "no cap".
	BackoffMax time.Duration
	// NextAttemptAt is the earliest instant this task may be granted execution
	// rights again after a requeue under backoff. Zero means "no delay". It is
	// scheduling state rather than policy, and is persisted with the retry
	// budget so a restart cannot make a task retry earlier than the running
	// fabric intended.
	NextAttemptAt time.Time
	// Origin is the agent ID that created the task ("" = root: user-submitted
	// or system-bootstrapped, no agent caller). It is Kernel-validated: the
	// create_task syscall stamps the caller from the tool context
	// (kernel.CallerID), never from LLM-supplied arguments, so provenance
	// such as "B.origin = A" is auditable end-to-end.
	Origin string
	// FailedDependency names the prerequisite task whose terminal FAILED
	// cascaded into this one ("" = this task failed on its own, or is not
	// failed). It exists because a FAILED prerequisite satisfies no dependency
	// unless the dependent explicitly opted in (see AllowPartial): without
	// cascading, the whole downstream subgraph would sit READY forever —
	// unschedulable, invisible to the reaper and a permanent "round still
	// active" for PlanLoop. Provenance is recorded so operators can tell a
	// subgraph that died of a root cause from one that executed and failed.
	// Guarded by f.mu like every other Task field.
	FailedDependency string
	// Requeued reports that this task was returned to the pool after being owned
	// at least once: a lease expiry (its owner died) or a retry requeue. It is the
	// durability half of defect A6's question — is this the task's first run, or
	// was work continued?
	//
	// It is a fact about the TASK and deliberately not an identity claim: the
	// fabric does not persist who the previous owner was (the executing-agent
	// stamp is quantum-scoped by design, see withExecutingAgent), so
	// Requeued=true does NOT prove a DIFFERENT agent took over. Read it together
	// with the finisher's own revive bit: requeued + a finisher that is not
	// revived = work continued on a blank slate.
	//
	// Guarded by f.mu like every other Task field.
	Requeued bool
	// AllowPartial is the creation-time policy that lets this task run when a
	// dependency fails permanently, instead of being failed by the cascade. The
	// gap is not hidden: the failed predecessor's ID is recorded in
	// DegradedInputs and mirrored into the task's payload as degraded_inputs, so
	// "not checked" stays distinguishable from "checked, nothing found". False
	// (the zero value) is strict: a failed dependency fails this task too.
	AllowPartial bool
	// DegradedInputs lists the predecessors this task proceeds without. It is
	// runtime state, filled the moment a cascade reaches an AllowPartial
	// dependent, and it is also what unlocks scheduling: only IDs recorded here
	// satisfy dependencySatisfied, so an AllowPartial task whose OTHER
	// dependency is still running keeps waiting. Guarded by f.mu.
	DegradedInputs []string
	// Quantum counts how many execution quanta (agent steps) this task has
	// run across ALL lease holders (accumulated across yield→resume cycles,
	// preemptions and chaos-recovery replacements). It is the "semantic step"
	// number the observability UI shows as Quantum #N (
	// "Agent is executing semantic quantum #18"). Guarded by f.mu like every
	// other Task field.
	Quantum int
	// CreatedAt is when the task entered the fabric; UpdatedAt is the last
	// state/lease mutation (both injectable-clock aware). Used by the panel
	// for age/recency display.
	CreatedAt time.Time
	// UpdatedAt is the last state transition or quantum boundary time.
	UpdatedAt time.Time
	// ReadyAt is the instant this task last entered StateReady. It is set
	// on Create and on every transition INTO READY (requeue, preempt,
	// release, lease-expiry), and cleared when the task leaves READY
	// (Acquire). It serves two purposes:
	//   - M0: the scheduler records ready_to_dispatch and ready_age
	//     observability metrics from it;
	//   - M1: ReadyTasks/ResumableTasks sort by (priority DESC, ReadyAt ASC,
	//     id ASC) for deterministic dispatch order.
	// Guarded by f.mu like every other Task field.
	ReadyAt time.Time
	// AttemptReadyAt is when THIS attempt became eligible to run: creation, a retry after failure,
	// a release or lease expiry, a preemption, or the yield that ended the previous quantum.
	//
	// It exists because ReadyAt answers a different question. ReadyAt is the task's age (fairness
	// and aging depend on it, so it must NOT be reset when a task yields); AttemptReadyAt is the
	// queueing delay of one attempt. Measuring dispatch latency from ReadyAt charged a resumed task
	// for the time its previous quantum spent executing — a 150ms quantum followed by an immediate
	// resume reported 150ms of "waiting" for a quantum that waited 0.4ms.
	AttemptReadyAt time.Time
	// PreemptRequested is set by Preempt to signal that a cooperative
	// preemption was requested at a quantum boundary (M5). A multi-step
	// quantum checks this between steps: when set, the quantum stops
	// dispatching further steps and hands the task back to READY with its
	// checkpoint preserved. The in-flight LLM call is NOT cancelled (see
	// PLAN-0.3.6-scheduling §5.1) — it completes naturally and its result
	// is discarded by the fencing token. Guarded by f.mu.
	PreemptRequested bool

	// --- HITL approval gate ---
	//
	// RequiresApproval is the creation-time policy: when true the task enters
	// READY but is blocked by an approval gate — it must not be scheduled or
	// acquired until a human resolves the approval. False (the zero value) is
	// the default: no gate, pre-HITL behaviour, byte-for-byte compatible.
	RequiresApproval bool
	// ApprovalMessage is the human-facing prompt shown to the approver (from
	// InterruptConfig.Message). Empty when RequiresApproval is false.
	ApprovalMessage string
	// ApprovalPayload carries optional context for the approver (from
	// InterruptConfig.Payload). Nil when RequiresApproval is false.
	ApprovalPayload map[string]any
	// ApprovalDecision is the runtime state of the gate:
	//   ""         — pending (not yet resolved)
	//   "approved" — gate opened, task is schedulable
	//   "rejected" — gate closed, task is driven to FAILED
	ApprovalDecision string
	// ApprovalFeedback is the human's feedback text (reject reason / approve
	// note). Empty until resolved.
	ApprovalFeedback string
	// ApprovalBy is the principal identifier of the person who resolved the
	// gate, for audit. Empty until resolved.
	ApprovalBy string
	// ApprovalAt is when the gate was resolved. Zero until resolved.
	ApprovalAt time.Time
	// ApprovalTimeout is the absolute deadline for a human to resolve the
	// approval gate. When non-zero and the fabric clock passes it,
	// ExpireApprovals auto-rejects the task so a pending gate cannot stall
	// forever (R1: approval SLA). Zero means "no SLA" — the gate stays open
	// until a human resolves it, preserving pre-SLA behaviour for callers
	// that do not set it.
	ApprovalTimeout time.Time
}

// RetryPolicy bounds re-queueing after failures.
type RetryPolicy struct {
	// MaxRetries is the TOTAL attempt budget: the first try counts, so
	// MaxRetries=2 allows exactly one requeue after the first failure
	// (CanRetry: Attempts < MaxRetries, checked after Attempts is
	// incremented by Fail). 0 — and any negative value — means no retries:
	// the first Fail transitions the task straight to terminal FAILED.
	MaxRetries int
	// Attempts counts executions so far.
	Attempts int
}

// CanRetry reports whether another attempt is allowed.
func (t *Task) CanRetry() bool {
	return t.RetryPolicy.Attempts < t.RetryPolicy.MaxRetries
}

// transition moves the task to a new state, rejecting illegal transitions
// (see canTransition in state.go).
func (t *Task) transition(to TaskState) error {
	if !canTransition(t.State, to) {
		return ErrIllegalState
	}
	t.State = to
	return nil
}
