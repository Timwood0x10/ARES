package sdk

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/agentsyscall"
	tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/agent"
	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// This file wires the spawn_agent / create_task syscalls into the SDK
// path. Previously the syscalls were bound only in peer mode (cmd/ares/
// peer_mode.go: BindTools(toolBinder, kernelSyscall)), so an SDK user's agent
// never saw the tools and could not autonomously decompose a task. The SDK is
// now a peer-runtime facade over the SAME kernel the serve path uses, so the
// syscalls operate on the same agent fabric + task fabric + scheduler:
//
//	SDK runtime → agentsyscall.Kernel → sdkFabric (tasks) + agentsFabric (agents)
//	                                      → L2 router cognition (spawned peers)
//
// spawn_agent / create_task are registered into the runtime's tool registry,
// so the L2 planner's binder carries them and the registry executes them
// (spawned peers execute through the shared L2 router, cmd/ares parity).

// syscallBinder adapts the SDK tool registry to the agentsyscall.ToolBinder
// contract. BindTools only needs BindTool(name, fn); the SDK registry exposes
// Register(tool), so each syscall is wrapped in a thin tool adapter.
type syscallBinder struct {
	reg *tools.Registry
}

var _ agentsyscall.ToolBinder = (*syscallBinder)(nil)

func (b *syscallBinder) BindTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error)) {
	if b == nil || b.reg == nil {
		return
	}
	_ = b.reg.Register(&syscallTool{name: name, fn: toolFunc})
}

// syscallTool adapts a syscall handler to the api/tools.Tool interface so the
// SDK registry can carry it. Its schema mirrors the LLM-facing schema from
// agentsyscall.ToolSchemas (matched by name); when no schema exists the tool
// still executes (defensive: the registry only needs Name/Execute).
type syscallTool struct {
	name string
	fn   func(ctx context.Context, args map[string]any) (any, error)
}

var _ tools.Tool = (*syscallTool)(nil)

func (t *syscallTool) Name() string { return t.name }

func (t *syscallTool) Description() string {
	for _, s := range agentsyscall.ToolSchemas() {
		if s.Name == t.name {
			return s.Description
		}
	}
	return t.name
}

func (t *syscallTool) Parameters() map[string]interface{} {
	for _, s := range agentsyscall.ToolSchemas() {
		if s.Name == t.name {
			return s.Parameters
		}
	}
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func (t *syscallTool) Execute(ctx context.Context, params map[string]interface{}) (tools.Result, error) {
	out, err := t.fn(ctx, params)
	if err != nil {
		return tools.Result{Success: false, Data: map[string]any{"error": err.Error()}}, err
	}
	return tools.Result{Success: true, Data: out}, nil
}

func (t *syscallTool) Capabilities() []string { return nil }

// l2RouterExecutor adapts the shared L2 router cognition to the
// agentsyscall.Executor contract so a syscall-spawned peer executes exactly
// like a serve-mode peer: one planner step per quantum (never a nested wait —
// the scheduler drain is a single loop, so waiting for another task on the
// same fabric from inside a quantum would deadlock it). Mirrors cmd/ares's
// peerExecutorAdapter.
type l2RouterExecutor struct {
	id  string
	typ models.AgentType
	cog agentfabric.Cognition
}

var _ agentsyscall.Executor = (*l2RouterExecutor)(nil)

// ID returns the spawned peer's agent ID.
func (e *l2RouterExecutor) ID() string { return e.id }

// Type returns the peer's declared capability.
func (e *l2RouterExecutor) Type() models.AgentType { return e.typ }

// ExecuteStep stamps the caller identity (spawn_agent/create_task provenance
// flows through kctx) and delegates one quantum to the router cognition.
func (e *l2RouterExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*agentsyscall.StepOutcome, error) {
	if e.cog == nil {
		return nil, fmt.Errorf("sdk: spawned peer %s has no L2 router (execution core not wired)", e.id)
	}
	ctx = kctx.WithCallerID(ctx, e.id)
	out, err := e.cog.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &agentsyscall.StepOutcome{}, nil
	}
	return &agentsyscall.StepOutcome{
		Done:       out.Done,
		Result:     out.Result,
		Checkpoint: out.Checkpoint,
	}, nil
}

// wireSyscalls builds the syscall Kernel over the runtime's fabrics and binds
// spawn_agent / create_task into the tool registry. Called once from
// ensureScheduler (schedOnce) so the same fabric + scheduler that drive
// Submit also back the syscalls. Safe to call multiple times (idempotent:
// tool registry Register overwrites by name; fabric creation is cheap).
func (r *Runtime) wireSyscalls() {
	if r.sdkFabric == nil || r.sched == nil {
		return
	}
	if r.agentsFabric == nil {
		r.agentsFabric = agentfabric.NewFabric()
	}
	// Parity with the serve path: bind the loop lifetime so the
	// create_plan `loop` option works on the SDK path too. The runtime ctx is
	// cancelled in Close, so SDK plan loops cannot outlive the Runtime — the
	// same lifecycle the serve path gets from its own ctx. Without this, the
	// schema advertises a loop parameter that always failed loudly. A nil ctx
	// (never expected: New always wires rtCtx) keeps the fail-loud behavior.
	var opts []agentsyscall.KernelOption
	if r.ctx != nil {
		opts = append(opts, agentsyscall.WithLoopLifetime(r.ctx))
	}
	// Same cognitive-execution budget as the L2 peer: a syscall-spawned
	// agent is bounded from birth (zero = unlimited), cmd/ares parity.
	opts = append(opts, agentsyscall.WithAgentGovernance(r.gov))
	kernelSyscall := agentsyscall.NewKernel(
		r.agentsFabric,
		r.sdkFabric,
		// factory: a syscall-spawned peer executes through the shared L2
		// router — one planner step per quantum, exactly like cmd/ares's
		// serve-mode peers. The executor's Type is the DECLARED capability
		// (not the generated agent id) so create_task sub-tasks match the
		// peer's advertised capability. ensureL2 is safe here: the factory
		// runs lazily inside a syscall tool call, long after wireSyscalls
		// itself finished (no schedOnce re-entry).
		func(agentID, capability string) agentsyscall.Executor {
			var cog agentfabric.Cognition
			if execCore := r.ensureL2(); execCore != nil {
				cog = execCore.Router
			}
			return &l2RouterExecutor{id: agentID, typ: models.AgentType(capability), cog: cog}
		},
		func(string, agentsyscall.Executor) {
			// No static-pool registration: capability matching happens
			// through the L2 planner, and a static entry would win hybrid
			// drains while the same peer also lives in the fabric pool.
		},
		opts...,
	)
	agentsyscall.BindTools(&syscallBinder{reg: r.toolReg}, kernelSyscall)
	r.syscallTools = syscallLLMTools()
	r.syscallKernel = kernelSyscall
}

// LivePlanLoops returns the plan IDs of the loops currently running on this
// Runtime, sorted. Empty before the first Submit (the syscall kernel is wired
// lazily by ensureScheduler) and after every loop has finished.
//
// Serve-path parity: the serve path exposes loop observability through its kernel, so
// the SDK must too — otherwise a `loop` plan started via create_plan would be
// unobservable and unstoppable from the embedding program.
func (r *Runtime) LivePlanLoops() []string {
	if r.syscallKernel == nil {
		return nil
	}
	return r.syscallKernel.LivePlanLoops()
}

// StopPlanLoop cancels a live plan loop by plan ID and waits for its driver to
// exit. Unknown or already-finished plans report agentsyscall.ErrPlanLoopNotFound;
// so does a Runtime whose syscall kernel was never wired (no Submit yet).
func (r *Runtime) StopPlanLoop(planID string) error {
	if r.syscallKernel == nil {
		return fmt.Errorf("sdk: stop plan loop %q: %w", planID, agentsyscall.ErrPlanLoopNotFound)
	}
	return r.syscallKernel.StopPlanLoop(planID)
}

// syscallLLMTools converts the syscall schemas to the LLM-facing api/llmcore.Tool
// list so resolveTools can append them to every agent's tool set (the agent
// sees spawn_agent / create_task regardless of its own WithTools list).
func syscallLLMTools() []llmcore.Tool {
	schemas := agentsyscall.ToolSchemas()
	if len(schemas) == 0 {
		return nil
	}
	out := make([]llmcore.Tool, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, llmcore.Tool{
			Type: "function",
			Function: llmcore.FunctionDefinition{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		})
	}
	return out
}

// helper: the factory needs an AgentOption; an empty tool set keeps the
// spawned agent minimal (its LLM tool list still carries the syscall tools
// via resolveTools).
