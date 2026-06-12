// Package core provides core abstractions for workflow operations.
package core

import (
	"context"
	"time"
)

// WorkflowService orchestrates multi-step agent workflows.
type WorkflowService interface {
	// Execute runs a workflow synchronously and returns the result.
	Execute(ctx context.Context, req *WorkflowRequest) (*WorkflowResponse, error)

	// ExecuteStream runs a workflow and streams progress events.
	ExecuteStream(ctx context.Context, req *WorkflowRequest) (<-chan WorkflowEvent, error)

	// ListWorkflows returns all registered workflow definitions.
	ListWorkflows(ctx context.Context) ([]*WorkflowSummary, error)

	// GetWorkflow returns a workflow definition by ID.
	GetWorkflow(ctx context.Context, id string) (*WorkflowDefinition, error)
}

// WorkflowRequest represents a workflow execution request.
type WorkflowRequest struct {
	// WorkflowID is the identifier of the workflow to execute.
	WorkflowID string
	// Input is the initial input for the workflow.
	Input string
	// Variables overrides workflow-level variables.
	Variables map[string]string
	// Timeout overrides the default execution timeout.
	Timeout time.Duration
}

// WorkflowResponse represents the result of a workflow execution.
type WorkflowResponse struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string
	// WorkflowID is the workflow that was executed.
	WorkflowID string
	// Status is the final execution status.
	Status WorkflowStatus
	// Output maps step IDs to their outputs.
	Output map[string]interface{}
	// Steps contains the result of each step.
	Steps []*StepResult
	// Error is set when the workflow failed.
	Error string
	// Duration is the total execution time.
	Duration time.Duration
}

// WorkflowEvent represents a streaming event during workflow execution.
type WorkflowEvent struct {
	// Type is the kind of event.
	Type WorkflowEventType
	// ExecutionID is the execution this event belongs to.
	ExecutionID string
	// WorkflowID is the workflow being executed.
	WorkflowID string
	// StepID is set for step-level events.
	StepID string
	// StepName is the display name of the step.
	StepName string
	// Status is the step or workflow status at event time.
	Status WorkflowStatus
	// Output is set when a step completes successfully.
	Output string
	// Error is set when a step or workflow fails.
	Error string
	// Timestamp is when the event occurred.
	Timestamp time.Time
}

// WorkflowSummary is a lightweight workflow listing entry.
type WorkflowSummary struct {
	// ID is the workflow identifier.
	ID string
	// Name is the display name.
	Name string
	// Description is a brief description.
	Description string
	// StepCount is the number of steps.
	StepCount int
	// CreatedAt is the creation timestamp.
	CreatedAt time.Time
	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time
}

// WorkflowDefinition describes a complete workflow.
type WorkflowDefinition struct {
	// ID is the workflow identifier.
	ID string
	// Name is the display name.
	Name string
	// Version is the workflow version.
	Version string
	// Description is a brief description.
	Description string
	// Steps defines the workflow steps.
	Steps []*StepDef
	// Variables are workflow-level variables.
	Variables map[string]string
	// Metadata is optional key-value metadata.
	Metadata map[string]string
	// CreatedAt is the creation timestamp.
	CreatedAt time.Time
	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time
}

// StepDef defines a single workflow step.
type StepDef struct {
	// ID is the step identifier.
	ID string
	// Name is the display name.
	Name string
	// AgentType is the type of agent that executes this step.
	AgentType string
	// Input is the step input template.
	Input string
	// DependsOn lists step IDs that must complete before this step.
	DependsOn []string
	// Timeout overrides the default step timeout.
	Timeout time.Duration
}

// StepResult represents the outcome of a single step.
type StepResult struct {
	// StepID is the step identifier.
	StepID string
	// Name is the step display name.
	Name string
	// Status is the step completion status.
	Status WorkflowStatus
	// Output is the step output on success.
	Output string
	// Error is the error message on failure.
	Error string
	// Duration is the step execution time.
	Duration time.Duration
}

// WorkflowStatus represents the execution status of a workflow or step.
type WorkflowStatus string

const (
	// WorkflowStatusPending indicates the workflow has not started.
	WorkflowStatusPending WorkflowStatus = "pending"
	// WorkflowStatusRunning indicates the workflow is executing.
	WorkflowStatusRunning WorkflowStatus = "running"
	// WorkflowStatusCompleted indicates the workflow finished successfully.
	WorkflowStatusCompleted WorkflowStatus = "completed"
	// WorkflowStatusFailed indicates the workflow failed.
	WorkflowStatusFailed WorkflowStatus = "failed"
	// WorkflowStatusCancelled indicates the workflow was cancelled.
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// WorkflowEventType classifies a streaming event.
type WorkflowEventType int

const (
	// WorkflowEventStarted is emitted when the workflow starts.
	WorkflowEventStarted WorkflowEventType = iota
	// WorkflowEventStepStarted is emitted when a step begins execution.
	WorkflowEventStepStarted
	// WorkflowEventStepCompleted is emitted when a step finishes successfully.
	WorkflowEventStepCompleted
	// WorkflowEventStepFailed is emitted when a step fails.
	WorkflowEventStepFailed
	// WorkflowEventCompleted is emitted when the workflow finishes successfully.
	WorkflowEventCompleted
	// WorkflowEventFailed is emitted when the workflow fails.
	WorkflowEventFailed
)
