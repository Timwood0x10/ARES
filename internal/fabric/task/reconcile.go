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
	if !ok {
		f.reconMu.Unlock()
		return fmt.Errorf("%w: %s", ErrReconcileUnknownEvent, eventID)
	}
	if rec.Generation != generation {
		f.reconMu.Unlock()
		return fmt.Errorf("%w: event %s was recorded at generation %d, caller says %d",
			ErrReconcileGenerationMismatch, eventID, rec.Generation, generation)
	}
	if !found {
		f.reconMu.Unlock()
		return fmt.Errorf("%w: event %s", ErrReconcileInconclusive, eventID)
	}
	delete(f.reconPending, eventID)
	f.reconMu.Unlock()

	log.Info(ctx, "Reconcile",
		"taskfabric: reconciled an unsettled append",
		"event_id", eventID, "task_id", rec.TaskID, "event_type", rec.Type,
		"reported_outcome", rec.Outcome.String())

	if rec.Type == EventTaskDeleted {
		// The tombstone exists after all, so the deletion happened: finish what the failed response
		// left open.
		f.mu.Lock()
		delete(f.tasks, rec.TaskID)
		f.mu.Unlock()
	}
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
