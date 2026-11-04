package taskfabric

import (
	"context"
	"errors"
	"fmt"
)

// CommitOutcome is what the fabric KNOWS about the durability of a state change.
//
// Three states, because "the append returned an error" and "the append did not happen" are not the
// same statement. A write that timed out may have landed; a write the store refused did not. Treating
// both as failure re-runs work that already happened; treating both as success loses it.
type CommitOutcome int

const (
	// CommitSkipped means no store is configured. The change exists only in memory: the existing
	// behaviour of an in-memory fabric, with no cross-process recovery claim.
	CommitSkipped CommitOutcome = iota
	// CommitCommitted means the store acknowledged the append.
	CommitCommitted
	// CommitNotCommitted means the store refused it. The event is definitely absent, so the caller
	// may report failure and retry later with a new event.
	CommitNotCommitted
	// CommitUnknown means the append's fate is undetermined (deadline, cancellation, transport
	// error). NOT equivalent to "not committed": a request that timed out may still be applied by
	// the store, and a single read that finds nothing does not prove that an in-flight append will
	// not land afterwards.
	CommitUnknown
)

// String implements fmt.Stringer so the state appears verbatim in logs and errors.
func (o CommitOutcome) String() string {
	switch o {
	case CommitSkipped:
		return "skipped"
	case CommitCommitted:
		return "committed"
	case CommitNotCommitted:
		return "not_committed"
	case CommitUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("CommitOutcome(%d)", int(o))
	}
}

// ErrCommitUnknown reports a state change whose durability could not be established.
//
// Callers must treat it as "do not retry blindly, do not report success": the event may or may not
// exist. Reconciliation goes by the stable event id, never by re-generating a new event for the same
// transition — that is how a duplicate execution is born.
var ErrCommitUnknown = errors.New("taskfabric: commit outcome unknown")

// ErrCommitRefused reports a state change the store definitely refused.
//
// Distinct from ErrCommitUnknown so callers can act on the difference: a refusal is safe to retry
// with a new event, an unknown is not. Both are storage outcomes, not execution failures — a caller
// that counts them as one pollutes whatever it uses to judge the executor.
var ErrCommitRefused = errors.New("taskfabric: commit refused")

// worse returns the more severe of two outcomes, so a batch reports its least reassuring member.
//
// Precedence: unknown outranks not-committed, which outranks committed, which outranks skipped. A
// batch is only "committed" if every member is.
func worse(a, b CommitOutcome) CommitOutcome {
	rank := func(o CommitOutcome) int {
		switch o {
		case CommitUnknown:
			return 3
		case CommitNotCommitted:
			return 2
		case CommitCommitted:
			return 1
		default:
			return 0
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// commitError turns an outcome into the error a caller should see.
//
// The message names both the task and what is known, because these errors end up in exactly one log
// line during an incident.
func commitError(outcome CommitOutcome, taskID string) error {
	switch outcome {
	case CommitUnknown:
		return fmt.Errorf("%w: task %q may or may not be durable; reconcile by event id before retrying", ErrCommitUnknown, taskID)
	case CommitNotCommitted:
		return fmt.Errorf("%w: task %q was not persisted; the change was refused by the store", ErrCommitRefused, taskID)
	default:
		return nil
	}
}

// classifyAppendError maps a store error onto the three states.
//
// Deadline and cancellation are UNKNOWN, not failed: the request reached the store and the store may
// have applied it. Everything else is a refusal, which is safe to report and retry.
func classifyAppendError(err error) CommitOutcome {
	if err == nil {
		return CommitCommitted
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return CommitUnknown
	}
	return CommitNotCommitted
}

// PartialCommitError reports a batch whose members did not all reach the same fate.
//
// It exists because a batch is not a single write: reporting "the plan was not committed" when three
// of five task.created events landed is as wrong as reporting success, and it is the more dangerous
// of the two — the caller retries the whole plan and creates duplicates of the ones that did commit.
type PartialCommitError struct {
	// Committed lists the ids whose events are durable (or were skipped because no store is wired).
	Committed []string
	// Refused lists the ids the store definitely rejected: they are absent and may be retried.
	Refused []string
	// Partial lists the ids whose own events disagree: some durable, some refused. Neither state, and
	// the one that must never be rolled back — the durable half exists in the store.
	Partial []string
	// Unknown lists the ids whose fate could not be established. Not retryable: reconcile by event id.
	Unknown []string
}

// Error implements error, naming all three groups.
func (e *PartialCommitError) Error() string {
	return fmt.Sprintf("%s: plan partially committed — committed=%v refused=%v partial=%v unknown=%v",
		ErrPartialCommit.Error(), e.Committed, e.Refused, e.Partial, e.Unknown)
}

// Is reports the class of every instance: ErrPartialCommit.
func (e *PartialCommitError) Is(target error) bool { return target == ErrPartialCommit }

// ErrPartialCommit is the class of a batch that ended in more than one state.
var ErrPartialCommit = errors.New("taskfabric: batch partially committed")

// ErrBatchNotCommitted is the class of a batch where nothing reached the store.
var ErrBatchNotCommitted = errors.New("taskfabric: batch not committed")

// classifyBatch groups per-append results into the three buckets the contract distinguishes.
func classifyBatch(results []appendResult) (committed, refused, partial, unknown []string) {
	// Per task, not per event: one transition can emit several events, and a task whose created event
	// landed but whose observational interrupt.created did not is committed, full stop.
	type tally struct {
		committed int
		refused   int
		unknown   bool
	}
	perTask := make(map[string]*tally, len(results))
	order := make([]string, 0, len(results))
	for _, r := range results {
		// Observability-only events never decide durability. Their failure is worth a log line, not a
		// verdict on whether the task exists.
		if !isMustPersistEvent(r.Type) {
			continue
		}
		entry, seen := perTask[r.TaskID]
		if !seen {
			entry = &tally{}
			perTask[r.TaskID] = entry
			order = append(order, r.TaskID)
		}
		switch r.Outcome {
		case CommitCommitted, CommitSkipped:
			entry.committed++
		case CommitNotCommitted:
			entry.refused++
		default:
			entry.unknown = true
		}
	}
	for _, id := range order {
		landed, refusedCount := perTask[id].committed, perTask[id].refused
		switch {
		case perTask[id].unknown:
			unknown = append(unknown, id)
		case refusedCount == 0:
			committed = append(committed, id)
		case landed == 0:
			refused = append(refused, id)
		default:
			// Some of this task's own events landed and some did not. Collapsing that into "refused"
			// was wrong in the dangerous direction: the caller then rolled the task back in memory
			// while the store held its task.created, and a restart resurrected it as READY. It is
			// neither state, so it gets its own.
			partial = append(partial, id)
		}
	}
	return committed, refused, partial, unknown
}

// worstMustPersist reports what is known about a change's AUTHORITATIVE events, ignoring the
// observational ones.
//
// The distinction decides whether a task may run. A retry emits task.failed (authoritative: it says
// the attempt happened and schedules the next one) and task.ready (observability). If only the latter
// failed, the retry is recorded and the task must stay runnable — gating on the aggregate outcome
// stranded a retry whose authoritative record had committed.
func worstMustPersist(results []appendResult) CommitOutcome {
	out := CommitSkipped
	for _, r := range results {
		if !isMustPersistEvent(r.Type) {
			continue
		}
		out = worse(out, r.Outcome)
	}
	return out
}
