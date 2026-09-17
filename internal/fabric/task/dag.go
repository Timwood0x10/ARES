package taskfabric

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
	return depsCompletedLocked(f.tasks, t.Dependencies), nil
}

// ReadyTasks returns the ids of every task whose dependencies are satisfied
// and that is currently READY — the scheduler's work source. No leader
// decides "B is done, now run C"; the completed states make C ready.
func (f *Fabric) ReadyTasks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id, t := range f.tasks {
		if t.State != StateReady {
			continue
		}
		if depsCompletedLocked(f.tasks, t.Dependencies) {
			out = append(out, id)
		}
	}
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
func (f *Fabric) ResumableTasks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id, t := range f.tasks {
		switch t.State {
		case StateReady:
			if depsCompletedLocked(f.tasks, t.Dependencies) {
				out = append(out, id)
			}
		case StateSuspended:
			if t.Lease != nil && !t.Lease.IsExpired(f.now()) {
				out = append(out, id)
			}
		}
	}
	return out
}

// depsCompletedLocked reports whether every dependency task exists and is
// COMPLETED. Caller must hold f.mu.
func depsCompletedLocked(tasks map[string]*Task, deps []string) bool {
	for _, dep := range deps {
		d, ok := tasks[dep]
		if !ok || d.State != StateCompleted {
			return false
		}
	}
	return true
}

// cascadeFailureLocked fails every transitive READY dependent of the task
// that just reached terminal FAILED. A FAILED predecessor can never satisfy
// depsCompletedLocked again, so leaving its dependents READY would strand the
// whole downstream subgraph forever: never in ReadyTasks (unschedulable),
// protected from the reaper (READY is live), and a permanent "round still
// active" for PlanLoop. Terminal failure therefore propagates — one exhausted
// root kills the branch that can no longer run.
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
			if err := t.transition(StateFailed); err != nil {
				continue
			}
			t.FailedDependency = cur
			*pending = append(*pending, f.recordLocked(t, EventTaskFailed))
			queue = append(queue, taskID)
		}
	}
}
