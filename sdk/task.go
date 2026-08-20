package sdk

import (
	"context"
	"time"
)

// Task is the minimal unit of work a caller submits to a Runtime
// (aresos-agentos-plan H1: 极简 SDK — NewRuntime → RegisterAgent → Submit →
// 结果). It is deliberately field-light: the Runtime resolves the executor by
// capability, so callers never construct agents or reference any internal
// scheduling/leadership concept.
type Task struct {
	// ID is an optional caller-provided identifier. When empty, the Runtime
	// assigns one.
	ID string
	// Capability selects the registered agent that can handle this task
	// (exact match on the capability the agent was registered with). When
	// empty, any registered agent may handle it.
	Capability string
	// Input is the task content passed to the agent.
	Input string
	// Timeout caps the total wall-clock duration (<=0 = no limit).
	Timeout time.Duration
}

// RegisterAgent creates an agent and registers it as the handler for its
// capability (H1: 极简 SDK — 不暴露 leader/sub/kernel 概念). The agent is
// named after the capability; opts configure it (WithInstruction/WithTools/
// ...). The first agent registered for a capability wins; a later
// RegisterAgent for the same capability does not replace it.
//
// The returned *Agent is fully configurable via opts and can also be run
// directly via Run — Submit is the uniform entry point, not the only one.
//
// Capability must be non-empty: an agent with no capability is not reachable
// by Submit.
func (r *Runtime) RegisterAgent(capability string, opts ...AgentOption) *Agent {
	if capability == "" {
		capability = "agent"
	}
	a := r.NewAgent(capability, opts...)
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if _, ok := r.agentByCapability[capability]; !ok {
		r.agentByCapability[capability] = a
	}
	return a
}

// Submit dispatches a task to the agent registered for its capability and
// returns the execution result (H1: 极简 SDK 闭环). When no agent is
// registered for the task's capability, a capability-named agent is created
// on demand and run — a runtime never refuses a well-formed task just because
// it was not pre-registered.
//
// Timeout, when > 0, bounds the execution via context deadline. The returned
// error wraps the agent's execution error; context cancellation surfaces as
// context.Canceled/DeadlineExceeded (code_rules_v2 §3.1).
func (r *Runtime) Submit(ctx context.Context, t Task) (*Result, error) {
	a := r.lookupAgent(t.Capability)
	if a == nil {
		cap := t.Capability
		if cap == "" {
			cap = "agent"
		}
		a = r.NewAgent(cap)
	}
	if t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}
	res, err := a.Run(ctx, t.Input)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// lookupAgent returns the agent registered for the capability, or nil. An
// empty capability returns any registered agent (the map iteration order is
// unspecified but stable within a process; prefer an explicit capability).
func (r *Runtime) lookupAgent(capability string) *Agent {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if capability != "" {
		return r.agentByCapability[capability]
	}
	for _, a := range r.agentByCapability {
		return a
	}
	return nil
}
