package planner

import (
	"context"
)

// SemanticAnalyzer converts a natural-language request into a structured Intent.
//
// This is the only component in the pipeline that may use an LLM.
// All downstream components are deterministic.
type SemanticAnalyzer interface {
	// Analyze parses a user request and returns a structured intent.
	// Args:
	//   ctx - cancellation and timeout context.
	//   request - raw user request string.
	// Returns:
	//   intent - structured intent for planning.
	//   err - error if analysis fails.
	Analyze(ctx context.Context, request string) (*Intent, error)
}

// CapabilityPlanner decomposes an Intent into a set of CapabilityRequirements.
//
// It is responsible for determining what capabilities are needed to fulfill
// the intent and in what order they should be executed.
type CapabilityPlanner interface {
	// Plan returns the capability requirements needed to fulfill the intent.
	// Args:
	//   ctx - cancellation and timeout context.
	//   intent - the parsed user intent.
	// Returns:
	//   requirements - ordered list of capability requirements.
	//   err - error if planning fails.
	Plan(ctx context.Context, intent *Intent) ([]CapabilityRequirement, error)
}

// ToolResolver maps capability requirements to candidate tools using
// the capability registry.
type ToolResolver interface {
	// Resolve finds all tools that can fulfill the given capability requirement.
	// Args:
	//   ctx - cancellation and timeout context.
	//   requirement - the capability required.
	// Returns:
	//   candidates - list of tool candidates sorted by initial relevance.
	//   err - error if resolution fails.
	Resolve(ctx context.Context, requirement *CapabilityRequirement) ([]ToolCandidate, error)
}

// ToolScorer ranks tool candidates using static metadata and historical evidence.
//
// The scorer is memory-aware: it uses evidence from past executions to adjust
// scores, preferring tools with higher success rates and lower latency.
type ToolScorer interface {
	// Score computes a score for each tool candidate and returns them ranked.
	// Args:
	//   ctx - cancellation and timeout context.
	//   candidates - tool candidates to score.
	//   evidence - historical evidence for the tools (may be empty).
	// Returns:
	//   scored - scored and ranked tool candidates.
	//   err - error if scoring fails.
	Score(ctx context.Context, candidates []ToolCandidate, evidence []ToolEvidence) ([]ToolCandidate, error)
}

// ExecutionPlanner converts scored capability requirements into an execution plan.
//
// For single-capability requests the plan is a single step.
// For multi-capability requests the plan is a DAG of steps with dependencies.
type ExecutionPlanner interface {
	// Plan creates an execution plan from the given requirements and candidates.
	// Args:
	//   ctx - cancellation and timeout context.
	//   intent - original intent (used for plan metadata).
	//   requirements - capability requirements with resolved tool candidates.
	// Returns:
	//   plan - the execution plan.
	//   err - error if plan generation fails.
	Plan(ctx context.Context, intent *Intent, requirements []CapabilityRequirement) (*ExecutionPlan, error)
}

// EvidenceStore persists and retrieves tool execution evidence for scoring.
//
// EvidenceStore is a plugin interface: external implementations can replace
// the default in-memory store with any backend (Postgres, Redis, file, etc.)
// by implementing this interface and passing it to NewPlanner.
//
// Built-in implementation: NewMemoryEvidenceStore().
// Example plugin: see integration_test.go for a custom implementation.
//
// To use a custom store:
//
//	store := MyPostgresEvidenceStore{db: pool}
//	planner, err := NewPlanner(analyzer, planner, resolver, scorer, execPlan, store)
type EvidenceStore interface {
	// Save records a tool execution result as evidence.
	// Args:
	//   ctx - cancellation and timeout context.
	//   evidence - the execution evidence to record.
	// Returns:
	//   err - error if storage fails.
	Save(ctx context.Context, evidence *ToolEvidence) error

	// Query retrieves evidence matching the given criteria.
	// Args:
	//   ctx - cancellation and timeout context.
	//   toolName - filter by tool name (empty means all tools).
	//   capabilityName - filter by capability name (empty means all capabilities).
	//   limit - max number of evidence records to return.
	// Returns:
	//   evidence - matching evidence records.
	//   err - error if query fails.
	Query(ctx context.Context, toolName string, capabilityName string, limit int) ([]ToolEvidence, error)

	// Aggregate returns aggregate metrics per tool and capability.
	// Args:
	//   ctx - cancellation and timeout context.
	//   toolName - filter by tool name (empty means all tools).
	// Returns:
	//   metrics - map of tool+capa → aggregate stats.
	//   err - error if aggregation fails.
	Aggregate(ctx context.Context, toolName string) (map[string]ToolScore, error)
}
