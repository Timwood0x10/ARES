package agentsyscall

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/kernelctx"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// CreatePlanTool is the tool name exposed to the LLM for submitting a
// multi-step plan (one batch, dependency-ordered) to the Task Fabric.
const CreatePlanTool = "create_plan"

// PlanStepArgs is the LLM-facing description of one plan step. It mirrors
// taskfabric.PlanStep minus the Kernel-stamped Origin.
type PlanStepArgs struct {
	// ID is the unique step id within the plan.
	ID string `json:"id"`
	// Capability is the required executor capability.
	Capability string `json:"capability"`
	// DependsOn lists prerequisite step IDs within the same plan.
	DependsOn []string `json:"depends_on,omitempty"`
	// Priority drives preemption (higher wins); 0 = normal.
	Priority int `json:"priority,omitempty"`
	// MaxRetries is the TOTAL attempt budget (0 = kernel default).
	MaxRetries int `json:"max_retries,omitempty"`
	// Payload carries opaque step data (task_desc, parameters).
	Payload map[string]any `json:"payload,omitempty"`
}

// CreatePlanArgs is the create_plan tool argument envelope.
type CreatePlanArgs struct {
	// Steps is the dependency-ordered batch to compile. Must be non-empty.
	Steps []PlanStepArgs `json:"steps"`
	// NOTE: no creator argument — the Kernel stamps every task's Origin from
	// the tool context (kernelctx.CallerID), identical to CreateTask.
}

// CreatePlanResult reports the batch outcome.
type CreatePlanResult struct {
	// TaskIDs are the created task IDs, in input order. All are READY.
	TaskIDs []string `json:"task_ids"`
	// Count is len(TaskIDs).
	Count int `json:"count"`
	// State is the shared lifecycle state of the batch ("ready").
	State string `json:"state"`
}

// CreatePlan is the create_plan syscall: it validates and compiles an
// LLM-produced multi-step plan (with dependencies) into one all-or-nothing
// batch of READY tasks. Compared with N× create_task it lets the cognitive
// layer draw the whole DAG at once; Origin is stamped from the tool context
// for every step in the batch (Kernel-enforced provenance).
//
// Args:
//   - ctx: the tool-call context; its caller id becomes every task's Origin.
//   - args: the parsed create_plan arguments.
//
// Returns:
//
//	*CreatePlanResult - the created batch, or nil on failure.
//	error - validation / compilation errors (nothing is created on error).
func (k *Kernel) CreatePlan(ctx context.Context, args CreatePlanArgs) (*CreatePlanResult, error) {
	if k.fabric == nil {
		return nil, errors.New("agentsyscall: task fabric not wired")
	}
	if len(args.Steps) == 0 {
		return nil, errors.New("agentsyscall: plan requires at least one step")
	}
	origin := kernelctx.CallerID(ctx)
	steps := make([]taskfabric.PlanStep, 0, len(args.Steps))
	for _, s := range args.Steps {
		if s.Capability == "" {
			return nil, fmt.Errorf("agentsyscall: plan step %q: capability is required", s.ID)
		}
		steps = append(steps, taskfabric.PlanStep{
			ID:         s.ID,
			Capability: s.Capability,
			DependsOn:  s.DependsOn,
			Priority:   s.Priority,
			MaxRetries: s.MaxRetries,
			Payload:    s.Payload,
			Origin:     origin,
		})
	}
	ids, err := k.fabric.CompilePlan(ctx, steps)
	if err != nil {
		return nil, fmt.Errorf("agentsyscall: compile plan: %w", err)
	}
	log.Printf("agentsyscall: created plan batch of %d tasks (origin=%q) → READY", len(ids), origin)
	return &CreatePlanResult{
		TaskIDs: ids,
		Count:   len(ids),
		State:   string(taskfabric.StateReady),
	}, nil
}

// CreatePlanToolSchema returns the LLM-facing schema for create_plan.
func CreatePlanToolSchema() ToolSchema {
	return ToolSchema{
		Name: CreatePlanTool,
		Description: "Submit a multi-step plan to the Task Fabric in one batch. " +
			"Steps may declare dependencies on other steps in the same plan; the batch is atomic " +
			"(any invalid step rejects the whole plan). Use this instead of repeated create_task calls " +
			"when you can draw the whole dependency DAG up front.",
		Parameters: map[string]any{
			paramType: paramTypeObject,
			paramProperties: map[string]any{
				"steps": map[string]any{
					paramType: paramTypeArray,
					paramDescription: "The plan steps. Each step needs a unique id, a capability, and optional depends_on ids " +
						"referencing other steps in this plan.",
					paramItems: map[string]any{
						paramType: paramTypeObject,
						paramProperties: map[string]any{
							"id": map[string]any{
								paramType:        paramTypeString,
								paramDescription: "Unique step id within this plan.",
							},
							paramCapability: map[string]any{
								paramType:        paramTypeString,
								paramDescription: "The required capability for this step (e.g. 'coder', 'reviewer').",
							},
							"depends_on": map[string]any{
								paramType:        paramTypeArray,
								paramItems:       map[string]any{paramType: paramTypeString},
								paramDescription: "Step IDs in this plan that must complete before this step runs.",
							},
							"priority": map[string]any{
								paramType:        "integer",
								paramDescription: "Scheduling priority (higher wins). 0 = normal.",
							},
							"max_retries": map[string]any{
								paramType:        "integer",
								paramDescription: "Total attempt budget. 0 = kernel default (first attempt + one retry).",
							},
							"payload": map[string]any{
								paramType:        paramTypeObject,
								paramDescription: "Opaque step data (e.g. task_desc, parameters).",
							},
						},
						paramRequired: []string{"id", paramCapability},
					},
				},
			},
			paramRequired: []string{"steps"},
		},
	}
}
