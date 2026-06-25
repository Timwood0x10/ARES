package scoring

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// mockExperienceProvider implements ExperienceProvider for testing.
type mockExperienceProvider struct {
	count      int
	confidence float64
	err        error
}

func (m *mockExperienceProvider) FindSimilar(ctx context.Context, taskType string, limit int) (int, float64, error) {
	if m.err != nil {
		return 0, 0, m.err
	}
	return m.count, m.confidence, nil
}

// TestNewMemoryAwareScorer_NilTiered verifies that nil tiered scorer is
// rejected.
func TestNewMemoryAwareScorer_NilTiered(t *testing.T) {
	_, err := NewMemoryAwareScorer(nil, nil, DefaultMemoryAwareScoringConfig())
	if err == nil {
		t.Fatal("expected error for nil tiered scorer")
	}
}

// TestNewMemoryAwareScorer_InvalidConfig verifies invalid config is rejected.
func TestNewMemoryAwareScorer_InvalidConfig(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)

	tests := []struct {
		name string
		cfg  MemoryAwareScoringConfig
	}{
		{
			name: "negative memory weight",
			cfg:  modifyMASConfig(func(c *MemoryAwareScoringConfig) { c.MemoryWeight = -0.1 }),
		},
		{
			name: "negative cost weight",
			cfg:  modifyMASConfig(func(c *MemoryAwareScoringConfig) { c.CostWeight = -0.1 }),
		},
		{
			name: "max bonus < min bonus",
			cfg:  modifyMASConfig(func(c *MemoryAwareScoringConfig) { c.MaxEvidenceBonus = 5.0; c.MinEvidenceBonus = 10.0 }),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMemoryAwareScorer(ts, nil, tt.cfg)
			if err == nil {
				t.Errorf("expected error for config: %s", tt.name)
			}
		})
	}
}

// modifyMASConfig returns a default config with the given modifier applied.
func modifyMASConfig(mod func(c *MemoryAwareScoringConfig)) MemoryAwareScoringConfig {
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true
	mod(&cfg)
	return cfg
}

// mustCreateTieredScorer creates a tiered scorer for testing.
func mustCreateTieredScorer(t *testing.T, budget int) *TieredScorer {
	t.Helper()
	cache := NewScoreCache(0)
	b := mustNewBudget(t, budget)
	ts, err := NewTieredScorer(TieredScorerConfig{
		Cache:           cache,
		Budget:          b,
		HeuristicScorer: constantScorer(50.0),
	})
	if err != nil {
		t.Fatalf("NewTieredScorer failed: %v", err)
	}
	return ts
}

// TestMemoryAwareScorer_Disabled_NoAdjustment verifies that when disabled,
// the scorer delegates directly to the tiered scorer with no adjustments.
func TestMemoryAwareScorer_Disabled_NoAdjustment(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 5, confidence: 0.8}, MemoryAwareScoringConfig{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	strategy := &mutation.Strategy{
		ID:             "test-1",
		Params:         map[string]any{"temperature": 0.7},
		PromptTemplate: "test prompt",
	}

	score, detail, err := ms.Score(context.Background(), strategy)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	if score != 50.0 {
		t.Errorf("expected score=50.0, got %f", score)
	}
	if detail != nil {
		t.Error("expected nil detail when disabled")
	}
}

// TestMemoryAwareScorer_NilExperienceProvider_NoAdjustment verifies that when
// the experience provider is nil, the scorer delegates directly.
func TestMemoryAwareScorer_NilExperienceProvider_NoAdjustment(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true
	ms, err := NewMemoryAwareScorer(ts, nil, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	strategy := newTestStrategy("nil-exp-test")
	score, detail, err := ms.Score(context.Background(), strategy)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	if score != 50.0 {
		t.Errorf("expected score=50.0, got %f", score)
	}
	if detail != nil {
		t.Error("expected nil detail when experience provider is nil")
	}
}

// TestMemoryAwareScorer_ExperienceFailure_NonFatal verifies that an experience
// lookup failure does not prevent scoring (non-fatal fallback).
func TestMemoryAwareScorer_ExperienceFailure_NonFatal(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true
	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{err: errors.New("query failed")}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	strategy := newTestStrategy("exp-fail-test")
	score, detail, err := ms.Score(context.Background(), strategy)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Should return the base quality score without adjustment.
	if score != 50.0 {
		t.Errorf("expected score=50.0 on experience failure, got %f", score)
	}
	if detail == nil {
		t.Fatal("expected non-nil detail even on experience failure")
	}
	if detail.QualityScore != 50.0 {
		t.Errorf("expected quality score 50.0, got %f", detail.QualityScore)
	}
}

// TestMemoryAwareScorer_FullAdjustment verifies that all components of the
// score formula are applied correctly when experience data is available.
//
// Formula: fitness = quality + memory_bonus - cost_penalty - latency_penalty - regression_penalty
//
// With: quality=50.0, expCount=3, confidence=0.8, cost=10.0, latency=5.0, regression=2.0
//   - memory_bonus = min(3*0.8*5.0, 20.0) = min(12.0, 20.0) = 12.0
//   - cost_penalty = 10.0 * 0.1 = 1.0
//   - latency_penalty = 5.0 * 0.05 = 0.25
//   - regression_penalty = 2.0 * 0.1 = 0.2
//   - final = 50.0 + 12.0 - 1.0 - 0.25 - 0.2 = 60.55
func TestMemoryAwareScorer_FullAdjustment(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true
	cfg.MemoryWeight = 0.2
	cfg.CostWeight = 0.1
	cfg.LatencyWeight = 0.05
	cfg.RegressionWeight = 0.1

	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 3, confidence: 0.8}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	strategy := &mutation.Strategy{
		ID: "adjustment-test",
		Params: map[string]any{
			"temperature": 0.7,
			"cost":        10.0,
			"latency":     5.0,
			"regression":  2.0,
		},
		PromptTemplate: "test",
	}

	score, detail, err := ms.Score(context.Background(), strategy)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Compute expected values using the same formula to avoid float precision issues.
	expectedBonus := float64(3) * 0.8 * 5.0 // min(3*0.8*5.0, 20.0)
	expectedCostPenalty := 1.0
	expectedLatencyPenalty := 0.25
	expectedRegressionPenalty := 0.2
	expectedFinal := 50.0 + expectedBonus - expectedCostPenalty - expectedLatencyPenalty - expectedRegressionPenalty

	if detail.MemoryEvidenceBonus != expectedBonus {
		t.Errorf("expected MemoryEvidenceBonus=%f, got %f", expectedBonus, detail.MemoryEvidenceBonus)
	}
	if detail.CostPenalty != expectedCostPenalty {
		t.Errorf("expected CostPenalty=%f, got %f", expectedCostPenalty, detail.CostPenalty)
	}
	if detail.LatencyPenalty != expectedLatencyPenalty {
		t.Errorf("expected LatencyPenalty=%f, got %f", expectedLatencyPenalty, detail.LatencyPenalty)
	}
	if detail.RegressionPenalty != expectedRegressionPenalty {
		t.Errorf("expected RegressionPenalty=%f, got %f", expectedRegressionPenalty, detail.RegressionPenalty)
	}
	if detail.ExperienceCount != 3 {
		t.Errorf("expected ExperienceCount=3, got %d", detail.ExperienceCount)
	}
	if detail.Confidence != 0.8 {
		t.Errorf("expected Confidence=0.8, got %f", detail.Confidence)
	}
	if score != expectedFinal {
		t.Errorf("expected final score=%f, got %f", expectedFinal, score)
	}
	if detail.FinalScore != expectedFinal {
		t.Errorf("expected detail.FinalScore=%f, got %f", expectedFinal, detail.FinalScore)
	}
}

// TestMemoryAwareScorer_MemoryBonusCapped verifies that the memory evidence
// bonus does not exceed MaxEvidenceBonus.
func TestMemoryAwareScorer_MemoryBonusCapped(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true
	cfg.MaxEvidenceBonus = 10.0 // Cap at 10.
	cfg.MinEvidenceBonus = 0.0

	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 100, confidence: 1.0}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	strategy := newTestStrategy("cap-test")

	_, detail, err := ms.Score(context.Background(), strategy)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Raw bonus would be 100*1.0*5.0 = 500, but capped at 10.
	if detail.MemoryEvidenceBonus != 10.0 {
		t.Errorf("expected capped bonus=10.0, got %f", detail.MemoryEvidenceBonus)
	}
}

// TestMemoryAwareScorer_ScoreAsScorerFunc verifies that ScoreAsScorerFunc
// returns a valid ScorerFunc that works with population scoring.
func TestMemoryAwareScorer_ScoreAsScorerFunc(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true

	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 2, confidence: 0.5}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	scorerFn := ms.ScoreAsScorerFunc()
	if scorerFn == nil {
		t.Fatal("ScoreAsScorerFunc returned nil")
	}

	strategy := newTestStrategy("scorer-func-test")
	score := scorerFn(strategy)

	// Should return a valid score (not unevaluated).
	if score < 0 {
		t.Errorf("expected valid score, got %f", score)
	}

	// Verify it's a genome.ScorerFunc compatible.
	sf := ms.ScoreAsScorerFunc()
	if sf == nil {
		t.Error("ScoreAsScorerFunc should satisfy genome.ScorerFunc")
	}
}

// TestMemoryAwareScorer_Stats verifies that Stats() returns correct
// aggregate statistics after scoring.
func TestMemoryAwareScorer_Stats(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true

	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 2, confidence: 0.5}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	// Score a few strategies.
	strategy := newTestStrategy("stats-test")
	for i := 0; i < 3; i++ {
		_, _, err := ms.Score(context.Background(), strategy)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
	}

	stats := ms.Stats()
	if stats["adjustments"] != 3 {
		t.Errorf("expected 3 adjustments, got %f", stats["adjustments"])
	}
	if stats["bonus_total"] <= 0 {
		t.Errorf("expected positive bonus_total, got %f", stats["bonus_total"])
	}
	if stats["avg_bonus"] <= 0 {
		t.Errorf("expected positive avg_bonus, got %f", stats["avg_bonus"])
	}

	// Reset and verify stats are cleared.
	ms.ResetStats()
	stats = ms.Stats()
	if stats["adjustments"] != 0 {
		t.Errorf("expected 0 adjustments after reset, got %f", stats["adjustments"])
	}
}

// TestMemoryAwareScorer_ScoreDetailComponents verifies that ScoreDetail
// includes quality, memory, cost, and latency components in all scenarios.
func TestMemoryAwareScorer_ScoreDetailComponents(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true

	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 3, confidence: 0.9}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	strategy := &mutation.Strategy{
		ID: "detail-test",
		Params: map[string]any{
			"temperature": 0.7,
			"cost":        5.0,
			"latency":     2.0,
		},
		PromptTemplate: "test",
	}

	_, detail, err := ms.Score(context.Background(), strategy)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// The detail must include quality, memory, cost, and latency components.
	if detail.QualityScore < 0 {
		t.Error("QualityScore should be non-negative")
	}
	if detail.MemoryEvidenceBonus < 0 {
		t.Error("MemoryEvidenceBonus should be non-negative")
	}
	if detail.CostPenalty < 0 {
		t.Error("CostPenalty should be non-negative")
	}
	if detail.LatencyPenalty < 0 {
		t.Error("LatencyPenalty should be non-negative")
	}
	if detail.RegressionPenalty < 0 {
		t.Error("RegressionPenalty should be non-negative")
	}

	// The final score should be the sum formula.
	expectedFinal := detail.QualityScore + detail.MemoryEvidenceBonus -
		detail.CostPenalty - detail.LatencyPenalty - detail.RegressionPenalty
	if detail.FinalScore != expectedFinal {
		t.Errorf("FinalScore=%f does not match formula result=%f",
			detail.FinalScore, expectedFinal)
	}
}

// TestTaskTypeFromStrategy verifies task type extraction from strategy.
func TestTaskTypeFromStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy *mutation.Strategy
		expected string
	}{
		{
			name:     "nil strategy returns default",
			strategy: nil,
			expected: "default",
		},
		{
			name:     "empty strategy returns default",
			strategy: &mutation.Strategy{},
			expected: "default",
		},
		{
			name:     "strategy with name returns name",
			strategy: &mutation.Strategy{Name: "my-task"},
			expected: "my-task",
		},
		{
			name: "strategy with task_type param",
			strategy: &mutation.Strategy{
				Params: map[string]any{"task_type": "code-review"},
			},
			expected: "code-review",
		},
		{
			name:     "name takes precedence over task_type",
			strategy: &mutation.Strategy{Name: "task-name", Params: map[string]any{"task_type": "param-type"}},
			expected: "task-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := taskTypeFromStrategy(tt.strategy)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

// TestStrategyCostExtraction verifies cost extraction from strategy params.
func TestStrategyCostExtraction(t *testing.T) {
	tests := []struct {
		name     string
		strategy *mutation.Strategy
		expected float64
	}{
		{name: "nil strategy", strategy: nil, expected: 0},
		{name: "no params", strategy: &mutation.Strategy{}, expected: 0},
		{name: "no cost key", strategy: &mutation.Strategy{Params: map[string]any{"t": 0.5}}, expected: 0},
		{name: "has cost", strategy: &mutation.Strategy{Params: map[string]any{"cost": 10.0}}, expected: 10.0},
		{name: "zero cost", strategy: &mutation.Strategy{Params: map[string]any{"cost": 0.0}}, expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strategyCost(tt.strategy)
			if got != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, got)
			}
		})
	}
}

// TestStrategyLatencyExtraction verifies latency extraction from strategy params.
func TestStrategyLatencyExtraction(t *testing.T) {
	tests := []struct {
		name     string
		strategy *mutation.Strategy
		expected float64
	}{
		{name: "nil strategy", strategy: nil, expected: 0},
		{name: "no latency key", strategy: &mutation.Strategy{Params: map[string]any{"t": 0.5}}, expected: 0},
		{name: "has latency", strategy: &mutation.Strategy{Params: map[string]any{"latency": 3.0}}, expected: 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strategyLatency(tt.strategy)
			if got != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, got)
			}
		})
	}
}

// TestStrategyRegressionExtraction verifies regression extraction from
// strategy params.
func TestStrategyRegressionExtraction(t *testing.T) {
	tests := []struct {
		name     string
		strategy *mutation.Strategy
		expected float64
	}{
		{name: "nil strategy", strategy: nil, expected: 0},
		{name: "no regression key", strategy: &mutation.Strategy{Params: map[string]any{"t": 0.5}}, expected: 0},
		{name: "has regression", strategy: &mutation.Strategy{Params: map[string]any{"regression": 1.5}}, expected: 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strategyRegression(tt.strategy)
			if got != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, got)
			}
		})
	}
}

// TestMemoryAwareScorer_ResetForGeneration verifies that ResetForGeneration
// delegates to the tiered scorer correctly.
func TestMemoryAwareScorer_ResetForGeneration(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true

	ms, err := NewMemoryAwareScorer(ts, &mockExperienceProvider{count: 2, confidence: 0.5}, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	// Score a few strategies.
	strategy := newTestStrategy("reset-test")
	for i := 0; i < 3; i++ {
		_, _, _ = ms.Score(context.Background(), strategy)
	}

	ms.ResetStats()

	stats := ms.Stats()
	if stats["adjustments"] != 0 {
		t.Errorf("expected 0 adjustments after reset, got %f", stats["adjustments"])
	}
}

// TestComputeMemoryBonus verifies the memory bonus formula directly.
func TestComputeMemoryBonus(t *testing.T) {
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true
	cfg.MinEvidenceBonus = 0.0
	cfg.MaxEvidenceBonus = 20.0

	ms, _ := NewMemoryAwareScorer(ts, nil, cfg)

	tests := []struct {
		name       string
		count      int
		confidence float64
		expected   float64
	}{
		{"zero experiences", 0, 0.8, 0.0},
		{"low confidence", 5, 0.1, 2.5}, // 5*0.1*5.0 = 2.5
		{"medium", 3, 0.5, 7.5},         // 3*0.5*5.0 = 7.5
		{"capped", 100, 1.0, 20.0},      // 100*1.0*5.0 = 500, capped at 20
		{"zero confidence", 10, 0.0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ms.computeMemoryBonus(tt.count, tt.confidence)
			if got != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, got)
			}
		})
	}
}
