// Package workflow provides the public API for workflow orchestration.
//
// This package exposes the static workflow engine (step + dependsOn) and
// the dynamic execution capabilities (retry, recovery, loops, routing)
// to external modules. The internal implementation lives in
// internal/workflow/engine; this file re-exports its public contract
// via type aliases so external callers can construct, register, and
// execute workflows without importing internal packages.
package workflow

import (
	"time"

	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// WorkflowStatus represents the execution status of a workflow.
type WorkflowStatus = engine.WorkflowStatus

// StepStatus represents the execution status of a workflow step.
type StepStatus = engine.StepStatus

// RecoveryStrategy classifies the recovery approach for a failed step.
type RecoveryStrategy = engine.RecoveryStrategy

// Workflow status constants.
const (
	WorkflowStatusPending   = engine.WorkflowStatusPending
	WorkflowStatusRunning   = engine.WorkflowStatusRunning
	WorkflowStatusCompleted = engine.WorkflowStatusCompleted
	WorkflowStatusFailed    = engine.WorkflowStatusFailed
	WorkflowStatusCancelled = engine.WorkflowStatusCancelled
)

// Step status constants.
const (
	StepStatusPending   = engine.StepStatusPending
	StepStatusRunning   = engine.StepStatusRunning
	StepStatusCompleted = engine.StepStatusCompleted
	StepStatusFailed    = engine.StepStatusFailed
	StepStatusSkipped   = engine.StepStatusSkipped
)

// Recovery strategy constants.
const (
	RecoveryRetry       = engine.RecoveryRetry
	RecoveryReplaceNode = engine.RecoveryReplaceNode
	RecoveryFailFast    = engine.RecoveryFailFast
)

// ConditionFunc is evaluated before a step executes. If it returns false,
// the step is skipped. A nil condition means unconditional.
type ConditionFunc = engine.ConditionFunc

// NodeRouter is a callback invoked after a step completes to dynamically
// select the next step to execute. Return "" to let the normal
// dependency-based topological order decide.
type NodeRouter = engine.NodeRouter

// RetryPolicy defines retry behavior for a step.
type RetryPolicy = engine.RetryPolicy

// RecoveryPolicy defines how the engine should recover when a step fails.
type RecoveryPolicy = engine.RecoveryPolicy

// InterruptConfig marks a step as requiring human approval before execution.
type InterruptConfig = engine.InterruptConfig

// LoopConfig defines controlled loop behavior for a workflow.
type LoopConfig = engine.LoopConfig

// Step represents a single step in a workflow.
type Step = engine.Step

// Workflow represents a workflow definition.
type Workflow = engine.Workflow

// WorkflowResult represents the final result of a workflow execution.
type WorkflowResult = engine.WorkflowResult

// StepResult represents the result of a step execution.
type StepResult = engine.StepResult

// StepFailure contains the context for a step failure that may be recoverable.
type StepFailure = engine.StepFailure

// RecoveryDecision is the outcome of a recovery handler invocation.
type RecoveryDecision = engine.RecoveryDecision

// StepRecoveryHandler defines the interface for recovering failed workflow steps.
type StepRecoveryHandler = engine.StepRecoveryHandler

// AgentFactory creates agent instances for workflow step execution.
// External modules provide their own factory to register custom agent types.
type AgentFactory = engine.AgentFactory

// AgentRegistry manages agent type registrations for the workflow engine.
// External modules use this to register their custom agent factories.
type AgentRegistry = engine.AgentRegistry

// NewAgentRegistry creates a new empty AgentRegistry.
// External modules call this to build a registry, register agent factories
// via Register(), then pass the registry to the workflow Service.
var NewAgentRegistry = engine.NewAgentRegistry

// NewWorkflow creates a new workflow with the given ID and name.
// Helper for external modules to construct workflow definitions.
func NewWorkflow(id, name string) *Workflow {
	return &Workflow{
		ID:        id,
		Name:      name,
		Version:   "1.0.0",
		Steps:     make([]*Step, 0),
		Variables: make(map[string]string),
		Metadata:  make(map[string]string),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// AddStep adds a step to the workflow and returns the workflow for chaining.
func AddStep(wf *Workflow, step *Step) *Workflow {
	wf.Steps = append(wf.Steps, step)
	return wf
}
