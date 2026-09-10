package evolution

import (
	"context"
	"testing"

	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

// stubGuidanceProvider returns a canned hint set for bridge passthrough tests.
type stubGuidanceProvider struct {
	hints []EvolutionHint
}

func (s *stubGuidanceProvider) HintsForTask(_ context.Context, _ string, _ int) ([]EvolutionHint, error) {
	return s.hints, nil
}

func (s *stubGuidanceProvider) RecordStrategyOutcome(_ context.Context, _ StrategyOutcome) error {
	return nil
}

// TestAPIGuidanceBridge_PassesAllHintFields locks the field passthrough of
// apiGuidanceBridge (REVIEW 2.4#22): the bridge previously dropped the
// Confidence field (and several others), so experience-guided mutation
// silently received zero-confidence hints on the public API path and
// filtered them out. Every field must survive the internal→API conversion.
func TestAPIGuidanceBridge_PassesAllHintFields(t *testing.T) {
	provider := &stubGuidanceProvider{hints: []EvolutionHint{{
		ID:                  "hint-1",
		TaskType:            "coding",
		Problem:             "slow build",
		Solution:            "cache deps",
		Constraints:         []string{"no network"},
		FailedPatterns:      []string{"rebuild-all"},
		PreferredTools:      []string{"bazel"},
		PromptSnippets:      []string{"prefer cache"},
		ParamHints:          map[string]float64{"temperature": 0.2},
		Confidence:          0.87,
		SourceExperienceIDs: []string{"exp-9"},
	}}}
	bridge := &apiGuidanceBridge{provider: provider}

	out, err := bridge.HintsForTask(context.Background(), "coding", 5)
	if err != nil {
		t.Fatalf("HintsForTask: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(out))
	}
	h := out[0]
	if h.ID != "hint-1" || h.TaskType != "coding" || h.Problem != "slow build" || h.Solution != "cache deps" {
		t.Errorf("identity fields lost: %+v", h)
	}
	if len(h.Constraints) != 1 || h.Constraints[0] != "no network" {
		t.Errorf("Constraints lost: %+v", h.Constraints)
	}
	if len(h.FailedPatterns) != 1 || len(h.PreferredTools) != 1 || len(h.PromptSnippets) != 1 {
		t.Errorf("list fields lost: %+v", h)
	}
	if h.ParamHints["temperature"] != 0.2 {
		t.Errorf("ParamHints lost: %+v", h.ParamHints)
	}
	// Confidence is the field the old bridge dropped — the exact regression
	// this test guards.
	if h.Confidence != 0.87 {
		t.Errorf("Confidence not passed through: got %f, want 0.87", h.Confidence)
	}
	if len(h.SourceExperienceIDs) != 1 || h.SourceExperienceIDs[0] != "exp-9" {
		t.Errorf("SourceExperienceIDs lost: %+v", h.SourceExperienceIDs)
	}
	// Interface sanity: the bridge output feeds evolution.GuidanceProvider.
	var _ evolution.GuidanceProvider = bridge
}
