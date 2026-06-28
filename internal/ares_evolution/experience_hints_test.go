package evolution

import (
	"testing"
	"time"

	aresExperience "github.com/Timwood0x10/ares/internal/ares_experience"
)

// TestHintFromRankedExperience_Valid verifies that a valid ranked experience
// produces a correctly populated EvolutionHint.
func TestHintFromRankedExperience_Valid(t *testing.T) {
	t.Parallel()

	exp := &aresExperience.Experience{
		ID:          "exp-1",
		Type:        aresExperience.ExperienceTypeSuccess,
		Problem:     "agent unable to parse complex JSON responses",
		Solution:    "use structured output with search and read tools",
		Constraints: "requires API key\nrate limit: 100 req/min",
		Score:       0.85,
		Success:     true,
		AgentID:     "agent-42",
		UsageCount:  5,
		CreatedAt:   time.Now(),
	}

	ranked := &aresExperience.RankedExperience{
		Experience:    exp,
		FinalScore:    0.75,
		SemanticScore: 0.8,
		UsageBoost:    0.05,
		RecencyBoost:  0.02,
	}

	hint := HintFromRankedExperience(ranked)

	if hint.ID != "exp-1" {
		t.Errorf("expected ID 'exp-1', got %q", hint.ID)
	}
	if hint.TaskType != "" {
		t.Errorf("expected empty TaskType (RankedExperience has no task type), got %q", hint.TaskType)
	}
	if hint.Problem != "agent unable to parse complex JSON responses" {
		t.Errorf("expected Problem to be preserved, got %q", hint.Problem)
	}
	if hint.Solution != "use structured output with search and read tools" {
		t.Errorf("expected Solution to be preserved, got %q", hint.Solution)
	}
	if len(hint.Constraints) != 2 {
		t.Fatalf("expected 2 constraints, got %d", len(hint.Constraints))
	}
	if hint.Constraints[0] != "requires API key" {
		t.Errorf("expected constraint[0]=%q, got %q", "requires API key", hint.Constraints[0])
	}
	if hint.Confidence != 0.75 {
		t.Errorf("expected Confidence 0.75, got %f", hint.Confidence)
	}
	if len(hint.SourceExperienceIDs) != 1 || hint.SourceExperienceIDs[0] != "exp-1" {
		t.Errorf("expected SourceExperienceIDs [exp-1], got %v", hint.SourceExperienceIDs)
	}
	if len(hint.PreferredTools) == 0 {
		t.Error("expected PreferredTools to be extracted from solution containing known tools")
	}
}

// TestHintFromRankedExperience_NilInput verifies that nil ranked experience
// and nil experience pointer both produce an empty EvolutionHint.
func TestHintFromRankedExperience_NilInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ranked *aresExperience.RankedExperience
	}{
		{name: "nil ranked experience", ranked: nil},
		{name: "nil experience field", ranked: &aresExperience.RankedExperience{Experience: nil}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hint := HintFromRankedExperience(tt.ranked)
			if hint.Confidence != 0 {
				t.Errorf("expected zero Confidence for nil input, got %f", hint.Confidence)
			}
			if hint.ID != "" {
				t.Errorf("expected empty ID for nil input, got %q", hint.ID)
			}
		})
	}
}

// TestHintFromRankedExperience_ConfidenceClamping verifies that the FinalScore
// is clamped to [0.0, 1.0] when converting.
func TestHintFromRankedExperience_ConfidenceClamping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		score    float64
		expected float64
	}{
		{name: "negative score clamped to 0", score: -0.5, expected: 0},
		{name: "over 1.0 clamped to 1", score: 1.5, expected: 1},
		{name: "within range preserved", score: 0.42, expected: 0.42},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := &aresExperience.Experience{
				ID:   "clamp-test",
				Type: aresExperience.ExperienceTypeSuccess,
			}
			ranked := &aresExperience.RankedExperience{
				Experience: exp,
				FinalScore: tt.score,
			}

			hint := HintFromRankedExperience(ranked)
			if hint.Confidence != tt.expected {
				t.Errorf("expected Confidence %f, got %f", tt.expected, hint.Confidence)
			}
		})
	}
}

// TestHintFromRankedExperience_EmptyConstraints verifies that empty or
// whitespace-only constraints produce a nil slice (not a single-element slice).
func TestHintFromRankedExperience_EmptyConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		constraints string
	}{
		{name: "empty constraints", constraints: ""},
		{name: "whitespace only", constraints: "  \n  "},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := &aresExperience.Experience{
				ID:          "empty-constraint-test",
				Type:        aresExperience.ExperienceTypeSuccess,
				Constraints: tt.constraints,
			}
			ranked := &aresExperience.RankedExperience{
				Experience: exp,
				FinalScore: 0.5,
			}

			hint := HintFromRankedExperience(ranked)
			if hint.Constraints != nil {
				t.Errorf("expected nil constraints for empty input, got %v", hint.Constraints)
			}
		})
	}
}

// TestHintFromRankedExperience_FailureType verifies that failure-type
// experiences produce no PreferredTools even if the solution contains
// tool names.
func TestHintFromRankedExperience_FailureType(t *testing.T) {
	t.Parallel()

	exp := &aresExperience.Experience{
		ID:       "failure-test",
		Type:     aresExperience.ExperienceTypeFailure,
		Problem:  "agent crashed on tool call",
		Solution: "use search and read tools with timeout",
	}
	ranked := &aresExperience.RankedExperience{
		Experience: exp,
		FinalScore: 0.3,
	}

	hint := HintFromRankedExperience(ranked)
	if hint.PreferredTools != nil {
		t.Errorf("expected nil PreferredTools for failure type, got %v", hint.PreferredTools)
	}
}

// TestHintFromRankedExperience_EmptyExperience verifies that an experience
// with only zero values produces a valid hint with appropriate defaults.
func TestHintFromRankedExperience_EmptyExperience(t *testing.T) {
	t.Parallel()

	ranked := &aresExperience.RankedExperience{
		Experience: &aresExperience.Experience{},
	}

	hint := HintFromRankedExperience(ranked)
	if hint.ID != "" {
		t.Errorf("expected empty ID for empty experience, got %q", hint.ID)
	}
	if hint.Confidence != 0 {
		t.Errorf("expected Confidence 0 for empty experience, got %f", hint.Confidence)
	}
	if hint.PreferredTools != nil {
		t.Errorf("expected nil PreferredTools for empty type, got %v", hint.PreferredTools)
	}
}

// TestExtractToolNames verifies that tool names are correctly extracted
// from solution strings.
func TestExtractToolNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		solution string
		expected []string
	}{
		{
			name:     "empty solution returns nil",
			solution: "",
			expected: nil,
		},
		{
			name:     "no known tools returns nil",
			solution: "just a regular description without tool references",
			expected: nil,
		},
		{
			name:     "multiple known tools detected",
			solution: "use search API and read from database, then write results",
			expected: []string{"search", "read", "write"},
		},
		{
			name:     "case insensitive matching",
			solution: "SEARCH and Read and Write",
			expected: []string{"search", "read", "write"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := extractToolNames(tt.solution)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d tools, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, tool := range tt.expected {
				if result[i] != tool {
					t.Errorf("expected tool[%d]=%q, got %q", i, tool, result[i])
				}
			}
		})
	}
}

// TestEvolutionHint_ZeroValue verifies that a zero-value EvolutionHint
// is usable and behaves predictably (nil slices, empty strings).
func TestEvolutionHint_ZeroValue(t *testing.T) {
	t.Parallel()

	var hint EvolutionHint
	if hint.ID != "" {
		t.Errorf("expected empty ID for zero value, got %q", hint.ID)
	}
	if hint.Constraints != nil {
		t.Errorf("expected nil Constraints for zero value")
	}
	if hint.FailedPatterns != nil {
		t.Errorf("expected nil FailedPatterns for zero value")
	}
	if hint.PreferredTools != nil {
		t.Errorf("expected nil PreferredTools for zero value")
	}
	if hint.PromptSnippets != nil {
		t.Errorf("expected nil PromptSnippets for zero value")
	}
	if hint.ParamHints != nil {
		t.Errorf("expected nil ParamHints for zero value")
	}
	if hint.SourceExperienceIDs != nil {
		t.Errorf("expected nil SourceExperienceIDs for zero value")
	}
}

// TestStrategyOutcome_ZeroValue verifies that a zero-value StrategyOutcome
// is usable and behaves predictably.
func TestStrategyOutcome_ZeroValue(t *testing.T) {
	t.Parallel()

	var outcome StrategyOutcome
	if outcome.StrategyID != "" {
		t.Errorf("expected empty StrategyID for zero value, got %q", outcome.StrategyID)
	}
	if outcome.ExperienceIDs != nil {
		t.Errorf("expected nil ExperienceIDs for zero value")
	}
}
