package agentsyscall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/kernelctx"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// SpawnAgentTool is the tool name exposed to the LLM for spawning peer agents.
const SpawnAgentTool = "spawn_agent"

// CreateTaskTool is the tool name exposed to the LLM for creating sub-tasks.
const CreateTaskTool = "create_task"

// goconst: these strings appear ≥3 times in ToolSchemas,
const (
	paramType        = "type"
	paramTypeString  = "string"
	paramTypeObject  = "object"
	paramTypeArray   = "array"
	paramDescription = "description"
	paramCapability  = "capability"

	// Schema object keys reused across tool schemas (goconst).
	paramProperties = "properties"
	paramItems      = "items"
	paramRequired   = "required"
)

// ExecutorFactory creates a sub.Agent executor for a dynamically spawned agent.
// The factory is injected by the serve wiring so spawned agents get the same
// LLM + tool capabilities as configured agents.
type ExecutorFactory func(agentID, capability string) Executor

// Executor is the minimal contract a spawned agent's executor must satisfy.
// In production this is sub.Agent; the interface keeps this package decoupled
// from the sub package (code_rules: interface at the consumer).
type Executor interface {
	ID() string
	Type() models.AgentType
	ExecuteStep(ctx context.Context, task *models.Task) (*StepOutcome, error)
}

// StepOutcome mirrors sub.StepOutcome to avoid a circular dependency.
type StepOutcome struct {
	Done       bool
	Checkpoint any
	Result     *models.TaskResult
}

// cognitionFunc adapts an Executor to the agentfabric.Cognition contract
// (aresos-agentos-plan C1: spawn 的 agent 带执行体). It converts the syscall
// StepOutcome shape to the fabric one — the underlying quantum is the same
// executor, so semantics are preserved by construction.
func cognitionFunc(executor Executor) agentfabric.Cognition {
	return agentfabric.CognitionFunc(func(ctx context.Context, task *models.Task) (*agentfabric.StepOutcome, error) {
		out, err := executor.ExecuteStep(ctx, task)
		if err != nil {
			return nil, err
		}
		return &agentfabric.StepOutcome{
			Done:       out.Done,
			Checkpoint: out.Checkpoint,
			Result:     out.Result,
		}, nil
	})
}

// RegisterExecutorFn registers a dynamically spawned executor into the
// scheduler so it can be selected for task execution. This is the same
// method as kernelScheduler.RegisterExecutor.
type RegisterExecutorFn func(agentID string, executor Executor)

// Kernel is the ensemble of fabric-level subsystems the syscalls operate on.
// It is the "Kernel enforces" surface: the syscalls validate against the
// agent fabric (quota/capability) and the task fabric (task creation).
type Kernel struct {
	agents   *agentfabric.Fabric
	fabric   *taskfabric.Fabric
	factory  ExecutorFactory
	register RegisterExecutorFn
	// idSeq generates unique agent IDs for auto-named spawns.
	idSeq atomic.Int64
}

// NewKernel creates a syscall Kernel over the given fabrics. The factory and
// register function are optional: without them, spawn creates the agent in
// the fabric but cannot register it as an executor (the agent exists but
// cannot execute tasks — useful for provenance-only spawns). In production
// both are wired so a spawned agent immediately becomes schedulable.
func NewKernel(
	agents *agentfabric.Fabric,
	fabric *taskfabric.Fabric,
	factory ExecutorFactory,
	register RegisterExecutorFn,
) *Kernel {
	return &Kernel{
		agents:   agents,
		fabric:   fabric,
		factory:  factory,
		register: register,
	}
}

// SpawnAgentArgs carries the LLM-provided arguments for the spawn_agent tool.
// The LLM decides the capability, the task context, and optionally the
// resource hints. The Kernel validates them.
type SpawnAgentArgs struct {
	// Capability is the declared capability of the new agent (e.g. "coder",
	// "reviewer"). Required — the scheduler matches tasks to agents by this.
	Capability string `json:"capability"`
	// TaskContext is the shared task state the new agent starts with. This
	// is the parent's projection of the task goal/constraints — never the
	// parent's private reasoning state.
	TaskContext map[string]any `json:"task_context,omitempty"`
	// Resources are optional resource hints for quota validation.
	Resources map[string]any `json:"resources,omitempty"`
	// ParentID is the spawning agent's ID (for provenance). Kernel-enforced:
	// when the tool context carries a caller (kernelctx.CallerID), that ID
	// wins and this field is ignored, so an LLM can never forge parentage;
	// the value is used only for direct/Kernel-internal calls without a
	// context identity. Empty parent = root spawn.
	ParentID string `json:"parent_id,omitempty"`
}

// SpawnAgentResult is the return value of the spawn_agent tool — what the
// LLM sees after the Kernel processes its spawn request.
type SpawnAgentResult struct {
	// AgentID is the identity of the newly created agent.
	AgentID string `json:"agent_id"`
	// Capability confirms the declared capability.
	Capability string `json:"capability"`
	// Registered reports whether the agent was registered as a scheduler
	// executor (false when no factory/register was wired).
	Registered bool `json:"registered"`
}

// SpawnAgent is the Kernel syscall behind the spawn_agent tool. It:
//  1. Validates the spec (non-empty capability, quota check via agentfabric).
//  2. Creates the Agent in the agent fabric (provenance link recorded).
//  3. If a factory + register function are wired, creates an executor and
//     registers it so the scheduler can drive tasks to the new agent.
//  4. Optionally creates a Task Fabric task if CreateTaskArgs is non-nil.
//
// The LLM calls this via the tool binder; the Kernel enforces safety.
func (k *Kernel) SpawnAgent(ctx context.Context, args SpawnAgentArgs) (*SpawnAgentResult, error) {
	if k.agents == nil {
		return nil, errors.New("agentsyscall: agent fabric not wired")
	}
	if args.Capability == "" {
		return nil, errors.New("agentsyscall: capability is required")
	}

	// Generate a unique agent ID when the LLM does not provide one.
	agentID := fmt.Sprintf("spawned-%s-%d", args.Capability, k.idSeq.Add(1))

	// C1: when an executor factory is wired, create the executor BEFORE spawn
	// and inject it as the agent's CognitionFactory so the spawned agent is a
	// REAL executable body (not just a provenance record) from birth. The
	// factory is called exactly once — the same executor instance is reused
	// for the scheduler registration below (code_rules: no second
	// executor copy).
	var executor Executor
	if k.factory != nil {
		executor = k.factory(agentID, args.Capability)
	}

	// Kernel-enforced provenance: the tool-context caller wins over any
	// LLM-supplied ParentID, so parentage can never be forged by a spawned
	// agent's arguments. The fallback keeps direct/Kernel-internal calls
	// (bootstrap wiring, existing syscall tests) working with no context
	// identity.
	parentID := args.ParentID
	if caller := kernelctx.CallerID(ctx); caller != "" {
		parentID = caller
	}

	spec := agentfabric.SpawnSpec{
		Identity:     agentID,
		Capabilities: []string{args.Capability},
		ParentID:     parentID,
		TaskContext:  args.TaskContext,
		Resources:    args.Resources,
	}
	if executor != nil {
		spec.CognitionFactory = func([]string) agentfabric.Cognition {
			return cognitionFunc(executor)
		}
	}

	agent, err := k.agents.Spawn(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("agentsyscall: spawn failed: %w", err)
	}

	registered := false
	if executor != nil && k.register != nil {
		k.register(agent.Identity, executor)
		registered = true
		log.Printf("agentsyscall: spawned agent %q (%s) registered as executor", agent.Identity, args.Capability)
	}

	if !registered {
		log.Printf("agentsyscall: spawned agent %q (%s) not registered (no factory)", agent.Identity, args.Capability)
	}

	return &SpawnAgentResult{
		AgentID:    agent.Identity,
		Capability: args.Capability,
		Registered: registered,
	}, nil
}

// CreateTaskArgs carries the LLM-provided arguments for the create_task tool.
// The LLM decides the task's capability, priority, and dependencies — this
// is the cognition layer's decomposition output.
type CreateTaskArgs struct {
	// Capability is the required capability for this task. The scheduler
	// scores agents against this.
	Capability string `json:"capability"`
	// Priority drives preemption (higher wins).
	Priority int `json:"priority,omitempty"`
	// Dependencies lists prerequisite task IDs (DAG gate).
	Dependencies []string `json:"dependencies,omitempty"`
	// Payload carries opaque task data (e.g. task_desc, profile).
	Payload map[string]any `json:"payload,omitempty"`
	// NOTE: there is deliberately no "creator" argument. The Kernel stamps
	// Task.Origin from the tool context (kernelctx.CallerID) so provenance
	// is enforced by the Kernel — an LLM cannot forge a creator via params.
}

// CreateTaskResult is the return value of the create_task tool.
type CreateTaskResult struct {
	TaskID string `json:"task_id"`
	State  string `json:"state"`
}

// CreateTask is the Kernel syscall behind the create_task tool. It creates
// a real Task Fabric task (Create → READY) so the scheduler can pick it up
// and execute it via the normal Schedule → Acquire → RunQuantum path.
func (k *Kernel) CreateTask(ctx context.Context, args CreateTaskArgs) (*CreateTaskResult, error) {
	if k.fabric == nil {
		return nil, errors.New("agentsyscall: task fabric not wired")
	}
	if args.Capability == "" {
		return nil, errors.New("agentsyscall: capability is required")
	}

	taskID := fmt.Sprintf("task-%s-%d", args.Capability, k.idSeq.Add(1))

	task := &taskfabric.Task{
		ID:           taskID,
		Capability:   args.Capability,
		Priority:     args.Priority,
		Dependencies: args.Dependencies,
		RetryPolicy:  taskfabric.RetryPolicy{MaxRetries: 2},
		// Origin is Kernel-enforced: stamped from the tool context caller
		// (kernelctx.CallerID), never from LLM-supplied arguments. Empty =
		// root call (no agent caller in context).
		Origin: kernelctx.CallerID(ctx),
		Checkpoint: &taskfabric.CheckpointEnvelope{
			Payload: args.Payload,
		},
	}

	if err := k.fabric.Create(task); err != nil {
		return nil, fmt.Errorf("agentsyscall: create task failed: %w", err)
	}

	log.Printf("agentsyscall: created task %q (%s) → READY", taskID, args.Capability)

	return &CreateTaskResult{
		TaskID: taskID,
		State:  string(taskfabric.StateReady),
	}, nil
}

// BindTools registers the spawn_agent and create_task tools on the given
// tool binder. The binder is the same sub.ToolBinder the production LLM
// executor uses for all its tools, so the LLM sees spawn_agent alongside
// web_search, file_read, etc. — the Agent's cognition treats spawning and
// task creation as first-class tool calls.
//
// The toolBinder interface matches sub.ToolBinder.BindTool exactly, so this
// function accepts any binder that implements that method.
func BindTools(binder ToolBinder, kernel *Kernel) {
	if binder == nil || kernel == nil {
		return
	}
	binder.BindTool(SpawnAgentTool, func(ctx context.Context, args map[string]any) (any, error) {
		var sa SpawnAgentArgs
		if v, ok := args[paramCapability].(string); ok {
			sa.Capability = v
		}
		if v, ok := args["parent_id"].(string); ok {
			sa.ParentID = v
		}
		if v, ok := args["task_context"].(map[string]any); ok {
			sa.TaskContext = v
		}
		if v, ok := args["resources"].(map[string]any); ok {
			sa.Resources = v
		}
		return kernel.SpawnAgent(ctx, sa)
	})

	binder.BindTool(CreateTaskTool, func(ctx context.Context, args map[string]any) (any, error) {
		var ct CreateTaskArgs
		if v, ok := args[paramCapability].(string); ok {
			ct.Capability = v
		}
		if deps, ok := args["dependencies"].([]any); ok {
			for _, d := range deps {
				if s, ok := d.(string); ok {
					ct.Dependencies = append(ct.Dependencies, s)
				}
			}
		}
		if v, ok := args["payload"].(map[string]any); ok {
			ct.Payload = v
		}
		return kernel.CreateTask(ctx, ct)
	})
	// W9: the whole-DAG planning entry. See plan.go. JSON round-trip keeps
	// the parse strict: type mismatches surface as errors instead of silently
	// dropping fields (e.g. a string "3" for priority).
	binder.BindTool(CreatePlanTool, func(ctx context.Context, args map[string]any) (any, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("agentsyscall: create_plan re-marshal: %w", err)
		}
		var cp CreatePlanArgs
		if err := json.Unmarshal(raw, &cp); err != nil {
			return nil, fmt.Errorf("agentsyscall: create_plan args: %w", err)
		}
		if len(cp.Steps) == 0 {
			return nil, errors.New("agentsyscall: create_plan requires a non-empty steps array")
		}
		return kernel.CreatePlan(ctx, cp)
	})
}

// ToolBinder is the minimal interface BindTools needs. It matches
// sub.ToolBinder.BindTool so the production binder satisfies it without
// importing this package.
type ToolBinder interface {
	BindTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error))
}

// ToolSchemas returns the LLM-facing tool schemas for spawn_agent and
// create_task. These are injected into the resources.Registry so the LLM
// Chat API receives them alongside the built-in tools.
func ToolSchemas() []ToolSchema {
	return []ToolSchema{
		{
			Name:        SpawnAgentTool,
			Description: "Spawn a new peer agent with a declared capability. The Kernel validates quota and registers the agent as a schedulable executor. Use this when you decide a task should be split and worked on by another agent.",
			Parameters: map[string]any{
				paramType: paramTypeObject,
				paramProperties: map[string]any{
					paramCapability: map[string]any{
						paramType:        paramTypeString,
						paramDescription: "The declared capability of the new agent (e.g. 'coder', 'reviewer', 'researcher').",
					},
					"parent_id": map[string]any{
						paramType:        paramTypeString,
						paramDescription: "The spawning agent's ID (for provenance). Leave empty for root agents.",
					},
					"task_context": map[string]any{
						paramType:        paramTypeObject,
						paramDescription: "Shared task state to pass to the new agent (goal, constraints). Never include private reasoning.",
					},
				},
				paramRequired: []string{paramCapability},
			},
		},
		{
			Name:        CreateTaskTool,
			Description: "Create a new task in the Task Fabric. The task enters READY state and the scheduler will assign it to a capable agent. Use this to decompose work into sub-tasks.",
			Parameters: map[string]any{
				paramType: paramTypeObject,
				paramProperties: map[string]any{
					paramCapability: map[string]any{
						paramType:        paramTypeString,
						paramDescription: "The required capability for this task (e.g. 'coder', 'reviewer').",
					},
					"dependencies": map[string]any{
						paramType:        paramTypeArray,
						paramItems:       map[string]any{paramType: paramTypeString},
						paramDescription: "Prerequisite task IDs that must complete before this task runs.",
					},
					"payload": map[string]any{
						paramType:        paramTypeObject,
						paramDescription: "Opaque task data (e.g. task_desc, parameters).",
					},
				},
				paramRequired: []string{paramCapability},
			},
		},
		CreatePlanToolSchema(),
	}
}

// ToolSchema is the minimal schema struct this package produces. It matches
// resources.ToolSchema's Name/Description/Parameters fields so the caller
// can convert without importing resources here.
type ToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}
