package agentfabric

import (
	"errors"
	"fmt"
	"time"
)

// Resource governance — Agent Runtime governance, NOT cgroups.
//
// The Kernel governs cognitive-execution
// budgets, not CPU timeslices. An agent runs with:
//
//	Agent A: token budget = 50k, tool budget = 100, deadline = 10m
//
// The Kernel enforces:
//
//	if budget.exceeded:   yield()          // cooperative, at quantum boundary
//	if deadline:         suspend / fail / handoff
//	if lease.expired:    requeue
//
// This file adds the budget side. Lease expiry is already covered by the
// taskfabric/recovery chain; cgroup-style CPU isolation is explicitly out of
// scope.

// Governance is an agent's cognitive-execution budget. Zero values mean
// "unlimited" for that dimension. It is set at spawn time and read-only for
// the agent's lifetime (change via Retire+Spawn or ResetResource for the
// consumption counters).
type Governance struct {
	// TokenBudget is the max tokens an agent may consume across its life.
	TokenBudget int
	// ToolBudget is the max tool calls an agent may make.
	ToolBudget int
	// Deadline is the wall-clock lifetime from spawn. Zero = no deadline.
	Deadline time.Duration
	// MinRemainingRatio is the share of a budget that must STILL be unspent
	// before the agent will accept another quantum, expressed as a fraction
	// of that dimension's budget (0 < r <= 1; 0 = disabled).
	//
	// It exists so an agent stops taking work while it still has room, rather
	// than spending its last tokens on a quantum it cannot finish: at
	// TokenBudget=500 and MinRemainingRatio=0.4 the agent accepts work only
	// while 200+ tokens remain, and at the boundary the task it holds is
	// reclaimed (released to READY) for a fresher agent to resume from the
	// preserved checkpoint. This is admission control, not execution
	// preemption — a running quantum is never interrupted.
	//
	// Dimensions with a zero budget are unlimited and never reserve. The
	// ratio applies to whichever limited dimension is closest to its floor.
	MinRemainingRatio float64
}

// ErrResourceExceeded is returned by ConsumeResource when a budget is
// exhausted; callers (the scheduler at a quantum boundary) should treat it as
// a cooperative yield signal, not a crash.
var ErrResourceExceeded = errors.New("agentfabric: resource budget exceeded")

// governanceState is the per-agent consumption counters, guarded by Agent.mu.
type governanceState struct {
	cfg       Governance
	tokenUsed int
	toolUsed  int
	deadline  time.Time // zero if no deadline
}

// CheckResource reports whether the agent may consume the given token/tool
// amounts in the NEXT quantum WITHOUT recording consumption. It is the
// scheduler's pre-quantum gate (validate before execute).
// ok=false means at least one budget would be exceeded.
func (f *Fabric) CheckResource(agentID string, token, tool int) (ok bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, found := f.agents[agentID]
	if !found {
		return false, ErrAgentNotFound
	}
	g, err := a.governanceLocked()
	if err != nil {
		return false, err
	}
	if g.cfg.TokenBudget > 0 && g.tokenUsed+token > g.cfg.TokenBudget {
		return false, nil
	}
	if g.cfg.ToolBudget > 0 && g.toolUsed+tool > g.cfg.ToolBudget {
		return false, nil
	}
	return true, nil
}

// Admissibility reports whether the agent may start another quantum and, when
// it may not, WHY (see StopReason). It is the SINGLE admission predicate: the
// scheduler's pre-schedule candidate filter and its post-acquire quantum gate
// both call it, so the two cannot disagree about who may run. Before this
// existed the two gates each re-derived affordability, and the reserve was
// expressible in neither — a budget-starved agent was indistinguishable from a
// missing executor.
//
// Precedence, and why:
//  1. Deadline — an agent past its wall-clock lifetime is dead weight; its
//     remaining budget is irrelevant.
//  2. Hard exhaustion — a dimension is fully spent (used + 1 > budget). The
//     request is 1 token, not 0: CheckResource's contract is
//     `used + request <= budget`, so a 0-token probe can never observe an
//     exhausted budget.
//  3. Reserve — the remaining fraction of a limited dimension dropped below
//     Governance.MinRemainingRatio.
//
// An error means "cannot decide" (unknown agent, unreadable governance): the
// fabric does not govern that body, so callers must ADMIT rather than block.
// ok is true in that case, so a caller that ignores err still behaves as
// before.
func (f *Fabric) Admissibility(agentID string) (ok bool, reason StopReason, err error) {
	if over, dErr := f.DeadlineExceeded(agentID); dErr != nil {
		return true, StopUnknown, dErr
	} else if over {
		return false, StopDeadlineExceeded, nil
	}
	if affordable, cErr := f.CheckResource(agentID, 1, 1); cErr != nil {
		return true, StopUnknown, cErr
	} else if !affordable {
		return false, StopBudgetExhausted, nil
	}
	exhausted, reason, rErr := f.reserveExhausted(agentID)
	if rErr != nil {
		return true, StopUnknown, rErr
	}
	if exhausted {
		return false, reason, nil
	}
	return true, StopUnknown, nil
}

// reserveExhausted reports whether the agent's REMAINING budget on any limited
// dimension has fallen below its configured reserve (MinRemainingRatio): the
// agent could still afford one more quantum, but only by spending headroom it
// needs to finish what it would start.
//
// A disabled reserve (ratio <= 0) and unlimited dimensions (budget == 0) never
// trip — the legacy behaviour is exactly preserved when the knob is unset.
func (f *Fabric) reserveExhausted(agentID string) (bool, StopReason, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, found := f.agents[agentID]
	if !found {
		return false, StopUnknown, ErrAgentNotFound
	}
	g, err := a.governanceLocked()
	if err != nil {
		return false, StopUnknown, err
	}
	ratio := g.cfg.MinRemainingRatio
	if ratio <= 0 {
		return false, StopUnknown, nil
	}
	if g.cfg.TokenBudget > 0 {
		remaining := float64(g.cfg.TokenBudget - g.tokenUsed)
		if remaining < ratio*float64(g.cfg.TokenBudget) {
			return true, StopBudgetReserved, nil
		}
	}
	if g.cfg.ToolBudget > 0 {
		remaining := float64(g.cfg.ToolBudget - g.toolUsed)
		if remaining < ratio*float64(g.cfg.ToolBudget) {
			return true, StopBudgetReserved, nil
		}
	}
	return false, StopUnknown, nil
}

// ConsumeResource records token/tool consumption. It returns
// ErrResourceExceeded when a budget is exhausted — the cooperative yield
// signal. When that happens the over-budget dimension is CLAMPED to its
// budget rather than left unrecorded: the quantum already spent those tokens,
// and the scheduler's pre-quantum gate (CheckResource) keys on
// `used >= budget` to stop further work. Refusing to record (the old
// behavior) left `used` below the budget forever, so the gate never fired and
// a runaway agent could not be stopped. Dimensions with a zero budget are
// unlimited and never rejected.
func (f *Fabric) ConsumeResource(agentID string, token, tool int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, found := f.agents[agentID]
	if !found {
		return ErrAgentNotFound
	}
	g, err := a.governanceLocked()
	if err != nil {
		return err
	}
	if token > 0 {
		if g.cfg.TokenBudget > 0 && g.tokenUsed+token > g.cfg.TokenBudget {
			used := g.tokenUsed
			g.tokenUsed = g.cfg.TokenBudget
			return fmt.Errorf("%w: token budget %d (used %d + want %d)",
				ErrResourceExceeded, g.cfg.TokenBudget, used, token)
		}
		g.tokenUsed += token
	}
	if tool > 0 {
		if g.cfg.ToolBudget > 0 && g.toolUsed+tool > g.cfg.ToolBudget {
			used := g.toolUsed
			g.toolUsed = g.cfg.ToolBudget
			return fmt.Errorf("%w: tool budget %d (used %d + want %d)",
				ErrResourceExceeded, g.cfg.ToolBudget, used, tool)
		}
		g.toolUsed += tool
	}
	return nil
}

// DeadlineExceeded reports whether the agent's wall-clock deadline (if any)
// has passed. The scheduler checks this at quantum boundaries to suspend/fail
// a runaway agent instead of preempting it mid-reasoning.
func (f *Fabric) DeadlineExceeded(agentID string) (exceeded bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, found := f.agents[agentID]
	if !found {
		return false, ErrAgentNotFound
	}
	g, err := a.governanceLocked()
	if err != nil {
		return false, err
	}
	if g.cfg.Deadline <= 0 {
		return false, nil
	}
	return !f.now().Before(g.deadline), nil
}

// governanceLocked reads the agent's governance state under Agent.mu. Callers
// must hold Fabric.mu (registry lock). Every agent has a governance state from
// birth (zero-value = unlimited), so this never fails.
func (a *Agent) governanceLocked() (*governanceState, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.governance, nil
}

// BudgetUsage returns the current consumption (tokens/tools) for observability
// and audit.
func (f *Fabric) BudgetUsage(agentID string) (token, tool int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, found := f.agents[agentID]
	if !found {
		return 0, 0, ErrAgentNotFound
	}
	g, err := a.governanceLocked()
	if err != nil {
		return 0, 0, err
	}
	return g.tokenUsed, g.toolUsed, nil
}
