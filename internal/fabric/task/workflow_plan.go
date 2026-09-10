package taskfabric

import (
	"context"
	"errors"
	"fmt"
)

// planDefaultMaxRetries is the retry budget CompilePlan stamps when a
// PlanStep leaves MaxRetries unset (<= 0). It mirrors the kernel's task
// submission default (cmd/ares agent task creation: MaxRetries=2), so a plan
// step without an explicit budget behaves like a directly submitted task
// instead of the fabric's 0 = no retries.
const planDefaultMaxRetries = 2

// PlanStep is the minimal step description compiled into a Task batch. It is
// defined in taskfabric (not workflow/engine) so the kernel never imports the
// planner package — the caller (cmd layer) projects engine.Step onto it.
//
// (Workflow is a
// compile-time planning layer on top of the single execution kernel.)
type PlanStep struct {
	// ID is the unique step id; becomes the fabric Task ID.
	ID string
	// Capability is the required executor capability (engine.Step.AgentType).
	Capability string
	// DependsOn lists step IDs that must COMPLETE before this step is READY.
	DependsOn []string
	// Priority drives preemption (higher wins); 0 = normal.
	Priority int
	// MaxRetries counts TOTAL attempts (taskfabric.CanRetry semantics).
	// <= 0 means "unset": CompilePlan resolves it to planDefaultMaxRetries
	// (2), it never reaches the fabric as a literal zero.
	MaxRetries int
	// Payload carries the step's input metadata (surfaced via the checkpoint
	// envelope to the executor).
	Payload map[string]any
	// Origin is the provenance stamped by the Kernel (kernel.CallerID) —
	// never supplied by the LLM (same contract as CreateTask). json:"-"
	// keeps it out of every LLM-facing schema.
	Origin string `json:"-"`
	// SessionID scopes this step's task to a session (SessionID 贯通).
	// Stamped onto the checkpoint envelope so the executor can look up the
	// per-session L2 graph registry. Empty = session-less (legacy behavior).
	SessionID string `json:"-"`
}

// CompilePlan validates a batch of PlanSteps and creates them as READY tasks
// in one all-or-nothing transaction:
//
//   - every DependsOn reference must resolve — inside the batch first, then
//     against tasks already in the fabric (see resolveDependencies);
//   - the dependency graph must be acyclic (topological check);
//   - every Create must succeed — any failure rolls back the tasks already
//     created in this batch so a half-built DAG never pollutes the ready queue.
//
// It returns the created task IDs in input order.
//
// Atomicity: validation, creation and rollback run inside ONE fabric
// critical section. Pre-fix, the creates ran one-by-one off-lock, so (a) a
// concurrent drain could acquire a half-built batch's first task and the
// rollback Delete then bounced off ErrTaskUndeletable, leaving a half-built
// DAG in the fabric, and (b) a task the batch validated its dependencies
// against could be concurrently deleted before the batch's creates landed.
// Holding f.mu across the batch closes both windows: Acquire and Delete both
// need the same lock, so neither can interleave. The durable appends are
// still flushed AFTER the lock is released (the off-lock I/O contract).
//
// Args:
//   - ctx: unused today (Create is synchronous); kept for signature symmetry
//     with future async compilation.
//   - steps: the batch to compile; must be non-empty.
//
// Returns:
//   - []string: the created task IDs, in input order.
//   - error: ErrTaskExists / ErrTaskIDRequired / validation errors, wrapped.
func (f *Fabric) CompilePlan(ctx context.Context, steps []PlanStep) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("taskfabric: compile plan: %w", err)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("taskfabric: compile plan: empty step batch")
	}
	byID := make(map[string]PlanStep, len(steps))
	for _, s := range steps {
		if s.ID == "" {
			return nil, fmt.Errorf("taskfabric: compile plan: step id required")
		}
		if _, dup := byID[s.ID]; dup {
			return nil, fmt.Errorf("taskfabric: compile plan: duplicate step id %q", s.ID)
		}
		byID[s.ID] = s
	}
	// Sample the strategy attribution ONCE for the whole batch so every
	// task of the batch carries the same strategy even if the active strategy
	// changes mid-compilation. Create fills the stamp only when the envelope
	// field is still empty, so this pre-stamp wins. Sampled before f.mu: the
	// stamp fn is external code and must not run under the fabric lock.
	strategyID := f.strategyStampID()

	pending := make([]*pendingAppend, 0, len(steps))
	f.mu.Lock()
	// Dependency closure: batch first, then tasks already in the fabric
	// (see resolveDependencies). Cross-batch resolution is what makes
	// runtime graph growth possible — a node grown at runtime depends on
	// nodes compiled by an earlier batch, which are typically already
	// COMPLETED and therefore let the new task go READY immediately.
	// Re-validated INSIDE the same critical section as the creates, so a
	// dependency resolved here cannot vanish before the batch lands.
	if err := resolveDependencies(steps, byID, f.tasks); err != nil {
		f.mu.Unlock()
		return nil, fmt.Errorf("taskfabric: compile plan: %w", err)
	}
	if err := detectPlanCycle(steps, byID); err != nil {
		f.mu.Unlock()
		return nil, err
	}
	// All-or-nothing creation under the lock: no drain can acquire a
	// half-built batch (Acquire needs f.mu), so the rollback Delete below
	// can never bounce off ErrTaskUndeletable.
	created := make([]string, 0, len(steps))
	for _, s := range steps {
		deps := append([]string(nil), s.DependsOn...)
		// PlanStep.MaxRetries <= 0 means "unset" (LLM omitted it / engine
		// step carried no retry_policy). The fabric reads 0 as "no retries",
		// so resolving unset to the kernel submission default here keeps the
		// documented plan contract: an unset step gets 2 total attempts
		// (first try + one retry), same as a directly submitted task.
		maxRetries := s.MaxRetries
		if maxRetries <= 0 {
			maxRetries = planDefaultMaxRetries
		}
		t := &Task{
			ID:           s.ID,
			Capability:   s.Capability,
			Dependencies: deps,
			Priority:     s.Priority,
			Origin:       s.Origin,
			RetryPolicy:  RetryPolicy{MaxRetries: maxRetries},
		}
		if s.Payload != nil || s.SessionID != "" {
			env := &CheckpointEnvelope{Payload: s.Payload}
			if strategyID != "" {
				env.StrategyID = strategyID
			}
			if s.SessionID != "" {
				env.SessionID = s.SessionID
			}
			t.Checkpoint = env
		}
		if err := f.createLocked(t, strategyID, &pending); err != nil {
			// Roll back EVERY created id under the same lock — each delete
			// can only fail if the task is somehow non-READY, which the
			// single critical section makes impossible. Report leftovers
			// loudly anyway (defense in depth).
			var delErrs []error
			for _, id := range created {
				if delErr := f.deleteLocked(id); delErr != nil {
					delErrs = append(delErrs, fmt.Errorf("rollback %q: %w", id, delErr))
				}
			}
			rollErr := fmt.Errorf("taskfabric: compile plan create %q: %w", s.ID, err)
			f.mu.Unlock()
			// The batch's task.created appends must NOT be flushed (their
			// tasks were rolled back — flushing would publish phantom
			// events), but their sequence numbers must still be claimed so
			// the durable-append barrier is not left with a gap no one will
			// ever complete.
			f.discardAppends(&pending)
			if len(delErrs) > 0 {
				return nil, errors.Join(rollErr,
					fmt.Errorf("rollback incomplete: %w", errors.Join(delErrs...)))
			}
			return nil, rollErr
		}
		created = append(created, s.ID)
	}
	f.mu.Unlock()
	f.flushAppends(&pending)
	return created, nil
}

// CompileNode compiles a single PlanStep into exactly one task. It is the
// entry point the incremental compiler uses when the live MutableDAG grows a
// node at runtime: one graph change, one task, and every other task of the
// graph is left untouched — including the ones that are currently RUNNING.
//
// The dependency rules are CompilePlan's, so a node may depend on a task
// compiled by an earlier batch (typically an already-COMPLETED plan node);
// depsCompletedLocked then reports it READY on the spot.
//
// Args:
//   - ctx: bounds the compile (see CompilePlan).
//   - step: the single step to compile; ID must be non-empty.
//
// Returns:
//   - string: the created task ID (== step.ID).
//   - error: ErrTaskExists / ErrTaskIDRequired / validation errors, wrapped.
func (f *Fabric) CompileNode(ctx context.Context, step PlanStep) (string, error) {
	ids, err := f.CompilePlan(ctx, []PlanStep{step})
	if err != nil {
		return "", err
	}
	if len(ids) != 1 {
		// Unreachable: CompilePlan returns one id per input step or an
		// error. Guarded so the 1:1 contract cannot silently widen.
		return "", fmt.Errorf("taskfabric: compile node %q: expected 1 task, got %d", step.ID, len(ids))
	}
	return ids[0], nil
}

// resolveDependencies validates the dependency closure of a compile batch.
//
// Resolution order is the whole point of this function:
//
//  1. inside the batch — the classic whole-graph compile;
//  2. a task already in the fabric — the runtime-growth case, where the new
//     node depends on something compiled by an earlier batch;
//  3. neither → an error, naming both ids.
//
// Order matters: a batch-local definition always wins, so a batch that
// redefines a node does not silently bind to a same-id task left behind from
// an earlier compile (that binding would resurrect a stale dependency edge).
//
// Caller must hold f.mu.
//
// Args:
//   - steps: the batch being compiled.
//   - byID: id → step for the batch.
//   - existing: the fabric's live task index (read-only here).
//
// Returns:
//   - error: the first unresolved dependency, or nil.
func resolveDependencies(steps []PlanStep, byID map[string]PlanStep, existing map[string]*Task) error {
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, ok := byID[dep]; ok {
				continue
			}
			if _, ok := existing[dep]; ok {
				continue
			}
			return fmt.Errorf("step %q depends on unknown step %q", s.ID, dep)
		}
	}
	return nil
}

// detectPlanCycle runs a depth-first color walk over the batch dependency
// graph and reports the first cycle found.
//
// Dependencies that resolve outside the batch (a task already in the fabric)
// are not walked: they existed before this batch, so no cycle introduced by
// this batch can pass through them, and treating them as zero-value batch
// members would pollute the walk with phantom nodes.
//
// Args:
//   - steps: the batch in input order.
//   - byID: id → step index for O(1) adjacency lookups.
//
// Returns:
//
//	error - a cycle description, or nil when the graph is a DAG.
func detectPlanCycle(steps []PlanStep, byID map[string]PlanStep) error {
	const (
		white = 0 // unvisited
		gray  = 1 // in the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(steps))
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		color[id] = gray
		next := append(append([]string(nil), path...), id)
		for _, dep := range byID[id].DependsOn {
			if _, ok := byID[dep]; !ok {
				// Resolves to a task outside this batch: not ours to walk.
				continue
			}
			switch color[dep] {
			case gray:
				return fmt.Errorf("taskfabric: compile plan: dependency cycle: %v -> %s", next, dep)
			case white:
				if err := visit(dep, next); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for _, s := range steps {
		if color[s.ID] == white {
			if err := visit(s.ID, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
