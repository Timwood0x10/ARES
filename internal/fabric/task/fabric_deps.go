package taskfabric

import (
	"context"
	"fmt"
	"slices"
	"sort"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
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
// The new list is VALIDATED before it is applied, and a rejected call leaves the task's
// existing dependencies untouched: the three rejections below are exactly the ways a
// dependency list can strand a task with no owner and no error, and applying a broken
// list "then reporting the problem" would create the stranding it just detected.
//
// Args:
//   - id: the task to rewire.
//   - deps: the new dependency IDs; copied, so the caller's slice stays its
//     own (same isolation contract as Create).
//
// Returns:
//   - error: ErrTaskNotFound, ErrTaskNotMutable when the state forbids it, and for a
//     rejected list either apperrors.ErrNotFound (a dependency that does not exist) or
//     apperrors.ErrDependencyCycle (a self-edge, or a cycle through the live graph).
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
	if err := f.validateDependenciesLocked(id, deps); err != nil {
		return err
	}
	t.Dependencies = append([]string(nil), deps...)
	pending = append(pending, f.recordLocked(t, EventTaskUpdated))
	return nil
}

// validateDependenciesLocked rejects a dependency list that could strand the task
// SILENTLY: an unknown id, a self-edge, or a cycle through the live graph.
//
// All three share one failure mode — the task stays READY forever, with no owner and no
// error anywhere — which is the "stranded silently" family P0-2 closed on the
// create_task path. SetDependencies is the OTHER way the runtime graph grows, so the
// same invariant has to hold here: the runtime graph stays a DAG, and every edge points
// at something that exists.
//
// The two sentinels are chosen to compose with the task fabric's permanent-cause table
// (failure.go): neither a missing dependency nor a cycle can be fixed by running the
// same step again, so a caller that reacts to one of these by retrying would be burning
// its budget on a graph edit that is wrong by construction.
//
// Caller must hold f.mu — it walks the live task map.
func (f *Fabric) validateDependenciesLocked(id string, deps []string) error {
	for _, dep := range deps {
		if dep == id {
			return fmt.Errorf("%w: task %q cannot depend on itself", apperrors.ErrDependencyCycle, id)
		}
		if _, ok := f.tasks[dep]; !ok {
			// ErrDependencyUnresolved is the fabric's own vocabulary for a dependency id
			// that names no live task — the strict create path already answers this way
			// (WithResolvedDependencies), and two names for one condition is how the two
			// halves of a graph writer stop agreeing.
			return fmt.Errorf("%w: dependency %q of task %q does not exist — nothing would ever satisfy it",
				ErrDependencyUnresolved, dep, id)
		}
	}
	if f.wouldCycleLocked(id, deps) {
		return fmt.Errorf("%w: task %q would depend, directly or transitively, on itself", apperrors.ErrDependencyCycle, id)
	}
	return nil
}

// wouldCycleLocked reports whether making id depend on deps closes a cycle, i.e. whether
// id is reachable from deps along the live dependency edges.
//
// Iterative with a visited set rather than recursive: a cycle that is ALREADY in the
// graph (written before this validation existed, or restored from an older store) must
// make this walk terminate, not spin. It walks the existing edges only — dep's own list
// is not what is being validated here; each rewrite is checked against the graph as it
// stands, which is sufficient because every accepted rewrite kept it acyclic.
func (f *Fabric) wouldCycleLocked(id string, deps []string) bool {
	visited := make(map[string]bool, len(deps))
	stack := append([]string(nil), deps...)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == id {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		if t, ok := f.tasks[current]; ok {
			stack = append(stack, t.Dependencies...)
		}
	}
	return false
}

// PayloadKeyDegradedInputs is the output-contract key carrying the ids of the
// predecessors a task ran without (plan §3.3 ④). One concept, one wire name: it
// matches restoreKeyDegradedInputs, which carries the same list on the event
// payload that a restart folds. Exported so the planner cognition
// (internal/fabric/agent) can read the same key it injects into the LLM
// context — a single source of truth across the producer and consumer.
const PayloadKeyDegradedInputs = "degraded_inputs"

// PayloadKeyRequeued is the task-payload key carrying Task.Requeued to the
// executing quantum. The scheduler stamps it at dispatch (ToModelTask) and the
// finisher reports it on the terminal result, so a caller can tell "this run
// continued earlier work" from "this is the task's first run" without reading
// fabric internals (defect A6). Exported so both sides — the scheduler that
// writes it and the cognition that reads it — share one wire name.
const PayloadKeyRequeued = "requeued"

// recordDegradedInputLocked marks missing as an input the dependent is
// proceeding without. It is the AllowPartial branch's whole action: the task
// stays READY, the gap is recorded durably, and scheduling is unlocked because
// dependencySatisfied accepts exactly the ids recorded here.
//
// The event is task.checkpointed rather than task.updated on purpose:
// task.updated is observability-only and is never folded by RestoreFromStore, so
// a gap recorded through it would be forgotten by a restart — re-stranding the
// very task the cascade just unblocked.
//
// Returns true when the gap was newly recorded, false when it was already known
// (the cascade can reach one dependent through two failed predecessors).
// Callers must hold f.mu.
func (f *Fabric) recordDegradedInputLocked(t *Task, missing string, pending *[]*pendingAppend) bool {
	if slices.Contains(t.DegradedInputs, missing) {
		return false
	}
	t.DegradedInputs = append(t.DegradedInputs, missing)
	f.mirrorDegradedInputsLocked(t)
	*pending = append(*pending, f.recordLocked(t, EventTaskCheckpointed))
	// task.ready carries the gap too: the event stream alone must distinguish
	// "waiting on a retry", "waiting on a dependency" and "runnable with a hole
	// in its input".
	*pending = append(*pending, f.recordWithExtrasLocked(t, EventTaskReady, map[string]any{
		restoreKeyDegradedInputs: append([]string(nil), t.DegradedInputs...),
	}))
	return true
}

// mirrorDegradedInputsLocked writes t.DegradedInputs into the task's checkpoint
// payload under degraded_inputs, so whoever consumes this task's output can tell
// "not checked" from "checked, nothing found" (ADR-1 option (a)). It reuses the
// same decode → patch → encode path as UpdatePayload, so every other envelope
// field (StrategyID, SessionID, StepCheckpoint) survives; a task with no
// envelope gets one, because the gap IS checkpoint content.
//
// The mirror is best-effort: DegradedInputs and the event log stay the
// authoritative record, so an undecodable envelope is logged rather than
// failing the cascade.
func (f *Fabric) mirrorDegradedInputsLocked(t *Task) {
	dc, err := f.DecodeCheckpoint(t.Checkpoint)
	if err != nil {
		log.Warn(context.Background(), "mirrorDegradedInputsLocked",
			"taskfabric: cannot mirror degraded_inputs into payload",
			"task", t.ID, "error", err)
		return
	}
	if dc.Payload == nil {
		dc.Payload = make(map[string]any, 1)
	}
	dc.Payload[PayloadKeyDegradedInputs] = append([]string(nil), t.DegradedInputs...)
	t.Checkpoint = EncodeCheckpoint(dc)
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
	dc, err := f.DecodeCheckpoint(t.Checkpoint)
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
// terminal task that live tasks still reference: depsSatisfiedLocked treats
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
