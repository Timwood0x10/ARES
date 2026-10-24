package taskfabric

import (
	"slices"
	"sort"
)

// IsReady reports whether a task's dependencies are all satisfied — every
// dependency task is COMPLETED — and the task itself is currently READY.
// This is the DAG-as-scheduling-source primitive (design of
// ares-runtime): the Scheduler only asks is_ready(task); the topology
// lives in each task's Dependencies. Callers construct
// Dependencies manually today; live DAG wiring is the
// workflow engine's job.
//
// Args:
//   - id: the task id.
//
// Returns:
//   - bool: true when the task is READY and all dependencies completed.
//   - error: ErrTaskNotFound for an unknown id.
func (f *Fabric) IsReady(id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return false, ErrTaskNotFound
	}
	if t.State != StateReady {
		return false, nil
	}
	return depsSatisfiedLocked(f.tasks, t), nil
}

// ReadyTasks returns the ids of every task whose dependencies are satisfied
// and that is currently READY — the scheduler's work source. No leader
// decides "B is done, now run C"; the completed states make C ready.
//
// A task waiting out a retry backoff (NextAttemptAt in the future) is excluded:
// it is READY in state but not runnable yet, and reporting it as ready would
// hand callers work they cannot acquire.
func (f *Fabric) ReadyTasks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id, t := range f.tasks {
		if t.State != StateReady {
			continue
		}
		if !f.retryDueLocked(t) {
			continue
		}
		if !approvalCleared(t) {
			continue
		}
		if depsSatisfiedLocked(f.tasks, t) {
			out = append(out, id)
		}
	}
	f.sortResumableLocked(out)
	return out
}

// ResumableTasks returns the ids of every task that can run a quantum right
// now: READY tasks (dependencies satisfied) plus SUSPENDED tasks whose lease
// is still valid — a yielded task the scheduler resumes by re-acquiring at the
// next quantum boundary (SUSPENDED semantics lock: "Continue is the
// Scheduler's decision via re-acquire"). SUSPENDED tasks with an expired lease
// are intentionally excluded: the crash-recovery path (CheckExpiredLeases)
// requeues them to READY, and including them here too would let two drains
// race the same task.
//
// A READY task whose retry backoff has not elapsed is excluded for the same
// reason ReadyTasks excludes it: the scheduler must not be handed work it cannot
// acquire. SUSPENDED tasks never carry a backoff (Fail requeues to READY, never
// to SUSPENDED), so only the READY branch needs the gate.
func (f *Fabric) ResumableTasks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id, t := range f.tasks {
		switch t.State {
		case StateReady:
			if !f.retryDueLocked(t) {
				continue
			}
			if !approvalCleared(t) {
				continue
			}
			if depsSatisfiedLocked(f.tasks, t) {
				out = append(out, id)
			}
		case StateSuspended:
			if !approvalCleared(t) {
				continue
			}
			if t.Lease != nil && !t.Lease.IsExpired(f.now()) {
				out = append(out, id)
			}
		}
	}
	f.sortResumableLocked(out)
	return out
}

// sortResumableLocked sorts the resumable task IDs for deterministic dispatch
// order (M1) with session-level fairness aging (M3). The sort key is:
//   - effectivePriority DESC, where effectivePriority = priority + agingWeight × waitSeconds
//     (waitSeconds = now - ReadyAt; a task that has waited longer gets a higher
//     effective priority, so a session with 100 READY tasks cannot starve a
//     session with 1 READY task indefinitely);
//   - ReadyAt ASC (FIFO within an equal effective priority band);
//   - id ASC (total order tiebreaker for determinism).
//
// The aging weight is deliberately small (0.01 priority points per second)
// so it only breaks ties between sessions, not within one — a high-priority
// task always outranks a low-priority one unless the wait difference is
// enormous (hundreds of seconds), which is exactly the intended behavior.
//
// Caller must hold f.mu (reads task fields). A zero ReadyAt sorts before
// any non-zero one (treats unset as "oldest"), which is the correct behavior
// for tasks restored from a durable store that predates the ReadyAt field.
func (f *Fabric) sortResumableLocked(ids []string) {
	if len(ids) <= 1 {
		return
	}
	now := f.now()
	// agingWeight: 0.01 priority points per second of wait. A task waiting
	// 100s gets +1 effective priority — enough to cross one priority band,
	// but not enough to dominate within one.
	const agingWeight = 0.01
	sort.Slice(ids, func(i, j int) bool {
		ti, oki := f.tasks[ids[i]]
		tj, okj := f.tasks[ids[j]]
		if !oki || !okj {
			return ids[i] < ids[j]
		}
		// effectivePriority = priority + agingWeight × waitSeconds.
		// A zero ReadyAt means "oldest possible" for restored tasks.
		waitI := 0.0
		if !ti.ReadyAt.IsZero() {
			waitI = now.Sub(ti.ReadyAt).Seconds()
			if waitI < 0 {
				waitI = 0
			}
		}
		waitJ := 0.0
		if !tj.ReadyAt.IsZero() {
			waitJ = now.Sub(tj.ReadyAt).Seconds()
			if waitJ < 0 {
				waitJ = 0
			}
		}
		effI := float64(ti.Priority) + agingWeight*waitI
		effJ := float64(tj.Priority) + agingWeight*waitJ
		if effI != effJ {
			return effI > effJ
		}
		if !ti.ReadyAt.Equal(tj.ReadyAt) {
			return ti.ReadyAt.Before(tj.ReadyAt)
		}
		return ids[i] < ids[j]
	})
}

// approvalCleared reports whether a task's HITL approval gate has been
// opened. A task that does not require approval is always cleared. A task
// that requires approval is cleared only when the decision is "approved".
// Pending ("") and rejected both return false — pending is blocked, rejected
// is driven to FAILED by the resolve path so it never lingers.
func approvalCleared(t *Task) bool {
	return !t.RequiresApproval || t.ApprovalDecision == ApprovalDecisionApproved
}

// retryDueLocked reports whether a requeued task has served its retry backoff.
// Caller must hold f.mu. A zero NextAttemptAt means "no backoff configured, or
// none pending", i.e. immediately runnable.
func (f *Fabric) retryDueLocked(t *Task) bool {
	return t.NextAttemptAt.IsZero() || !t.NextAttemptAt.After(f.now())
}

// dependencySatisfied reports whether one dependency is satisfied for the given
// dependent. It is the single place the AllowPartial policy is read, and every
// scheduler-facing query (IsReady, ReadyTasks, ResumableTasks) goes through it,
// so "let through by the policy" and "handed to a scheduler" are the same
// question. Acquire deliberately does NOT consult it: Acquire is the CAS
// ownership claim (state + owner + deadline + backoff), and the scheduler's work
// source — the thing that decides what may run — is ResumableTasks. Adding a
// dependency guard there was tried and reverted (2026-10-10): four harnesses
// drive a specific node directly, which is the contract, not an oversight.
//
// The rule: COMPLETED satisfies. A FAILED dependency satisfies only when the
// dependent opted in AND that exact dependency was recorded as missing (see
// recordDegradedInputLocked). AllowPartial alone is deliberately not enough: an
// opted-in task with a second dependency still RUNNING must keep waiting for it.
func dependencySatisfied(dep, dependent *Task) bool {
	if dep == nil {
		return false
	}
	if dep.State == StateCompleted {
		return true
	}
	if dependent.AllowPartial && dep.State == StateFailed && slices.Contains(dependent.DegradedInputs, dep.ID) {
		return true
	}
	return false
}

// depsSatisfiedLocked reports whether every dependency of t exists and is
// satisfied (see dependencySatisfied). Caller must hold f.mu.
func depsSatisfiedLocked(tasks map[string]*Task, t *Task) bool {
	for _, dep := range t.Dependencies {
		d, ok := tasks[dep]
		if !ok || !dependencySatisfied(d, t) {
			return false
		}
	}
	return true
}

// cascadeFailureLocked fails every transitive READY dependent of the task
// that just reached terminal FAILED. A FAILED predecessor can never satisfy
// depsSatisfiedLocked again — unless the dependent opted into AllowPartial and
// recorded the gap — so leaving its dependents READY would strand the whole
// downstream subgraph forever: never in ReadyTasks (unschedulable), protected
// from the reaper (READY is live), and a permanent "round still active" for
// PlanLoop. Terminal failure therefore propagates — one exhausted root kills the
// branch that can no longer run.
//
// An AllowPartial dependent is deliberately NOT killed: it stays READY with the
// failed predecessor recorded in its DegradedInputs, which is precisely what
// makes it schedulable (dependencySatisfied). It is also not queued for further
// cascade — it has not failed, so its own dependents keep waiting for it to
// complete.
//
// Only READY dependents are cascaded. A LEASED/RUNNING/SUSPENDED dependent
// cannot exist in practice (it could only be acquired while its dependencies
// were COMPLETED, and COMPLETED never moves), and a terminal one is already
// final; both are left untouched rather than fought over. Each cascaded task
// records its own task.failed (with FailedDependency naming the nearest
// failed predecessor) so the event log and every subscriber — including the
// L2 answer-failure release — see the subgraph die. Callers must hold f.mu.
func (f *Fabric) cascadeFailureLocked(rootID string, pending *[]*pendingAppend) {
	// Reverse-edge index, built once under the lock: O(tasks·deps) per
	// cascade instead of a full map scan per dequeued task.
	dependents := make(map[string][]string, len(f.tasks))
	for taskID, t := range f.tasks {
		for _, dep := range t.Dependencies {
			dependents[dep] = append(dependents[dep], taskID)
		}
	}
	queue := []string{rootID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, taskID := range dependents[cur] {
			t := f.tasks[taskID]
			if t == nil || t.State != StateReady {
				continue
			}
			if t.AllowPartial {
				// Declared degradation: record the gap instead of dying. The
				// task stays READY and becomes schedulable as soon as its
				// remaining dependencies complete.
				f.recordDegradedInputLocked(t, cur, pending)
				continue
			}
			if err := t.transition(StateFailed); err != nil {
				continue
			}
			t.FailedDependency = cur
			*pending = append(*pending, f.recordLocked(t, EventTaskFailed))
			queue = append(queue, taskID)
		}
	}
}
