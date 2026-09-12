package kernel

import (
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

// budgetOK reports whether the winning agent may start a new quantum. It is
// the pre-quantum gate: deadline first (a deadline-expired agent is dead
// weight), then the budgets for this quantum. A denial is a cooperative
// yield — the scheduler returns the task to READY instead of burning a
// quantum the agent cannot afford.
//
// The token request is 1, not 0: CheckResource's contract is
// `used + request <= budget`, so a 0-token probe can never observe an
// exhausted token budget. Asking for 1 nominal token makes an agent whose
// tokenUsed has reached TokenBudget fail the gate (its next quantum can only
// spend more), which is what stops a long task from running forever. The tool
// request stays 1 (the quantum's expected one tool round).
func (s *Scheduler) budgetOK(winner string) bool {
	if s.governance == nil {
		return true
	}
	if over, err := s.governance.DeadlineExceeded(winner); err == nil && over {
		return false
	}
	ok, err := s.governance.CheckResource(winner, 1, 1)
	if err != nil {
		return true // unknown agent (not spawned via fabric) → don't block
	}
	return ok
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
		log.Warn("kernel scheduler: agent budget consumption", "agent", winner, "tokens", tokens, "error", err)
	}
}

// filterBudgetAffordable drops candidates whose governance budget or
// deadline is exhausted. Filtering happens BEFORE Schedule acquires a lease:
// the post-acquire budget gate releases the task back to READY, and with a
// sole exhausted candidate that release loop became a silent acquire/release
// livelock — every drain re-acquired the lease, hit the gate, released, and
// repeated, appending two durable events per poll for a task that could not
// run. Filtering first leaves the task in the throttled "no capable
// candidate" wait state until another capable agent appears or the
// exhausted one is replaced (budget counters reset only on respawn).
func (s *Scheduler) filterBudgetAffordable(cands []taskfabric.Candidate) []taskfabric.Candidate {
	if s.governance == nil {
		return cands
	}
	affordable := make([]taskfabric.Candidate, 0, len(cands))
	for _, cand := range cands {
		if s.budgetOK(cand.AgentID) {
			affordable = append(affordable, cand)
		}
	}
	return affordable
}
