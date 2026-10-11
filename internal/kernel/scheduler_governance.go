package kernel

import (
	"context"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// WithGovernance attaches the budget provider (agentfabric.Fabric). It is
// wired by the kernel lifecycle once the agent fabric exists; without it the
// scheduler enforces nothing (backward compatible with tests and minimal
// wiring). The provider is read-only here — the scheduler checks and consumes,
// it never mutates budgets.
func (s *Scheduler) WithGovernance(g *agentfabric.Fabric) *Scheduler {
	s.governance = g
	return s
}

// admissible reports whether the winning agent may start a new quantum and,
// when it may not, why (agentfabric.StopReason). It is the pre-quantum gate:
// deadline first (a deadline-expired agent is dead weight), then the budgets
// for this quantum, then the agent's budget RESERVE (the headroom it must keep
// to finish what it starts). A denial is a cooperative yield — the scheduler
// reclaims the task to READY instead of burning a quantum the agent cannot
// afford, and the reason travels with the release event so "why did this task
// stop moving" is answerable from the log.
//
// All of that lives in agentfabric.Admissibility, deliberately: the
// pre-schedule candidate filter (filterBudgetAffordable) and the post-acquire
// gate below call the SAME predicate, so a candidate can never pass one and
// fail the other — the drift that once turned a sole exhausted candidate into
// an acquire/release livelock.
func (s *Scheduler) admissible(winner string) (bool, agentfabric.StopReason) {
	if s.governance == nil {
		return true, agentfabric.StopUnknown
	}
	ok, reason, err := s.governance.Admissibility(winner)
	if err != nil {
		// Unknown agent (not spawned via the fabric) → don't block. Debug,
		// not Error: an ungoverned candidate is a legitimate wiring mode
		// (static SDK registry), not a fault.
		log.Debug(context.Background(), "admissible",
			"kernel scheduler: agent not governed by the fabric, admitting",
			"agent", winner, "error", err)
		return true, agentfabric.StopUnknown
	}
	if !ok {
		// The per-candidate detail behind the aggregated
		// no_affordable_candidate line: WHICH agent was refused and WHY.
		// Debug because this runs per candidate per drain.
		log.Debug(context.Background(), "admissible",
			"kernel scheduler: agent inadmissible for the next quantum",
			"agent", winner, "reason", reason.String())
	}
	return ok, reason
}

// consumeBudget records the winning agent's quantum consumption — the
// quantum's LLM tokens plus 1 tool round — after a completed quantum. Errors
// (budget exceeded mid-quantum) are logged, not fatal: the task already ran;
// the next quantum's gate stops further work.
//
// tokens is the per-quantum LLM spend read from the step result metadata
// (tokenUsageFromResult), NOT the cumulative session total: the governance
// counter accumulates across quanta itself, so feeding it the cumulative
// value would square the growth.
func (s *Scheduler) consumeBudget(winner string, tokens int) {
	if s.governance == nil {
		return
	}
	if err := s.governance.ConsumeResource(winner, tokens, 1); err != nil {
		// The overshoot is clamped inside the fabric, so this is not fatal —
		// but it is the moment the agent crossed its budget, and the next
		// quantum will be refused. Logged with the agent and the spend so the
		// denial that follows is attributable to this line.
		log.Warn(context.Background(), "consumeBudget",
			"kernel scheduler: agent budget consumption overshot (clamped)",
			"agent", winner, "tokens", tokens, "error", err)
	}
}

// filterBudgetAffordable drops candidates the agentfabric admission predicate
// rejects — deadline, hard exhaustion, or reserve. Filtering happens BEFORE
// Schedule acquires a lease: the post-acquire gate releases the task back to
// READY, and with a sole excluded candidate that release loop became a silent
// acquire/release livelock — every drain re-acquired the lease, hit the gate,
// released, and repeated, appending two durable events per poll for a task
// that could not run. Filtering first leaves the task in the throttled "no
// admissible candidate" wait state until another agent appears or the excluded
// one is replaced (budget counters reset only on respawn).
func (s *Scheduler) filterBudgetAffordable(cands []taskfabric.Candidate) []taskfabric.Candidate {
	if s.governance == nil {
		return cands
	}
	affordable := make([]taskfabric.Candidate, 0, len(cands))
	for _, cand := range cands {
		if ok, _ := s.admissible(cand.AgentID); ok {
			affordable = append(affordable, cand)
		}
	}
	return affordable
}
