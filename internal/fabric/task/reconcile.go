package taskfabric

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// PendingReconciliation is one append whose fate the fabric could not establish.
//
// It exists because "unknown" is not a state a process can act on: it needs a NAME to ask the store
// about later. The record carries the event id, the task state the event described, and the outcome
// the append reported, so a reconciliation can be performed by something that was not present when
// the append happened (a restart, an operator, a recovery sweep).
type PendingReconciliation struct {
	// EventID is the id assigned before the append — the thing to query the store for.
	EventID string
	// TaskID and Type say what the event claimed, so a reconciliation can decide what settling it
	// means for the task.
	TaskID string
	Type   EventType
	// Generation is the task's transition counter when the event was recorded. Settling against a
	// task that has moved on would apply an outcome to a state that no longer exists.
	Generation uint64
	// Outcome is what the append reported: Refused or Unknown. Both are unsettled until the store
	// answers — a refusal is only certain for the store that produced it.
	Outcome CommitOutcome
	// At is when the append was attempted, so the ledger can be ordered and aged.
	At time.Time
	// Snapshot is the task as the event described it. Settling a completion without it restored the
	// state but not the result: consumers got a COMPLETED task with the pre-quantum checkpoint.
	Snapshot *Task
}

// TODO(persistence): this ledger is in memory only, so an unknown outcome is unrecoverable across a
// restart — the process that recorded it is the only one that can settle it. Persisting it (or
// deriving it from the store: created without completed) is the follow-up; until then the honest
// statement is "reconcilable within one process lifetime", not "reconcilable".
//
// ErrReconcileUnknownEvent reports a reconciliation for an id the fabric is not waiting on.
var ErrReconcileUnknownEvent = errors.New("taskfabric: no pending reconciliation for event")

// ErrReconcileInconclusive reports that the store answered "not found", which settles nothing.
//
// A single miss does not prove absence: an append that is still in flight may land afterwards. The
// record therefore stays pending, and the caller is told the question is still open rather than being
// given a yes/no it cannot have.
var ErrReconcileInconclusive = errors.New("taskfabric: reconciliation inconclusive; the append may still land")

// ErrReconcileGenerationMismatch reports a reconciliation aimed at a task state that has moved on.
var ErrReconcileGenerationMismatch = errors.New("taskfabric: task generation changed since the event was recorded")

// recordPendingReconciliation registers an unsettled append. Called from the flush path, off-lock.
func (f *Fabric) recordPendingReconciliation(r appendResult) {
	if r.EventID == "" {
		// Without an id there is nothing to ask the store about, so the event would be unsettleable.
		// Ids are assigned at record time, so this is a broken invariant rather than a normal case —
		// logged instead of dropped in silence, because a lost record is invisible by construction.
		log.Error(context.Background(), "recordPendingReconciliation",
			"taskfabric: unsettled append has no event id; it cannot be reconciled",
			nil, "task_id", r.TaskID, "event_type", r.Type, "outcome", r.Outcome.String())
		f.reconciliationDropped.Add(1)
		return
	}
	f.reconMu.Lock()
	defer f.reconMu.Unlock()
	if f.reconPending == nil {
		f.reconPending = make(map[string]PendingReconciliation)
	}
	f.reconPending[r.EventID] = PendingReconciliation{
		EventID:    r.EventID,
		TaskID:     r.TaskID,
		Type:       r.Type,
		Generation: r.Generation,
		Outcome:    r.Outcome,
		At:         f.now(),
		Snapshot:   r.Snapshot,
	}
}

// PendingReconciliations returns a copy of the ledger, ordered by time then id so a report of it is
// stable across runs.
func (f *Fabric) PendingReconciliations() []PendingReconciliation {
	f.reconMu.Lock()
	defer f.reconMu.Unlock()
	out := make([]PendingReconciliation, 0, len(f.reconPending))
	for _, r := range f.reconPending {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].EventID < out[j].EventID
	})
	return out
}

// Reconcile settles one pending reconciliation against what the store says.
//
// The rules, in order:
//
//   - an id the fabric is not waiting on is an error, not a no-op: it means the caller is reconciling
//     something else's record;
//   - the generation must match the one the event was recorded at, or the task has moved on and the
//     outcome no longer describes anything;
//   - found == false is INCONCLUSIVE and keeps the record: an in-flight append can still land, so one
//     miss must never be read as "it never happened";
//   - found == true settles it: the tombstone is applied by removing the task, any other event simply
//     leaves the task as memory has it (memory was never rolled back for those).
func (f *Fabric) Reconcile(ctx context.Context, eventID string, found bool, generation uint64) error {
	f.reconMu.Lock()
	rec, ok := f.reconPending[eventID]
	f.reconMu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrReconcileUnknownEvent, eventID)
	}
	if !found {
		// A single miss settles nothing: an append that is still in flight can land afterwards.
		return fmt.Errorf("%w: event %s", ErrReconcileInconclusive, eventID)
	}

	// Apply against LIVE state, in one critical section: the generation must be compared with the
	// task as it is now, and the effect applied before anything else can move it. Comparing the
	// caller's generation with the ledger alone (what this used to do) accepted a stale generation
	// whenever the task had advanced since the event was recorded — and then acted on the new state.
	f.mu.Lock()
	task, live := f.tasks[rec.TaskID]
	// All three must agree: the caller's view, the record, and the live task. Checking only the
	// caller against the live task accepted an OLD record against a task that had since advanced —
	// e.g. a stale tombstone deleting newer state.
	if generation != rec.Generation {
		f.mu.Unlock()
		return fmt.Errorf("%w: event %s was recorded at generation %d, caller says %d",
			ErrReconcileGenerationMismatch, eventID, rec.Generation, generation)
	}
	if live && task.Generation != generation {
		f.mu.Unlock()
		return fmt.Errorf("%w: event %s was recorded at generation %d, task %q is at %d",
			ErrReconcileGenerationMismatch, eventID, rec.Generation, rec.TaskID, task.Generation)
	}

	var applyErr error
	switch {
	case !live && rec.Type == EventTaskDeleted:
		// Already gone: the tombstone and the absence agree.
	case !live && rec.Type == EventTaskCreated && rec.Snapshot != nil:
		// Only a CREATE may bring a task back. Any other event with no live task means the task is
		// gone for a reason — completed and deleted, usually — and restoring its snapshot would
		// resurrect it: the recorded state says "READY at the time of the yield", which is true and
		// completely out of date.
		restored := cloneTask(rec.Snapshot)
		restored.CommitPending, restored.CommitUnconfirmed = false, false
		f.tasks[rec.TaskID] = restored
	case !live:
		log.Warn(ctx, "Reconcile",
			"taskfabric: reconciled event has no in-memory task and no snapshot; nothing to repair",
			"event_id", eventID, "task_id", rec.TaskID, "event_type", rec.Type)
	case rec.Type == EventTaskCompleted:
		// The completion was reverted when its append failed; now that the store confirms it exists,
		// apply it. Clearing the flags without applying the transition was the gap: the ledger entry
		// went away and the task stayed RUNNING forever.
		if task.State != StateCompleted {
			applyErr = task.transition(StateCompleted)
		}
		if applyErr == nil {
			// The checkpoint the quantum produced. Restoring the state without it handed consumers a
			// completed task whose result was the pre-quantum value (nil in the reported case).
			if rec.Snapshot != nil {
				// Assign even when nil: CompleteWithCheckpoint(..., nil) means "the quantum produced
				// no result", and keeping the previous checkpoint would publish an older result as
				// this completion's output.
				task.Checkpoint = rec.Snapshot.Checkpoint
			}
			task.CommitUnconfirmed, task.CommitPending = false, false
			task.UpdatedAt = f.now()
		}
	case rec.Type == EventTaskDeleted:
		delete(f.tasks, rec.TaskID)
	case rec.Type == EventTaskFailed || rec.Type == EventTaskCreated ||
		rec.Type == EventTaskYielded || rec.Type == EventTaskCheckpointed:
		// The state already reflects the event (a failed attempt may have requeued or terminated);
		// what the failed append left behind is the isolation. It is only lifted when no OTHER
		// authoritative record for this task is still unresolved: a yield emits task.yielded and
		// task.checkpointed, and confirming the observational one says nothing about the checkpoint
		// that decides where the task resumes.
		// Two conditions, for two different reasons:
		//   - CommitPending: an authoritative append for this task is IN FLIGHT. It has no ledger
		//     entry yet (entries are written when an append fails), so the ledger alone cannot see
		//     it; releasing the gate here made the task resumable before that append was acknowledged.
		//   - no other unsettled record at THIS generation: a record from an older generation can
		//     never be reconciled against the current task, so counting it would keep the retry
		//     isolated forever.
		if !task.CommitPending && !f.hasUnsettledMustPersist(rec.TaskID, rec.EventID, task.Generation) {
			task.CommitUnconfirmed, task.CommitPending = false, false
			task.UpdatedAt = f.now()
		}
	}
	f.mu.Unlock()

	if applyErr != nil {
		// The record stays: the reconciliation did not settle anything, and retrying it is the
		// caller's decision, not something to hide by deleting the evidence.
		return fmt.Errorf("taskfabric: reconcile %s: %w", eventID, applyErr)
	}

	f.reconMu.Lock()
	delete(f.reconPending, eventID)
	f.reconMu.Unlock()

	log.Info(ctx, "Reconcile",
		"taskfabric: reconciled an unsettled append",
		"event_id", eventID, "task_id", rec.TaskID, "event_type", rec.Type,
		"reported_outcome", rec.Outcome.String())
	return nil
}

// clearPendingReconciliation drops an entry that a later attempt settled.
//
// Silence when the id is absent is deliberate: the common case is a first-attempt success, which has
// nothing to clear.
func (f *Fabric) clearPendingReconciliation(eventID string) {
	if eventID == "" {
		return
	}
	f.reconMu.Lock()
	defer f.reconMu.Unlock()
	delete(f.reconPending, eventID)
}

// hasUnsettledMustPersist reports whether another authoritative record for taskID is still waiting.
//
// Caller must NOT hold reconMu. The exception is the record being settled: its entry is removed after
// this check, so counting it would keep every task isolated forever.
func (f *Fabric) hasUnsettledMustPersist(taskID, exceptEventID string, generation uint64) bool {
	f.reconMu.Lock()
	defer f.reconMu.Unlock()
	for id, rec := range f.reconPending {
		if id == exceptEventID || rec.TaskID != taskID {
			continue
		}
		// A record captured at an older generation describes a state that no longer exists: it cannot
		// be settled against the live task, so it must not hold the current one isolated.
		if rec.Generation != generation {
			continue
		}
		if isMustPersistEvent(rec.Type) {
			return true
		}
	}
	return false
}

// clearPendingForTask drops every ledger entry for one task.
//
// Called when the task is confirmed deleted: an entry recorded before that (an uncertain create, a
// yield) describes a task the store no longer has, and settling it later would resurrect a task its
// owner deliberately removed.
func (f *Fabric) clearPendingForTask(taskID string) {
	f.reconMu.Lock()
	defer f.reconMu.Unlock()
	for id, rec := range f.reconPending {
		if rec.TaskID == taskID {
			delete(f.reconPending, id)
		}
	}
}
