package agentruntime

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/kernel"
	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
)

// RouterExecutor adapts an assembled execution core's L2 router to the scheduler's
// CapabilityExecutor contract.
//
// It exists here rather than being reused from cmd/ares: serve has an equivalent adapter
// (peerExecutorAdapter), but it lives in package main and is therefore invisible to every
// other assembly — including this one, which needs exactly the same three-line
// translation for a candidate-scoped fabric.
//
// The identity stamp is not decoration. Tool nodes call the binder with this context and
// the Kernel syscalls (spawn_agent / create_task) enforce provenance from
// ctx.CallerID(); without it the executing agent's identity would fall back to whatever
// the LLM supplied, which is forgeable.
type RouterExecutor struct {
	id         string
	capability models.AgentType
	router     agentfabric.Cognition
}

// NewRouterExecutor binds one identity/capability to a core's router.
//
// It fails loud on a core without a router: an executor that cannot execute would let
// the scheduler lease a task to a body that does nothing, and the case would stall rather
// than report a missing piece.
func NewRouterExecutor(exec *Execution, id string, capability models.AgentType) (*RouterExecutor, error) {
	if exec == nil || exec.Router == nil {
		return nil, fmt.Errorf("agentruntime: router executor requires an assembled execution core with a router")
	}
	if id == "" {
		return nil, fmt.Errorf("agentruntime: router executor requires an identity (provenance is stamped from it)")
	}
	return &RouterExecutor{id: id, capability: capability, router: exec.Router}, nil
}

// ID implements kernel.CapabilityExecutor.
func (e *RouterExecutor) ID() string { return e.id }

// Type implements kernel.CapabilityExecutor.
func (e *RouterExecutor) Type() models.AgentType { return e.capability }

// ExecuteStep implements kernel.CapabilityExecutor: it stamps this executor's identity
// and delegates one quantum to the router, translating the outcome to the scheduler's
// StepOutcome shape (the two types differ field-for-field, so one struct cannot satisfy
// both contracts).
func (e *RouterExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	ctx = kctx.WithCallerID(ctx, e.id)
	out, err := e.router.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &sub.StepOutcome{}, nil
	}
	return &sub.StepOutcome{Done: out.Done, Checkpoint: out.Checkpoint, Result: out.Result}, nil
}

// DefaultCandidateExecutors builds the minimum executor set a candidate case needs: one
// plan-capable body backed by the core's router.
//
// It is the default so that a caller with nothing special to say gets a runnable
// evaluation environment by setting only the chat client and the tool binder, while a
// caller that needs more capabilities (tool peers, a second identity) can extend the map
// returned here. Both halves matter: a default that did not exist would make every caller
// re-derive this adapter, and a default that could not be extended would freeze the
// evaluation environment at one capability.
func DefaultCandidateExecutors(exec *Execution) (map[string]kernel.CapabilityExecutor, error) {
	plan, err := NewRouterExecutor(exec, "candidate-plan", PlanCapability)
	if err != nil {
		return nil, err
	}
	return map[string]kernel.CapabilityExecutor{"candidate-plan": plan}, nil
}
