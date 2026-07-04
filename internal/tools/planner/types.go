// Package planner implements a capability-driven tool selection and execution
// planning layer. It replaces LLM-based tool guessing with deterministic
// intent-to-capability-to-tool resolution, scoring, and DAG execution.
//
// The planner pipeline:
//
//	Request → SemanticAnalyzer → Intent → CapabilityPlanner
//	  → []CapabilityRequirement → ToolResolver → []ToolCandidate
//	  → ToolScorer → ranked []ToolCandidate → ExecutionPlanner → ExecutionPlan
//	  → ToolRuntime → EvidenceCollector → EvidenceStore
package planner

import (
	"time"
)

// Intent represents the parsed understanding of a user request.
// This is the output of the SemanticAnalyzer and the input to CapabilityPlanner.
type Intent struct {
	// Goal is a high-level description of what the user wants to achieve.
	// Example: "mathematical computation", "document processing"
	Goal string

	// Operation is the specific operation within the goal domain.
	// Example: "summation", "text_extraction", "pdf_parsing"
	Operation string

	// Complexity indicates the estimated complexity level.
	// Values: "simple", "moderate", "complex"
	Complexity string

	// RequiredCapabilities lists the capabilities needed to fulfill the intent.
	// This is the bridge between semantic understanding and capability planning.
	RequiredCapabilities []string

	// Constraints holds any additional constraints (time, accuracy, etc.).
	Constraints map[string]string
}

// CapabilityRequirement represents a single capability needed to fulfill
// part of an intent. A complex intent can be decomposed into multiple
// capability requirements that form a dependency chain.
type CapabilityRequirement struct {
	// Name is the canonical capability name.
	// Example: "Arithmetic", "Summation", "PDFParsing"
	Name string

	// InputType describes the expected input type.
	// Example: "Expression", "File", "Text"
	InputType string

	// OutputType describes the output produced.
	// Example: "Number", "Text", "JSON"
	OutputType string

	// DependsOn lists capability names that must complete before this one.
	DependsOn []string
}

// ToolCandidate represents a tool that can fulfill a capability requirement,
// along with its metadata for scoring and selection.
type ToolCandidate struct {
	// ToolName is the registered tool name.
	ToolName string

	// CapabilityName is the capability this tool provides for this request.
	CapabilityName string

	// Score is the computed score for this candidate (higher = better).
	Score float64

	// Cost is a relative cost metric (lower = cheaper).
	Cost int

	// Latency is the estimated execution latency.
	Latency time.Duration

	// Deterministic indicates whether the tool produces the same output
	// for the same input every time.
	Deterministic bool

	// Composable indicates whether the tool output can be piped as input
	// to another tool.
	Composable bool

	// SideEffects indicates whether the tool changes external state.
	SideEffects bool

	// SuccessRate is the historical success rate (0.0 to 1.0).
	// Extracted from EvidenceStore when available.
	SuccessRate float64
}

// ToolScore holds the computed scoring factors for a tool candidate.
type ToolScore struct {
	// BaseScore is the static, configuration-derived score.
	BaseScore float64

	// EvidenceScore is the dynamic score adjustment from historical evidence.
	EvidenceScore float64

	// Penalty accumulates penalties for side effects, high latency, etc.
	Penalty float64

	// Final is the final computed score (BaseScore + EvidenceScore - Penalty).
	Final float64
}

// ExecutionStep represents a single step in an execution plan.
type ExecutionStep struct {
	// StepID is the unique identifier for this step within the plan.
	StepID string

	// ToolName is the name of the tool to execute.
	ToolName string

	// CapabilityName is the capability this step fulfills.
	CapabilityName string

	// Parameters to pass to the tool.
	Parameters map[string]interface{}

	// DependsOn lists StepIDs that must complete before this step.
	DependsOn []string

	// FallbackToolNames lists alternative tools if the primary fails.
	FallbackToolNames []string
}

// ExecutionPlan is the complete plan for fulfilling an intent.
// It can be single-step or multi-step (DAG).
type ExecutionPlan struct {
	// PlanID is a unique identifier for this plan instance.
	PlanID string

	// Intent is the original intent this plan fulfills.
	Intent Intent

	// Steps are the execution steps in dependency order.
	Steps []ExecutionStep

	// IsMultiStep indicates whether this plan requires multiple tools.
	IsMultiStep bool

	// Cost is the estimated total cost.
	Cost int

	// EstimatedLatency is the estimated total execution time.
	EstimatedLatency time.Duration
}

// ToolEvidence records the result of a single tool execution for feedback.
type ToolEvidence struct {
	// ToolName is the name of the tool that was executed.
	ToolName string

	// CapabilityName is the capability that was invoked.
	CapabilityName string

	// Success indicates whether the execution succeeded.
	Success bool

	// Latency is how long the execution took.
	Latency time.Duration

	// RetryCount is how many retries were attempted.
	RetryCount int

	// ErrorClass classifies the failure if not successful.
	// Values: "timeout", "invalid_input", "internal_error", "external_unavailable"
	ErrorClass string

	// Timestamp is when the execution occurred.
	Timestamp time.Time
}
