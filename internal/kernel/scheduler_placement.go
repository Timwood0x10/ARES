package kernel

import (
	"context"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// hasRecoveryCandidate mirrors execute's binding fallback: an absent or incapable replacement
// does not prevent the task from using the general pool.
func (s *Scheduler) hasRecoveryCandidate(tk *taskfabric.Task) bool {
	id, bound := s.boundFor(tk.ID)
	if !bound {
		return false
	}
	exec, ok := s.LookupExecutor(id)
	return ok && taskfabric.CapabilityOverlap(tk.Capability, []string{string(exec.Type())}) > 0
}

// preemptionAgentAvailable ignores a quantum's busy flag, but checks that the agent can still
// execute after it gives up that quantum. Recovery bindings are never shared capacity.
func (s *Scheduler) preemptionAgentAvailable(id string) bool {
	if s.isBoundToAnyTask(id) {
		return false
	}
	if s.agents != nil {
		if agent, err := s.agents.Get(id); err == nil {
			return agent.IsSchedulable()
		}
		if !s.hybridStatic {
			return false
		}
	}
	_, ok := s.LookupExecutor(id)
	return ok
}

// pruneDispatchAssignments drops obsolete reservations, including tasks waiting for approval or
// retry backoff. Called under placementMu by both planning and admission so cancellations and
// executor deaths do not reserve capacity until another preemption sweep happens.
func (s *Scheduler) pruneDispatchAssignments(ctx context.Context) {
	if len(s.dispatchAssignments) == 0 {
		return
	}
	ready := make(map[string]bool)
	for _, id := range s.fabric.ResumableTasks() {
		ready[id] = true
	}
	for taskID, agentID := range s.dispatchAssignments {
		if !ready[taskID] || !s.preemptionAgentAvailable(agentID) {
			delete(s.dispatchAssignments, taskID)
			continue
		}
		tk, err := s.fabric.Task(taskID)
		if err != nil || s.hasRecoveryCandidate(tk) {
			delete(s.dispatchAssignments, taskID)
			continue
		}
		if taskfabric.CapabilityOverlap(tk.Capability, s.agentCapabilities(agentID)) == 0 {
			delete(s.dispatchAssignments, taskID)
			continue
		}
		if ok, _ := s.admissible(ctx, agentID); !ok {
			delete(s.dispatchAssignments, taskID)
		}
	}
}

// constrainDispatchCandidates applies both sides of the matching: a task waits for its assigned
// agent, and other tasks cannot steal that agent. The ordinary capability, budget, and busy gates
// still apply. A preempted quantum can remain busy until its next cooperative boundary.
// Caller holds placementMu through Schedule and admitWinner, closing the stale-candidate window.
func (s *Scheduler) constrainDispatchCandidates(
	tk *taskfabric.Task,
	cands []taskfabric.Candidate,
) []taskfabric.Candidate {
	assigned := s.dispatchAssignments[tk.ID]
	if assigned != "" {
		eligible := false
		for _, cand := range cands {
			if cand.AgentID == assigned && taskfabric.Score(tk.Capability, cand) > 0 {
				eligible = true
				break
			}
		}
		busy := s.tracker.Load(assigned) >= 1
		if s.agents != nil {
			if _, err := s.agents.Get(assigned); err == nil {
				busy = busy || !s.agents.IsIdle(assigned)
			}
		}
		if !eligible && !busy {
			// The source/scoring policy changed (for example hybrid static preference). Release
			// this pin so it cannot strand the task; other tasks' reservations still hold.
			delete(s.dispatchAssignments, tk.ID)
			assigned = ""
		}
	}
	reserved := make(map[string]string, len(s.dispatchAssignments))
	for taskID, agentID := range s.dispatchAssignments {
		reserved[agentID] = taskID
	}
	kept := make([]taskfabric.Candidate, 0, len(cands))
	for _, cand := range cands {
		if assigned != "" && cand.AgentID != assigned {
			continue
		}
		if taskID, held := reserved[cand.AgentID]; held && taskID != tk.ID {
			continue
		}
		kept = append(kept, cand)
	}
	return kept
}
