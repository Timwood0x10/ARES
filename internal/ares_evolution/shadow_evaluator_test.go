package evolution

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

func TestShadowEvaluator_New(t *testing.T) {
	t.Run("default_config", func(t *testing.T) {
		cfg := DefaultShadowEvaluationConfig()
		e := NewShadowEvaluator(cfg)
		if e == nil {
			t.Fatal("expected non-nil evaluator")
		}
		if e.minSamples != 10 {
			t.Errorf("expected minSamples=10, got %d", e.minSamples)
		}
		if e.minWinRate != 0.55 {
			t.Errorf("expected minWinRate=0.55, got %f", e.minWinRate)
		}
	})

	t.Run("zero_values_use_defaults", func(t *testing.T) {
		cfg := ShadowEvaluationConfig{
			MinSamples: 0,
			MinWinRate: 0,
		}
		e := NewShadowEvaluator(cfg)
		if e.minSamples != 10 {
			t.Errorf("expected minSamples default 10, got %d", e.minSamples)
		}
		if e.minWinRate != 0.55 {
			t.Errorf("expected minWinRate default 0.55, got %f", e.minWinRate)
		}
	})

	t.Run("custom_values", func(t *testing.T) {
		cfg := ShadowEvaluationConfig{
			MinSamples: 5,
			MinWinRate: 0.60,
		}
		e := NewShadowEvaluator(cfg)
		if e.minSamples != 5 {
			t.Errorf("expected minSamples=5, got %d", e.minSamples)
		}
		if e.minWinRate != 0.60 {
			t.Errorf("expected minWinRate=0.60, got %f", e.minWinRate)
		}
	})
}

func TestShadowEvaluator_StartShadow(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	active := &mutation.Strategy{ID: "active-v1"}
	candidate := &mutation.Strategy{ID: "candidate-v2"}

	e.SetActiveStrategy(active)
	e.StartShadow(candidate)

	if e.ShadowStrategy() != candidate {
		t.Error("shadow strategy not set correctly")
	}
	if e.ActiveStrategy() != active {
		t.Error("active strategy not preserved")
	}
}

func TestShadowEvaluator_StartShadow_ResetsResults(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate-1"})

	// Record some results.
	e.RecordResult(80, 90)
	e.RecordResult(85, 95)
	if len(e.Results()) != 2 {
		t.Errorf("expected 2 results, got %d", len(e.Results()))
	}

	// Start a new shadow evaluation; results should be reset.
	e.StartShadow(&mutation.Strategy{ID: "candidate-2"})
	if len(e.Results()) != 0 {
		t.Errorf("expected 0 results after reset, got %d", len(e.Results()))
	}
}

func TestShadowEvaluator_RecordResult(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	e.RecordResult(80, 90)
	e.RecordResult(85, 95)
	e.RecordResult(90, 85) // Shadow loses this one.

	results := e.Results()
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if !results[0].ShadowWon {
		t.Error("expected result[0] shadow to win (90 > 80)")
	}
	if !results[1].ShadowWon {
		t.Error("expected result[1] shadow to win (95 > 85)")
	}
	if results[2].ShadowWon {
		t.Error("expected result[2] shadow to lose (85 < 90)")
	}
}

func TestShadowEvaluator_ShouldDeploy_BelowMinSamples(t *testing.T) {
	e := NewShadowEvaluator(ShadowEvaluationConfig{
		Enabled:    true,
		MinSamples: 5,
		MinWinRate: 0.55,
	})
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	// Record only 3 results (below min of 5).
	e.RecordResult(80, 90)
	e.RecordResult(85, 95)
	e.RecordResult(90, 85)

	deploy, report := e.ShouldDeploy()
	if deploy {
		t.Error("expected false deployment below min samples")
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if !strings.Contains(report.Recommendation, "insufficient samples") {
		t.Errorf("expected 'insufficient samples' recommendation, got: %s", report.Recommendation)
	}
	if report.TotalComparisons != 3 {
		t.Errorf("expected 3 total comparisons, got %d", report.TotalComparisons)
	}
}

func TestShadowEvaluator_ShouldDeploy_AboveThreshold(t *testing.T) {
	e := NewShadowEvaluator(ShadowEvaluationConfig{
		Enabled:    true,
		MinSamples: 5,
		MinWinRate: 0.55,
	})
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	// Record 5 results; shadow wins 4 (80% win rate, above 55% threshold).
	e.RecordResult(80, 95) // Shadow wins
	e.RecordResult(85, 90) // Shadow wins
	e.RecordResult(90, 92) // Shadow wins
	e.RecordResult(87, 91) // Shadow wins
	e.RecordResult(95, 85) // Shadow loses

	deploy, report := e.ShouldDeploy()
	if !deploy {
		t.Error("expected true deployment when win rate exceeds threshold")
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.ShadowWins != 4 {
		t.Errorf("expected 4 shadow wins, got %d", report.ShadowWins)
	}
	if report.WinRate != 0.80 {
		t.Errorf("expected 0.80 win rate, got %f", report.WinRate)
	}
	if !strings.Contains(report.Recommendation, "recommend deployment") {
		t.Errorf("expected 'recommend deployment' recommendation, got: %s", report.Recommendation)
	}
}

func TestShadowEvaluator_ShouldDeploy_BelowThreshold(t *testing.T) {
	e := NewShadowEvaluator(ShadowEvaluationConfig{
		Enabled:    true,
		MinSamples: 5,
		MinWinRate: 0.55,
	})
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	// Record 5 results; shadow wins only 2 (40% win rate, below 55% threshold).
	e.RecordResult(80, 75) // Shadow loses
	e.RecordResult(85, 82) // Shadow loses
	e.RecordResult(90, 95) // Shadow wins
	e.RecordResult(87, 85) // Shadow loses
	e.RecordResult(95, 98) // Shadow wins

	deploy, report := e.ShouldDeploy()
	if deploy {
		t.Error("expected false deployment when win rate below threshold")
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.ShadowWins != 2 {
		t.Errorf("expected 2 shadow wins, got %d", report.ShadowWins)
	}
	if !strings.Contains(report.Recommendation, "keep active") {
		t.Errorf("expected 'keep active' recommendation, got: %s", report.Recommendation)
	}
}

func TestShadowEvaluator_ShouldDeploy_NoResults(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	deploy, report := e.ShouldDeploy()
	if deploy {
		t.Error("expected false deployment with no results")
	}
	if report != nil {
		t.Error("expected nil report with no results")
	}
}

func TestShadowEvaluator_Reset(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})
	e.RecordResult(80, 90)

	e.Reset()
	if e.ShadowStrategy() != nil {
		t.Error("expected nil shadow strategy after reset")
	}
	if len(e.Results()) != 0 {
		t.Error("expected empty results after reset")
	}
}

func TestShadowEvaluator_ThreadSafety(t *testing.T) {
	e := NewShadowEvaluator(ShadowEvaluationConfig{
		MinSamples: 100,
		MinWinRate: 0.55,
	})
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	var wg sync.WaitGroup
	n := 50

	// Concurrently record results.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(score float64) {
			defer wg.Done()
			e.RecordResult(80, score)
		}(float64(70 + i%30))
	}

	// Concurrently read results.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.Results()
			_, _ = e.ShouldDeploy()
		}()
	}

	wg.Wait()

	results := e.Results()
	if len(results) != n {
		t.Errorf("expected %d results, got %d", n, len(results))
	}
}

func TestShadowEvaluator_SetActiveStrategy(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	s1 := &mutation.Strategy{ID: "v1"}
	s2 := &mutation.Strategy{ID: "v2"}

	e.SetActiveStrategy(s1)
	if e.ActiveStrategy() != s1 {
		t.Error("expected s1 as active strategy")
	}

	e.SetActiveStrategy(s2)
	if e.ActiveStrategy() != s2 {
		t.Error("expected s2 as active strategy after change")
	}
}

func TestShadowEvaluator_ShadowWonTie(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	// Equal scores: shadow should NOT be considered as winning.
	e.RecordResult(80, 80)
	results := e.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ShadowWon {
		t.Error("expected shadow not to win on equal scores")
	}
}

func TestShadowEvaluator_ResultsReturnsCopy(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	e.RecordResult(80, 90)
	e.RecordResult(85, 95)

	results := e.Results()
	resultsCopy := make([]ShadowComparison, len(results))
	copy(resultsCopy, results)

	// Modify the returned copy.
	resultsCopy[0].ShadowWon = !resultsCopy[0].ShadowWon
	resultsCopy[0].ActiveScore = 999

	// Original should be unchanged.
	original := e.Results()
	if original[0].ActiveScore == 999 {
		t.Error("Results() should return a copy, not a reference")
	}
}

func TestShadowEvaluator_TimestampsAreSet(t *testing.T) {
	e := NewShadowEvaluator(DefaultShadowEvaluationConfig())
	e.SetActiveStrategy(&mutation.Strategy{ID: "active"})
	e.StartShadow(&mutation.Strategy{ID: "candidate"})

	before := time.Now()
	e.RecordResult(80, 90)
	after := time.Now()

	results := e.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	ts := results[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Error("timestamp should be between before and after")
	}
}
