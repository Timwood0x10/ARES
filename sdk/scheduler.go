package sdk

import (
	"context"
	"errors"
	"time"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/kernel"
)

// This file wires the SDK onto the shared kernel scheduler over its own Task
// Fabric. Since the B3 convergence there is ONE execution path: every
// submission is an L2 session through the shared agentruntime execution core
// (submitThroughL2) — the same router cognition serve/start use. The
// scheduler drains the fabric (plan / tool / answer nodes); no executor runs
// a private, fully-blocking loop inside a quantum.
//
// The single-loop constraint is why no static-executor branch exists: the
// drain executes quanta serially on one goroutine, so a quantum that waited
// for another task on the same fabric (the pre-B3 ReAct executor nested
// inside Submit) would deadlock the loop that must schedule its answer.

// ensureScheduler lazily starts the shared scheduler over the runtime's own
// Task Fabric. It runs exactly once; subsequent calls reuse the started
// scheduler. The scheduler goroutine lives until Runtime.Close cancels
// schedCtx.
func (r *Runtime) ensureScheduler() {
	r.schedOnce.Do(func() {
		r.sdkFabric = taskfabric.NewFabric()
		r.schedCtx, r.schedCancel = context.WithCancel(context.Background())
		r.sched = kernel.New(r.sdkFabric, r.sdkExecutors, nil)
		r.sched.PollInterval = 20 * time.Millisecond
		// the SDK is a peer-runtime facade — wire the same kernel
		// syscalls (spawn_agent/create_task) into the tool registry so SDK
		// users can autonomously decompose tasks. Registered after sched
		// exists because the syscall Kernel needs the shared fabric + sched.
		r.wireSyscalls()
		// Attach the agent fabric (created by wireSyscalls) and hybrid
		// candidate mode BEFORE the drain loop starts: the scheduler's
		// With* setters are plain field writes, safe only against a not-yet
		// started drain (cmd/ares wires them in the kernel lifecycle before
		// Run). An empty fabric behaves exactly like no fabric — static
		// executors keep running — and ensureL2's Spawn later joins the L2
		// peer to the SAME pool through the thread-safe fabric API.
		r.sched.WithAgentFabric(r.agentsFabric).WithStaticPoolHybrid()
		// Governance enforcement (WithAgentGovernance): the scheduler checks
		// the L2 peer's token/tool budgets and deadline at quantum
		// boundaries (cmd/ares parity). Wired here, before the drain starts.
		r.sched.WithGovernance(r.agentsFabric)
		go r.sched.Run(r.schedCtx)
	})
}

// submitThroughScheduler dispatches a submission through the shared L2
// execution core and waits for the session's terminal answer. Since the B3
// convergence there is no static-executor branch: every agent run IS an L2
// session (Agent.Run routes here conceptually), and an executor that waited
// for another task inside a scheduler quantum would deadlock the single-loop
// drain. A runtime without an LLM can never build the core and refuses
// loudly instead of silently falling back to a dead path.
func (r *Runtime) submitThroughScheduler(ctx context.Context, t Task) (*Result, error) {
	r.ensureScheduler()
	execCore := r.ensureL2()
	if execCore == nil {
		return nil, errors.New("sdk: submit requires the L2 execution core (no LLM configured?)")
	}
	// Pick up tools registered after the peer was spawned, so a late tool
	// is both visible to the planner and routable as a tool/* node. No-op
	// once the sets have converged.
	r.resyncL2Tools()
	return r.submitThroughL2(ctx, execCore, t)
}
