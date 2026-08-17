package taskfabric

import "time"

// Task is the durable-intent object (design §3 of docs/zh/architecture/ares-runtime.md).
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
	// Deadline is the latest acceptable completion time.
	Deadline time.Time
	// RetryPolicy carries the retry budget.
	RetryPolicy RetryPolicy
}

// RetryPolicy bounds re-queueing after failures.
type RetryPolicy struct {
	// MaxRetries is the total attempts allowed (0 = no retries).
	MaxRetries int
	// Attempts counts executions so far.
	Attempts int
}

// CanRetry reports whether another attempt is allowed.
func (t *Task) CanRetry() bool {
	return t.RetryPolicy.MaxRetries <= 0 || t.RetryPolicy.Attempts < t.RetryPolicy.MaxRetries
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
