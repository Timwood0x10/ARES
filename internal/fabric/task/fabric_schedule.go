package taskfabric

import (
	"time"
)

// Schedule picks the best capable candidate for a task and acquires it on its
// behalf (design §8: capability-aware scheduling — "who is the best executor",
// not merely "who is idle"). The Scheduler orchestrates
// uniformly — ReadyTasks → Schedule → execute; idle agents Steal → Acquire.
// The scoring (capability overlap × (1-load) × confidence) comes from
// scheduler.go; Experience supplies confidence.
//
// Args:
//   - taskID: the task id.
//   - candidates: the agents competing to execute the task.
//   - ttl: the lease TTL granted to the winner.
//
// Returns:
//   - string: the winning agent id.
//   - uint64: the fencing token (lease epoch) the winner must present on
//     subsequent ownership-carrying operations.
//   - error: ErrNoCapableCandidate / ErrTaskNotFound / ErrTaskNotReady.
func (f *Fabric) Schedule(taskID string, candidates []Candidate, ttl time.Duration) (string, uint64, error) {
	t, err := f.Task(taskID)
	if err != nil {
		return "", 0, err
	}
	// Design §8 (Skill-first): the experience prior supplies confidence for
	// candidates that do not declare one — Score's Confidence comes from the
	// wired ConfidenceSource (ares_skills.Experience BestMatch SuccessRate).
	//
	// The fill condition is the READ half of the M4.4 loop closure: the
	// kernel scheduler pre-fills every candidate with the tracker's
	// ConfidenceFor (an explicit value, or the NEUTRAL 1.0 when the agent
	// has no execution history). A neutral 1.0 is "no opinion", not a
	// measurement, so it must NOT mask a recorded experience prior — but a
	// <= 0 check can never distinguish them. ConfidenceForMeasured on the
	// tracker reports whether the value is measured; the scheduler zeroes
	// unmeasured candidates so the prior fills them here. A MEASURED value
	// (>= or < prior) always wins: live feedback outranks stale priors.
	f.mu.Lock()
	src := f.confidence
	f.mu.Unlock()
	if src != nil {
		if conf := src.Confidence(t.Capability); conf > 0 {
			for i := range candidates {
				if candidates[i].Confidence <= 0 {
					candidates[i].Confidence = conf
				}
			}
		}
	}
	best := Pick(t.Capability, candidates)
	if best == nil {
		return "", 0, ErrNoCapableCandidate
	}
	epoch, err := f.Acquire(taskID, best.AgentID, ttl)
	if err != nil {
		return "", 0, err
	}
	return best.AgentID, epoch, nil
}

// Preempt cooperatively preempts a RUNNING task at a quantum boundary
// (architecture invariant #9: cooperative — never OS-style hard preemption).
// The task returns to READY with its checkpoint preserved, so another agent
// can acquire and resume it. The priority comparison itself is the caller's
// (Scheduler's) decision — Preempt is the primitive that hands the task back
// at the boundary; the fencing token ensures only the current holder can
// preempt its own task.
//
// Args:
//   - taskID: the task id.
//   - agentID: the preempting agent (must hold the lease).
//   - epoch: the fencing token returned by Acquire.
//   - reason: debug reason for the preemption (recorded in the event).
//
// Returns:
//   - error: ErrNotOwner / ErrEpochMismatch / ErrIllegalState.
func (f *Fabric) Preempt(taskID, agentID string, epoch uint64, reason string) error {
	pending := make([]*pendingAppend, 0, 1)
	f.mu.Lock()
	defer f.flushAppends(&pending)
	defer f.mu.Unlock()
	t, err := f.ownerLocked(taskID, agentID, epoch)
	if err != nil {
		return err
	}
	if err := t.transition(StateReady); err != nil {
		return err
	}
	t.Owner = ""
	t.Lease = nil
	pending = append(pending, f.recordLocked(t, EventTaskPreempted))
	return nil
}
