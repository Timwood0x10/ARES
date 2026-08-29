package taskfabric

import (
	"context"
	"fmt"
)

// PlanStep is the minimal step description compiled into a Task batch. It is
// defined in taskfabric (not workflow/engine) so the kernel never imports the
// planner package — the caller (cmd layer) projects engine.Step onto it.
//
// See ares-repair-plan-zh.md appendix C (W9 / option A: workflow as a
// compile-time planning layer on top of the single execution kernel).
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
	MaxRetries int
	// Payload carries the step's input metadata (surfaced via the checkpoint
	// envelope to the executor).
	Payload map[string]any
	// Origin is the provenance stamped by the Kernel (kernelctx.CallerID) —
	// never supplied by the LLM (same contract as CreateTask). json:"-"
	// keeps it out of every LLM-facing schema.
	Origin string `json:"-"`
}

// CompilePlan validates a batch of PlanSteps and creates them as READY tasks
// in one all-or-nothing transaction:
//
//   - every DependsOn reference must resolve to a step in the batch;
//   - the dependency graph must be acyclic (topological check);
//   - every Create must succeed — any failure rolls back the tasks already
//     created in this batch so a half-built DAG never pollutes the ready queue.
//
// It returns the created task IDs in input order.
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
	// Dependency closure: every DependsOn must resolve inside the batch.
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("taskfabric: compile plan: step %q depends on unknown step %q", s.ID, dep)
			}
		}
	}
	if err := detectPlanCycle(steps, byID); err != nil {
		return nil, err
	}
	// All-or-nothing creation: roll back on any failure so the ready queue is
	// never polluted by a half-built DAG.
	created := make([]string, 0, len(steps))
	for _, s := range steps {
		deps := append([]string(nil), s.DependsOn...)
		t := &Task{
			ID:           s.ID,
			Capability:   s.Capability,
			Dependencies: deps,
			Priority:     s.Priority,
			Origin:       s.Origin,
			// MaxRetries <= 0 keeps the kernel default (2 = first attempt +
			// one retry); a positive value is honored verbatim.
			RetryPolicy: RetryPolicy{MaxRetries: s.MaxRetries},
		}
		if s.Payload != nil {
			t.Checkpoint = &CheckpointEnvelope{Payload: s.Payload}
		}
		if err := f.Create(t); err != nil {
			for _, id := range created {
				if delErr := f.Delete(id); delErr != nil {
					return nil, fmt.Errorf("taskfabric: compile plan rollback %q: %w (original error: %v)", id, delErr, err)
				}
			}
			return nil, fmt.Errorf("taskfabric: compile plan create %q: %w", s.ID, err)
		}
		created = append(created, s.ID)
	}
	return created, nil
}

// detectPlanCycle runs a depth-first color walk over the batch dependency
// graph and reports the first cycle found.
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
