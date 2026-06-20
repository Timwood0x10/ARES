// Package evolution provides automatic experience extraction from flight recorder diagnostics.
// It bridges the flight recording system with the experience store to enable
// continuous learning from agent execution failures and anomalies.
package evolution

import (
	"context"

	"goagentx/internal/events"
)

// FlightRecorder defines the interface for accessing flight recorder diagnostics.
type FlightRecorder interface {
	// Diagnostics returns access to diagnostic reports for agents.
	Diagnostics() DiagnosticsAccessor

	// EventStore returns the event store for subscribing to events.
	EventStore() EventStoreSubscriber
}

// DiagnosticsAccessor provides read access to diagnostic results.
type DiagnosticsAccessor interface {
	// Get retrieves the diagnostic report for a specific agent.
	// Returns nil if no diagnostics exist for the agent.
	Get(agentID string) *DiagnosticsReport
}

// EventStoreSubscriber defines the subscription interface for event stores.
type EventStoreSubscriber interface {
	// Subscribe returns a channel that receives events matching the filter.
	// The channel is closed when ctx is cancelled.
	Subscribe(ctx context.Context, filter events.EventFilter) (<-chan *events.Event, error)
}

// ExperienceRepository defines the persistence interface for experiences.
type ExperienceRepository interface {
	// Create persists a new experience entry.
	Create(ctx context.Context, exp *Experience) error
}

// DiagnosticsReport represents aggregated diagnostic data for an agent.
type DiagnosticsReport struct {
	// AgentID is the identifier of the agent this report covers.
	AgentID string

	// Records contains individual diagnostic records.
	Records []DiagnosticRecord

	// HasIssues indicates whether any problematic diagnostics were found.
	HasIssues bool
}

// DiagnosticRecord represents a single diagnostic entry from the flight recorder.
type DiagnosticRecord struct {
	// ID is the unique identifier of the diagnostic record.
	ID string

	// AgentID is the identifier of the agent that generated this record.
	AgentID string

	// TaskID is the identifier of the task associated with this record.
	TaskID string

	// Category classifies the type of issue (e.g., "tool_timeout", "llm_error").
	Category string

	// RootCause describes the underlying cause of the issue.
	RootCause string

	// Suggestion provides a recommended fix or workaround.
	Suggestion string

	// Severity indicates how severe the issue is (1-10, higher is more severe).
	Severity int
}

// Experience represents an extracted experience to be stored.
type Experience struct {
	// TenantID is the tenant identifier for multi-tenancy isolation.
	TenantID string

	// Type is the experience type (e.g., "failure", "solution", "heuristic").
	Type string

	// Problem is the abstract problem statement derived from diagnostics.
	Problem string

	// Solution is the suggested solution approach from diagnostics.
	Solution string

	// Score is the importance score (0-1), inversely related to severity.
	Score float64

	// Source indicates where this experience originated.
	Source string

	// AgentID is the identifier of the agent that generated this experience.
	AgentID string

	// Metadata holds additional structured data.
	Metadata map[string]interface{}
}

// Experience type constants used by the evolution package.
const (
	// TypeFailure represents a failure experience learned from errors.
	TypeFailure = "failure"

	// TypeSolution represents a solution experience learned from successful patterns.
	TypeSolution = "solution"

	// TypeHeuristic represents a heuristic experience learned from recurring patterns.
	TypeHeuristic = "heuristic"

	// TypeStrategy represents a strategy experience learned from decision patterns.
	TypeStrategy = "strategy"

	// TypeGeneral represents a general-purpose experience.
	TypeGeneral = "general"
)

// Strategy represents an agent decision strategy that can be mutated and evolved.
type Strategy struct {
	// ID is the unique identifier of this strategy.
	ID string

	// Name is the human-readable name of the strategy.
	Name string

	// Version is the version number of this strategy (monotonically increasing).
	Version int

	// Params holds the configurable parameters of the strategy.
	Params map[string]any

	// ParentID references the parent strategy this was evolved from (empty for root strategies).
	ParentID string
}

// MutatorInterface abstracts strategy mutation capability.
// Implementations generate candidate strategies by applying mutations to a parent strategy.
type MutatorInterface interface {
	// Mutate generates n candidate strategies from the given parent strategy.
	Mutate(ctx context.Context, parent Strategy, n int) ([]Strategy, error)
}

// RegressionConfig holds configuration for arena regression testing.
type RegressionConfig struct {
	// Candidate is the strategy variant to test against the baseline.
	Candidate Strategy

	// Baseline is the current active strategy used as comparison reference.
	Baseline Strategy

	// TaskSampleSize is the number of historical tasks to replay for testing.
	TaskSampleSize int
}

// RegressionResult holds the outcome of an arena regression test.
type RegressionResult struct {
	// CandidateScore is the average score achieved by the candidate strategy.
	CandidateScore float64

	// BaselineScore is the average score achieved by the baseline strategy.
	BaselineScore float64

	// WinRate is the proportion of tasks where candidate outperformed baseline (0-1).
	WinRate float64

	// TotalTasks is the total number of tasks used in the regression test.
	TotalTasks int
}

// TesterInterface abstracts arena regression testing capability.
// Implementations compare candidate strategies against the current baseline.
type TesterInterface interface {
	// Run executes a regression test with the given configuration and returns results.
	Run(ctx context.Context, cfg RegressionConfig) (*RegressionResult, error)
}

// StrategyLineage records the genealogical relationship between strategies.
type StrategyLineage struct {
	// ParentID is the ID of the parent (source) strategy.
	ParentID string

	// ChildID is the ID of the new (mutated) strategy.
	ChildID string

	// MutationType describes what kind of mutation was applied.
	MutationType string

	// WinRate achieved by the child strategy in arena testing.
	WinRate float64

	// ScoreImprovement is the delta between child and parent scores.
	ScoreImprovement float64

	// Timestamp when this lineage record was created.
	Timestamp int64
}

// GenealogyRecorder abstracts strategy lineage recording capability.
// Implementations persist strategy evolution history for traceability.
type GenealogyRecorder interface {
	// Record persists a strategy lineage entry for future analysis.
	Record(ctx context.Context, lineage StrategyLineage) error
}
