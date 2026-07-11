package arena

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockScorer returns deterministic scores for testing.
type mockScorer struct {
	scores    map[int]float64 // call index → score
	callCount int
	mu        sync.Mutex
}

// newMockScorer creates a mock scorer with predefined scores.
func newMockScorer(scores map[int]float64) *mockScorer {
	return &mockScorer{
		scores: scores,
	}
}

// Score returns the predetermined score for the current call index.
func (m *mockScorer) Score(runResult any) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.callCount
	m.callCount++

	score, ok := m.scores[idx]
	if !ok {
		return 0.0, errors.New("mock: no score configured for this call")
	}
	return score, nil
}

// TestRegressionResult_DefaultFields verifies zero-value initialization.
func TestRegressionResult_DefaultFields(t *testing.T) {
	var result RegressionResult

	if result.OldStrategyID != "" {
		t.Errorf("expected empty OldStrategyID, got %q", result.OldStrategyID)
	}
	if result.NewStrategyID != "" {
		t.Errorf("expected empty NewStrategyID, got %q", result.NewStrategyID)
	}
	if result.OldAvg != 0 {
		t.Errorf("expected OldAvg 0, got %f", result.OldAvg)
	}
	if result.NewAvg != 0 {
		t.Errorf("expected NewAvg 0, got %f", result.NewAvg)
	}
	if result.WinRate != 0 {
		t.Errorf("expected WinRate 0, got %f", result.WinRate)
	}
	if result.Confident {
		t.Error("expected Confident false")
	}
	if result.Samples != 0 {
		t.Errorf("expected Samples 0, got %d", result.Samples)
	}
	if result.PValue != 0 {
		t.Errorf("expected PValue 0, got %f", result.PValue)
	}
	if !result.TestedAt.IsZero() {
		t.Error("expected TestedAt to be zero")
	}
	if len(result.OldScores) != 0 {
		t.Error("expected empty OldScores")
	}
	if len(result.NewScores) != 0 {
		t.Error("expected empty NewScores")
	}
}

// TestDefaultRegressionConfig verifies default configuration values.
func TestDefaultRegressionConfig(t *testing.T) {
	cfg := DefaultRegressionConfig()

	if cfg.BaselineRuns != 5 {
		t.Errorf("expected BaselineRuns 5, got %d", cfg.BaselineRuns)
	}
	if cfg.CompareRuns != 5 {
		t.Errorf("expected CompareRuns 5, got %d", cfg.CompareRuns)
	}
	if cfg.TestSuite != "" {
		t.Errorf("expected empty TestSuite, got %q", cfg.TestSuite)
	}
	if cfg.Confidence != 0.05 {
		t.Errorf("expected Confidence 0.05, got %f", cfg.Confidence)
	}
	if cfg.MinWinRate != 0.55 {
		t.Errorf("expected MinWinRate 0.55, got %f", cfg.MinWinRate)
	}
	if cfg.OldStrategy != nil {
		t.Error("expected nil OldStrategy")
	}
	if cfg.NewStrategy != nil {
		t.Error("expected nil NewStrategy")
	}
}

// TestNewRegressionTester_ValidArgs verifies successful creation.
func TestNewRegressionTester_ValidArgs(t *testing.T) {
	service := NewService(nil, nil, nil)
	scorer := newMockScorer(map[int]float64{0: 1.0})

	tester, err := NewRegressionTester(service, scorer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tester == nil {
		t.Fatal("expected non-nil tester")
	}
	if tester.arena != service {
		t.Error("arena not set correctly")
	}
	if tester.scorer != scorer {
		t.Error("scorer not set correctly")
	}
}

// TestNewRegressionTester_NilArena checks nil arena rejection.
func TestNewRegressionTester_NilArena(t *testing.T) {
	scorer := newMockScorer(map[int]float64{0: 1.0})

	tester, err := NewRegressionTester(nil, scorer)
	if err == nil {
		t.Fatal("expected error for nil arena")
	}
	if tester != nil {
		t.Error("expected nil tester on error")
	}
	if !errors.Is(err, ErrNilArena) {
		t.Errorf("expected ErrNilArena, got %v", err)
	}
}

// TestNewRegressionTester_NilScorer checks nil scorer rejection.
func TestNewRegressionTester_NilScorer(t *testing.T) {
	service := NewService(nil, nil, nil)

	tester, err := NewRegressionTester(service, nil)
	if err == nil {
		t.Fatal("expected error for nil scorer")
	}
	if tester != nil {
		t.Error("expected nil tester on error")
	}
	if !errors.Is(err, ErrNilScorer) {
		t.Errorf("expected ErrNilScorer, got %v", err)
	}
}

// TestRun_NewBetterStrategy verifies high win rate and confidence when new is better.
func TestRun_NewBetterStrategy(t *testing.T) {
	service := NewService(nil, nil, nil)

	// Use separate scorers for each strategy to avoid race conditions.
	oldScorer := newMockScorer(map[int]float64{
		0: 55.0, 1: 52.0, 2: 58.0, 3: 54.0, 4: 56.0,
	})
	newScorer := newMockScorer(map[int]float64{
		0: 85.0, 1: 88.0, 2: 82.0, 3: 90.0, 4: 86.0,
	})

	// Create a composite scorer that routes to the correct scorer based on input.
	compositeScorer := &compositeScorer{
		scorers: map[any]Scorer{
			"old-strategy": oldScorer,
			"new-strategy": newScorer,
		},
	}

	tester, err := NewRegressionTester(service, compositeScorer)
	if err != nil {
		t.Fatalf("failed to create tester: %v", err)
	}

	cfg := RegressionConfig{
		OldStrategy:  "old-strategy",
		NewStrategy:  "new-strategy",
		BaselineRuns: 5,
		CompareRuns:  5,
		Confidence:   0.05,
	}

	result, err := tester.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.WinRate < 0.8 {
		t.Errorf("expected WinRate >= 0.8, got %f", result.WinRate)
	}
	if !result.Confident {
		t.Error("expected Confident true for obvious improvement")
	}
	if result.OldAvg < 50 || result.OldAvg > 60 {
		t.Errorf("expected OldAvg between 50-60, got %f", result.OldAvg)
	}
	if result.NewAvg < 80 || result.NewAvg > 90 {
		t.Errorf("expected NewAvg between 80-90, got %f", result.NewAvg)
	}
	if result.Samples != 5 {
		t.Errorf("expected Samples 5, got %d", result.Samples)
	}
	if result.TestedAt.IsZero() {
		t.Error("expected TestedAt to be set")
	}
}

// TestRun_OldBetterStrategy verifies low win rate when new is worse.
func TestRun_OldBetterStrategy(t *testing.T) {
	service := NewService(nil, nil, nil)

	oldScorer := newMockScorer(map[int]float64{
		0: 85.0, 1: 88.0, 2: 82.0, 3: 90.0, 4: 86.0,
	})
	newScorer := newMockScorer(map[int]float64{
		0: 55.0, 1: 52.0, 2: 58.0, 3: 54.0, 4: 56.0,
	})

	compositeScorer := &compositeScorer{
		scorers: map[any]Scorer{
			"old": oldScorer,
			"new": newScorer,
		},
	}

	tester, err := NewRegressionTester(service, compositeScorer)
	if err != nil {
		t.Fatalf("failed to create tester: %v", err)
	}

	cfg := RegressionConfig{
		OldStrategy:  "old",
		NewStrategy:  "new",
		BaselineRuns: 5,
		CompareRuns:  5,
		Confidence:   0.05,
	}

	result, err := tester.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.WinRate > 0.2 {
		t.Errorf("expected WinRate <= 0.2, got %f", result.WinRate)
	}
	if result.OldAvg < 80 {
		t.Errorf("expected OldAvg > 80, got %f", result.OldAvg)
	}
	if result.NewAvg > 60 {
		t.Errorf("expected NewAvg < 60, got %f", result.NewAvg)
	}
}

// TestRun_EqualStrategies verifies win rate ~0.5 when strategies are equal.
func TestRun_EqualStrategies(t *testing.T) {
	service := NewService(nil, nil, nil)

	// Both strategies return identical scores.
	baseScore := 70.0
	scores := make([]float64, 10)
	for i := range scores {
		scores[i] = baseScore
	}

	combinedScorer := &sequentialScorer{scores: scores}

	tester, err := NewRegressionTester(service, combinedScorer)
	if err != nil {
		t.Fatalf("failed to create tester: %v", err)
	}

	cfg := RegressionConfig{
		OldStrategy:  "strategy-a",
		NewStrategy:  "strategy-b",
		BaselineRuns: 5,
		CompareRuns:  5,
		Confidence:   0.05,
	}

	result, err := tester.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Win rate should be exactly 1.0 since all scores are equal (new >= old).
	if result.WinRate < 0.9 {
		t.Errorf("expected WinRate >= 0.9 for identical strategies, got %f", result.WinRate)
	}
	// Should NOT be confident since there's no real difference.
	if result.Confident {
		t.Error("expected Confident false for identical strategies")
	}
	if mathAbs(result.OldAvg-result.NewAvg) > 0.001 {
		t.Errorf("expected similar averages, OldAvg=%f, NewAvg=%f", result.OldAvg, result.NewAvg)
	}
}

// TestComputeSignificance_ObviousDifference checks statistical significance detection.
func TestComputeSignificance_ObviousDifference(t *testing.T) {
	oldScores := []float64{10.0, 11.0, 10.0, 12.0, 11.0} // Mean ~10.8
	newScores := []float64{90.0, 91.0, 89.0, 92.0, 90.0} // Mean ~90.4

	confident, pValue := computeSignificance(oldScores, newScores, 0.05)

	if !confident {
		t.Error("expected confident true for obvious difference")
	}
	if pValue >= 0.05 {
		t.Errorf("expected p-value < 0.05, got %f", pValue)
	}
	if pValue < 0 {
		t.Errorf("p-value should be non-negative, got %f", pValue)
	}
}

// TestComputeSignificance_NoDifference checks no significance for similar data.
func TestComputeSignificance_NoDifference(t *testing.T) {
	scores := []float64{50.0, 51.0, 49.0, 50.0, 51.0, 50.0, 49.0, 51.0}

	confident, pValue := computeSignificance(scores, scores, 0.05)

	if confident {
		t.Error("expected confident false for identical data")
	}
	if pValue < 0.5 { // p-value should be very high for identical data
		t.Errorf("expected p-value >= 0.5, got %f", pValue)
	}
}

// TestComputeSignificance_SingleSample handles edge case with minimal samples.
func TestComputeSignificance_SingleSample(t *testing.T) {
	oldScores := []float64{50.0}
	newScores := []float64{60.0}

	confident, pValue := computeSignificance(oldScores, newScores, 0.05)

	if confident {
		t.Error("expected confident false with single sample")
	}
	if pValue != 1.0 {
		t.Errorf("expected p-value 1.0 for single sample, got %f", pValue)
	}
}

// TestComputeSignificance_EmptySlices handles empty input gracefully.
func TestComputeSignificance_EmptySlices(t *testing.T) {
	confident, pValue := computeSignificance([]float64{}, []float64{1.0}, 0.05)

	if confident {
		t.Error("expected confident false with empty slice")
	}
	if pValue != 1.0 {
		t.Errorf("expected p-value 1.0 for empty slice, got %f", pValue)
	}
}

// TestRun_CancelByContext verifies context cancellation propagation.
func TestRun_CancelByContext(t *testing.T) {
	service := NewService(nil, nil, nil)

	// Scorer that simulates slow scoring.
	slowScorer := &slowScorer{delay: 100 * time.Millisecond}

	tester, err := NewRegressionTester(service, slowScorer)
	if err != nil {
		t.Fatalf("failed to create tester: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	cfg := RegressionConfig{
		OldStrategy:  "old",
		NewStrategy:  "new",
		BaselineRuns: 3,
		CompareRuns:  3,
	}

	start := time.Now()
	result, err := tester.Run(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error from context cancellation")
	}
	if result != nil {
		t.Error("expected nil result on cancellation")
	}
	// Should have cancelled quickly, not waited for all runs.
	if elapsed > 200*time.Millisecond {
		t.Errorf("took too long to cancel: %v", elapsed)
	}
}

// TestRun_InvalidConfig checks configuration validation.
func TestRun_InvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RegressionConfig
		wantErr error
	}{
		{
			name: "nil old strategy",
			cfg: RegressionConfig{
				OldStrategy:  nil,
				NewStrategy:  "new",
				BaselineRuns: 5,
				CompareRuns:  5,
			},
			wantErr: ErrNilStrategy,
		},
		{
			name: "nil new strategy",
			cfg: RegressionConfig{
				OldStrategy:  "old",
				NewStrategy:  nil,
				BaselineRuns: 5,
				CompareRuns:  5,
			},
			wantErr: ErrNilStrategy,
		},
		{
			name: "negative baseline runs",
			cfg: RegressionConfig{
				OldStrategy:  "old",
				NewStrategy:  "new",
				BaselineRuns: -1,
				CompareRuns:  5,
			},
			wantErr: ErrInvalidRuns,
		},
		{
			name: "confidence out of range",
			cfg: RegressionConfig{
				OldStrategy:  "old",
				NewStrategy:  "new",
				BaselineRuns: 5,
				CompareRuns:  5,
				Confidence:   1.5,
			},
			wantErr: ErrConfidenceRange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(nil, nil, nil)
			scorer := newMockScorer(map[int]float64{0: 1.0})
			tester, err := NewRegressionTester(service, scorer)
			if err != nil {
				t.Fatalf("failed to create tester: %v", err)
			}

			result, err := tester.Run(context.Background(), tt.cfg)
			if err == nil {
				t.Error("expected error but got none")
			}
			if result != nil {
				t.Error("expected nil result on validation error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestComputeMean verifies mean calculation.
func TestComputeMean(t *testing.T) {
	tests := []struct {
		name   string
		scores []float64
		want   float64
	}{
		{"empty slice", []float64{}, 0},
		{"single value", []float64{42.0}, 42.0},
		{"multiple values", []float64{10.0, 20.0, 30.0}, 20.0},
		{"negative values", []float64{-10.0, 10.0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeMean(tt.scores)
			if mathAbs(got-tt.want) > 1e-9 {
				t.Errorf("computeMean(%v) = %f, want %f", tt.scores, got, tt.want)
			}
		})
	}
}

// TestComputeVariance verifies variance calculation.
func TestComputeVariance(t *testing.T) {
	tests := []struct {
		name   string
		scores []float64
		want   float64
	}{
		{"empty slice", []float64{}, 0},
		{"constant values", []float64{5.0, 5.0, 5.0}, 0.0},
		{"varying values", []float64{2.0, 4.0, 6.0}, 4.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeVariance(tt.scores)
			if mathAbs(got-tt.want) > 1e-6 {
				t.Errorf("computeVariance(%v) = %f, want %f", tt.scores, got, tt.want)
			}
		})
	}
}

// TestComputeWinRate verifies win rate calculation.
func TestComputeWinRate(t *testing.T) {
	tests := []struct {
		name      string
		oldScores []float64
		newScores []float64
		want      float64
	}{
		{"new wins all", []float64{1, 2, 3}, []float64{4, 5, 6}, 1.0},
		{"old wins all", []float64{4, 5, 6}, []float64{1, 2, 3}, 0.0},
		{"mixed results", []float64{5, 3, 7}, []float64{4, 6, 2}, 1.0 / 3.0},
		{"equal scores", []float64{5, 5, 5}, []float64{5, 5, 5}, 1.0},
		{"empty slices", []float64{}, []float64{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeWinRate(tt.oldScores, tt.newScores)
			if mathAbs(got-tt.want) > 1e-9 {
				t.Errorf("computeWinRate(%v, %v) = %f, want %f",
					tt.oldScores, tt.newScores, got, tt.want)
			}
		})
	}
}

// sequentialScorer returns scores in sequence for deterministic testing.
type sequentialScorer struct {
	scores []float64
	idx    int
	mu     sync.Mutex
}

// Score returns the next score in sequence.
func (s *sequentialScorer) Score(runResult any) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx >= len(s.scores) {
		return 0.0, errors.New("sequential scorer: no more scores")
	}
	score := s.scores[s.idx]
	s.idx++
	return score, nil
}

// slowScorer introduces artificial delay for timeout testing.
type slowScorer struct {
	delay time.Duration
}

// Score sleeps before returning to simulate slow processing.
func (s *slowScorer) Score(runResult any) (float64, error) {
	time.Sleep(s.delay)
	return 75.0, nil
}

// compositeScorer routes scoring requests to different scorers based on input.
type compositeScorer struct {
	scorers map[any]Scorer
	mu      sync.Mutex
}

// Score routes to the appropriate scorer based on the input key.
// If the input is a TestCaseInput, it unpacks the embedded strategy as the key.
func (c *compositeScorer) Score(input any) (float64, error) {
	key := input
	if tci, ok := input.(TestCaseInput); ok {
		key = tci.Strategy
	}

	c.mu.Lock()
	scorer, ok := c.scorers[key]
	c.mu.Unlock()

	if !ok {
		return 0.0, fmt.Errorf("composite scorer: no scorer for input %v", input)
	}
	return scorer.Score(key)
}

// mathAbs is a helper to avoid importing math in test file.
func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
