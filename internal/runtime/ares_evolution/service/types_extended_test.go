package evolution

import (
	"testing"
	"time"
)

func TestEvolutionHintFieldsAccessible(t *testing.T) {
	h := EvolutionHint{
		ID:                  "hint-1",
		TaskType:            "code-review",
		Problem:             "review code quality",
		Solution:            "use static analysis",
		Constraints:         []string{"no LLM"},
		FailedPatterns:      []string{"manual review"},
		PreferredTools:      []string{"golangci-lint"},
		PromptSnippets:      []string{"check for bugs"},
		ParamHints:          map[string]float64{"temperature": 0.3},
		Confidence:          0.85,
		SourceExperienceIDs: []string{"exp-1", "exp-2"},
	}
	if h.ID != "hint-1" {
		t.Errorf("ID = %q, want hint-1", h.ID)
	}
	if h.Confidence != 0.85 {
		t.Errorf("Confidence = %v, want 0.85", h.Confidence)
	}
	if len(h.ParamHints) != 1 {
		t.Errorf("ParamHints len = %d, want 1", len(h.ParamHints))
	}
}

func TestStrategyOutcomeFieldsAccessible(t *testing.T) {
	o := StrategyOutcome{
		StrategyID:    "s-1",
		TaskType:      "code-review",
		Success:       true,
		Score:         0.92,
		Cost:          0.05,
		LatencyMs:     150,
		MutationType:  "param_tweak",
		ExperienceIDs: []string{"exp-1"},
		Timestamp:     time.Now(),
	}
	if o.StrategyID != "s-1" {
		t.Errorf("StrategyID = %q, want s-1", o.StrategyID)
	}
	if !o.Success {
		t.Error("Success should be true")
	}
}

func TestSystemConfigFieldsAccessible(t *testing.T) {
	cfg := &SystemConfig{
		PopulationSize:         20,
		EliteCount:             3,
		SurvivalRate:           0.6,
		MutationRate:           0.2,
		MinMutationRate:        0.05,
		MaxMutationRate:        0.5,
		MaxStagnantGenerations: 10,
		DiversityThreshold:     0.15,
		BreedingPoolRatio:      0.6,
	}
	if cfg.PopulationSize != 20 {
		t.Errorf("PopulationSize = %d, want 20", cfg.PopulationSize)
	}
	if cfg.MutationRate != 0.2 {
		t.Errorf("MutationRate = %v, want 0.2", cfg.MutationRate)
	}
	if cfg.SurvivalRate != 0.6 {
		t.Errorf("SurvivalRate = %v, want 0.6", cfg.SurvivalRate)
	}
}

func TestStrategyFieldsAccessible(t *testing.T) {
	s := &Strategy{
		ID:       "s-1",
		Name:     "test-strategy",
		Version:  3,
		Params:   map[string]any{"temperature": 0.5},
		ParentID: "s-0",
	}
	if s.ID != "s-1" {
		t.Errorf("ID = %q, want s-1", s.ID)
	}
	if s.Version != 3 {
		t.Errorf("Version = %d, want 3", s.Version)
	}
}

func TestGuardrailConfigFieldsAccessible(t *testing.T) {
	g := GuardrailConfig{
		Enabled:                true,
		BaselineScore:          0.5,
		MaxStagnantGenerations: 10,
		MaxLineageShare:        0.8,
	}
	if !g.Enabled {
		t.Error("Enabled should be true")
	}
	if g.BaselineScore != 0.5 {
		t.Errorf("BaselineScore = %v, want 0.5", g.BaselineScore)
	}
}

func TestEvidenceFieldsAccessible(t *testing.T) {
	e := Evidence{
		StrategyID:  "s-1",
		SuccessRate: 0.85,
		LatencyP50:  150,
		ErrorRate:   0.02,
		SampleCount: 100,
		Confidence:  0.9,
	}
	if e.StrategyID != "s-1" {
		t.Errorf("StrategyID = %q, want s-1", e.StrategyID)
	}
	if e.SuccessRate != 0.85 {
		t.Errorf("SuccessRate = %v, want 0.85", e.SuccessRate)
	}
}

func TestMemoryAwareScoringConfigFields(t *testing.T) {
	cfg := MemoryAwareScoringConfig{
		Enabled:          true,
		MemoryWeight:     0.2,
		CostWeight:       0.1,
		LatencyWeight:    0.05,
		RegressionWeight: 0.1,
	}
	if !cfg.Enabled {
		t.Error("Enabled should be true")
	}
	if cfg.MemoryWeight != 0.2 {
		t.Errorf("MemoryWeight = %v, want 0.2", cfg.MemoryWeight)
	}
}

func TestStrategyLineageFields(t *testing.T) {
	l := StrategyLineage{
		ParentID:     "s0",
		ChildID:      "s1",
		MutationType: "crossover",
		WinRate:      0.65,
	}
	if l.ParentID != "s0" {
		t.Errorf("ParentID = %q, want s0", l.ParentID)
	}
	if l.WinRate != 0.65 {
		t.Errorf("WinRate = %v, want 0.65", l.WinRate)
	}
}

func TestStatsZeroValue(t *testing.T) {
	var s Stats
	if s.Generation != 0 {
		t.Errorf("zero Generation = %d, want 0", s.Generation)
	}
}

func TestEvolutionResultZeroValue(t *testing.T) {
	var r EvolutionResult
	if r.BestStrategy != nil {
		t.Error("zero BestStrategy should be nil")
	}
	if r.TotalGens != 0 {
		t.Errorf("zero TotalGens = %d, want 0", r.TotalGens)
	}
}
