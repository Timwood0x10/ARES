package taskfabric

import (
	"fmt"
	"sort"
)

// SetDependencies replaces a task's dependency list in place. It is the
// incremental-compile primitive behind runtime graph growth: an AddEdge or
// RemoveEdge on the live MutableDAG must move ONE task's scheduling shape,
// not rebuild the whole compiled batch.
//
// Only a READY task may be rewired. A LEASED/RUNNING/SUSPENDED task has an
// owner whose quantum was admitted against the dependency posture it read at
// acquire time — rewriting under it would let a task run before a dependency
// it never knew about. A terminal task's dependency list is a historical
// fact. Neither case is silent: the caller gets ErrTaskNotMutable (wrapped
// with the offending state) and must account for it.
//
// The rewrite is recorded as an observability-only event (EventTaskUpdated);
// see that constant for why it is deliberately not persisted.
//
// Args:
//   - id: the task to rewire.
//   - deps: the new dependency IDs; copied, so the caller's slice stays its
//     own (same isolation contract as Create).
//
// Returns:
//   - error: ErrTaskNotFound, or ErrTaskNotMutable when the state forbids it.
func (f *Fabric) SetDependencies(id string, deps []string) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if t.State != StateReady {
		return fmt.Errorf("%w: task %q is %s", ErrTaskNotMutable, id, t.State)
	}
	t.Dependencies = append([]string(nil), deps...)
	pending = append(pending, f.recordLocked(t, EventTaskUpdated))
	return nil
}

// UpdatePayload replaces the Payload inside a task's checkpoint envelope
// without recreating the task. It is the incremental-compile action behind a
// metadata-only graph change (SetNodeMetadata): a pure attribute patch must
// not cost a task rebuild, and must not reset the task's CreatedAt or its
// submission-time strategy attribution.
//
// Refused only while the task is RUNNING — a running quantum is already
// reading its payload. READY / LEASED / SUSPENDED / terminal tasks are all
// writable: nothing has committed to the payload yet, or it is history.
//
// The envelope's other fields (UserProfile, StepCheckpoint, UsedExperienceID,
// StrategyID) are preserved verbatim; the payload is copied so the caller
// cannot later mutate fabric-owned state.
//
// Args:
//   - id: the task whose payload to replace.
//   - payload: the new payload map (may be nil to clear it).
//
// Returns:
//   - error: ErrTaskNotFound, ErrCheckpointSchemaVersion, or
//     ErrTaskNotMutable when the task is RUNNING.
func (f *Fabric) UpdatePayload(id string, payload map[string]any) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if t.State == StateRunning {
		return fmt.Errorf("%w: task %q is %s", ErrTaskNotMutable, id, t.State)
	}
	if t.Checkpoint == nil && len(payload) == 0 {
		// Nothing stored and nothing to store: do not invent an envelope
		// (that would flip TaskView.HasCheckpoint for no reason).
		return nil
	}
	pcopy := make(map[string]any, len(payload))
	for k, v := range payload {
		pcopy[k] = v
	}
	// Decode → replace → re-encode keeps every field the envelope already
	// carried (notably StrategyID, the submission-time attribution) intact
	// and keeps this the single decode path for checkpoints.
	dc, err := DecodeCheckpoint(t.Checkpoint)
	if err != nil {
		return err
	}
	dc.Payload = pcopy
	t.Checkpoint = EncodeCheckpoint(dc)
	pending = append(pending, f.recordLocked(t, EventTaskUpdated))
	return nil
}

// Dependents returns the ids of every task that lists id in its Dependencies
// — the reverse-edge index the incremental compiler needs to migrate
// successors onto a replacement node (ChangeReplaceNode). Sorted for
// deterministic callers.
//
// Args:
//   - id: the dependency to look up (need not itself exist as a task).
//
// Returns:
//   - []string: the dependent task ids (empty when there are none).
func (f *Fabric) Dependents(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, 1)
	for taskID, t := range f.tasks {
		for _, dep := range t.Dependencies {
			if dep == id {
				out = append(out, taskID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// ReferencedDependencies returns the set of every task id that appears in at
// least one other task's Dependencies — the union of the dependency graph's
// "targets". Housekeeping sweeps (the reaper) use it to avoid deleting a
// terminal task that live tasks still reference: depsCompletedLocked treats
// a missing dependency as unsatisfied forever, so deleting a referenced
// predecessor would strand its dependents permanently. One O(n·d) pass under
// a single lock instead of a per-candidate Dependents scan.
func (f *Fabric) ReferencedDependencies() map[string]struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]struct{})
	for _, t := range f.tasks {
		for _, dep := range t.Dependencies {
			out[dep] = struct{}{}
		}
	}
	return out
}
