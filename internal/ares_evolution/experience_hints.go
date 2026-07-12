// Package evolution provides automatic experience extraction from flight recorder diagnostics.
// It bridges the flight recording system with the experience store to enable
// continuous learning from agent execution failures and anomalies.
package evolution

import (
	"context"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/experience"
	aresExperience "github.com/Timwood0x10/ares/internal/ares_experience"
)

// EvolutionHint represents a distilled guidance hint derived from past
// experiences. It captures what worked, what failed, and what parameter
// biases should be applied during strategy mutation.
type EvolutionHint struct {
	// ID is the unique identifier of this hint.
	ID string

	// TaskType is the type of task this hint applies to.
	TaskType string

	// Problem is the abstract problem statement.
	Problem string

	// Solution is the concise solution approach that worked.
	Solution string

	// Constraints are important constraints or context for the solution.
	Constraints []string

	// FailedPatterns are patterns that led to failure and should be avoided.
	FailedPatterns []string

	// PreferredTools are tool configurations that worked well.
	PreferredTools []string

	// PromptSnippets are prompt text snippets that contributed to success.
	PromptSnippets []string

	// ParamHints maps parameter names to suggested values.
	ParamHints map[string]float64

	// Confidence is the confidence score for this hint (0.0 to 1.0).
	Confidence float64

	// SourceExperienceIDs are the IDs of the experiences that produced this hint.
	SourceExperienceIDs []string
}

// StrategyOutcome records the result of deploying a strategy mutation,
// enabling the experience provider to learn from real execution outcomes.
type StrategyOutcome struct {
	// StrategyID is the ID of the strategy that was deployed.
	StrategyID string

	// TaskType is the type of task this strategy was used for.
	TaskType string

	// Success indicates whether the strategy deployment was successful.
	Success bool

	// Score is the fitness score achieved by this strategy.
	Score float64

	// Cost is the computational cost incurred.
	Cost float64

	// LatencyMs is the execution latency in milliseconds.
	LatencyMs int64

	// MutationType describes what kind of mutation produced this strategy.
	MutationType string

	// ExperienceIDs are the IDs of experiences that influenced this strategy.
	ExperienceIDs []string

	// Timestamp is when this outcome was recorded.
	Timestamp time.Time
}

// GuidanceProvider abstracts the ability to query evolution hints from
// past experiences and record strategy outcomes. Implementations must not
// depend on concrete memory repositories, allowing the evolution system to
// request hints without direct import of storage packages.
type GuidanceProvider interface {
	// HintsForTask returns evolution hints relevant to the given task type.
	// The implementation should return up to limit hints, ordered by relevance.
	// Returns empty slice with nil error when no hints are available.
	HintsForTask(ctx context.Context, taskType string, limit int) ([]EvolutionHint, error)

	// RecordStrategyOutcome persists a strategy outcome for future learning.
	RecordStrategyOutcome(ctx context.Context, outcome StrategyOutcome) error
}

// HintFromRankedExperience converts an ares_experience.RankedExperience into
// an EvolutionHint. Fields that have no direct counterpart in RankedExperience
// (e.g., FailedPatterns, PreferredTools, PromptSnippets, ParamHints) are left
// as zero values, since these are populated by more sophisticated providers.
//
// The Confidence is derived from the ranked experience's FinalScore, clamped
// to [0.0, 1.0]. The SourceExperienceIDs contains the original experience ID.
//
// Returns an empty EvolutionHint (with zero Confidence) if the input is nil
// or has a nil Experience field.
func HintFromRankedExperience(ranked *aresExperience.RankedExperience) EvolutionHint {
	if ranked == nil || ranked.Experience == nil {
		return EvolutionHint{}
	}

	exp := ranked.Experience
	confidence := ranked.FinalScore
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	var constraints []string
	if strings.TrimSpace(exp.Constraints) != "" {
		constraints = strings.Split(exp.Constraints, "\n")
	}

	var preferredTools []string
	if exp.Type == aresExperience.ExperienceTypeSuccess {
		preferredTools = extractToolNames(exp.Solution)
	}

	return EvolutionHint{
		ID: exp.ID,
		// TaskType is left empty here because RankedExperience does not carry
		// task type information. Callers with access to richer experience data
		// should populate this field themselves.
		TaskType:            "",
		Problem:             exp.Problem,
		Solution:            exp.Solution,
		Constraints:         constraints,
		Confidence:          confidence,
		SourceExperienceIDs: []string{exp.ID},
		PreferredTools:      preferredTools,
	}
}

// extractToolNames performs simple heuristic extraction of tool names from a
// solution string. It looks for known tool patterns like "search", "read",
// "write", "exec", "calculate". Returns nil when no tools are detected.
func extractToolNames(solution string) []string {
	if solution == "" {
		return nil
	}

	lower := strings.ToLower(solution)
	var tools []string
	knownTools := []string{"search", "read", "write", "exec", "calculate", "code", "web"}

	for _, t := range knownTools {
		if strings.Contains(lower, t) {
			tools = append(tools, t)
		}
	}

	return tools
}

// FuncGuidanceProvider adapts functions to the GuidanceProvider interface.
// Useful for injecting simple implementations in bootstrap without creating
// a full struct type.
type FuncGuidanceProvider struct {
	HintsFunc  func(ctx context.Context, taskType string, limit int) ([]EvolutionHint, error)
	RecordFunc func(ctx context.Context, outcome StrategyOutcome) error
}

// HintsForTask delegates to the wrapped HintsFunc.
func (p *FuncGuidanceProvider) HintsForTask(ctx context.Context, taskType string, limit int) ([]EvolutionHint, error) {
	if p.HintsFunc == nil {
		return nil, nil
	}
	return p.HintsFunc(ctx, taskType, limit)
}

// RecordStrategyOutcome delegates to the wrapped RecordFunc.
func (p *FuncGuidanceProvider) RecordStrategyOutcome(ctx context.Context, outcome StrategyOutcome) error {
	if p.RecordFunc == nil {
		return nil
	}
	return p.RecordFunc(ctx, outcome)
}

// HintFromNormalizedExperience converts a NormalizedExperience into an EvolutionHint.
// This is a simpler conversion than HintFromRankedExperience, using the experience's
// Score directly as the confidence.
func HintFromNormalizedExperience(exp experience.NormalizedExperience) EvolutionHint {
	confidence := exp.Score
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	var constraints []string
	if strings.TrimSpace(exp.Problem) != "" {
		constraints = append(constraints, exp.Problem)
	}

	preferredTools := extractToolNames(exp.Solution)

	return EvolutionHint{
		ID:                  exp.ID,
		TaskType:            exp.TaskType,
		Problem:             exp.Problem,
		Solution:            exp.Solution,
		Constraints:         constraints,
		Confidence:          confidence,
		SourceExperienceIDs: []string{exp.ID},
		PreferredTools:      preferredTools,
	}
}
