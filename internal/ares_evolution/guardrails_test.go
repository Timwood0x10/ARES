package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEvolutionGuardrails_ValidOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    []GuardrailOption
		wantErr bool
		check   func(t *testing.T, g *EvolutionGuardrails)
	}{
		{
			name: "default_options",
			opts: []GuardrailOption{},
			check: func(t *testing.T, g *EvolutionGuardrails) {
				assert.Equal(t, 0.0, g.BaselineScore)
				assert.Equal(t, 10, g.MaxStagnantGenerations)
				assert.Equal(t, 0.8, g.MaxLineageShare)
				assert.Equal(t, 1000, g.MaxEvents)
			},
		},
		{
			name: "custom_baseline_score",
			opts: []GuardrailOption{WithBaselineScore(85.5)},
			check: func(t *testing.T, g *EvolutionGuardrails) {
				assert.Equal(t, 85.5, g.BaselineScore)
			},
		},
		{
			name: "custom_stagnant_generations",
			opts: []GuardrailOption{WithMaxStagnantGenerations(20)},
			check: func(t *testing.T, g *EvolutionGuardrails) {
				assert.Equal(t, 20, g.MaxStagnantGenerations)
			},
		},
		{
			name: "custom_lineage_share",
			opts: []GuardrailOption{WithMaxLineageShare(0.6)},
			check: func(t *testing.T, g *EvolutionGuardrails) {
				assert.Equal(t, 0.6, g.MaxLineageShare)
			},
		},
		{
			name: "all_custom_options",
			opts: []GuardrailOption{
				WithBaselineScore(90.0),
				WithMaxStagnantGenerations(15),
				WithMaxLineageShare(0.7),
			},
			check: func(t *testing.T, g *EvolutionGuardrails) {
				assert.Equal(t, 90.0, g.BaselineScore)
				assert.Equal(t, 15, g.MaxStagnantGenerations)
				assert.Equal(t, 0.7, g.MaxLineageShare)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := NewEvolutionGuardrails(tt.opts...)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, g)
			tt.check(t, g)
		})
	}
}

func TestPreEvolveCheck_NormalCase(t *testing.T) {
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()
	result := g.PreEvolveCheck(ctx, 75.0, 1, 100, 10)

	assert.False(t, result.ShouldStop)
	assert.Empty(t, result.Events)
}

func TestPreEvolveCheck_MajorityUnevaluated(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()
	result := g.PreEvolveCheck(ctx, 75.0, 1, 100, 60)

	assert.True(t, result.ShouldStop)
	require.Len(t, result.Events, 1)
	assert.Equal(t, GuardrailCritical, result.Events[0].Level)
	assert.Equal(t, "unevaluated_population", result.Events[0].Rule)
	assert.Contains(t, result.Events[0].Message, "majority population unevaluated")
	assert.Equal(t, 1, result.Events[0].Generation)
}

func TestPreEvolveCheck_StagnationExceeded(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(3),
	)

	// Simulate stagnation by calling PostEvolveCheck multiple times without improvement
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		g.PostEvolveCheck(ctx, 75.0, i+1, nil)
	}

	result := g.PreEvolveCheck(ctx, 75.0, 5, 100, 10)

	assert.False(t, result.ShouldStop) // Warning should not stop
	require.Len(t, result.Events, 1)
	assert.Equal(t, GuardrailWarning, result.Events[0].Level)
	assert.Equal(t, "stagnation", result.Events[0].Rule)
	assert.Contains(t, result.Events[0].Message, "no improvement for")
}

func TestPostEvolveCheck_ImprovementResetsStagnation(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(70.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()

	// First call - initial score (improvement from 0)
	result := g.PostEvolveCheck(ctx, 80.0, 1, nil)
	assert.False(t, result.ShouldStop)
	assert.Equal(t, 0, g.StagnantCount())

	// Second call with same score (no improvement)
	result = g.PostEvolveCheck(ctx, 80.0, 2, nil)
	assert.False(t, result.ShouldStop)
	assert.Equal(t, 1, g.StagnantCount())

	// Third call with better score (improvement)
	result = g.PostEvolveCheck(ctx, 85.0, 3, nil)
	assert.False(t, result.ShouldStop)
	assert.Equal(t, 0, g.StagnantCount()) // Should reset to 0
}

func TestPostEvolveCheck_NoImprovementIncrementsStagnation(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(70.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()

	// Initial improvement
	g.PostEvolveCheck(ctx, 80.0, 1, nil)
	assert.Equal(t, 0, g.StagnantCount())

	// Multiple calls without improvement
	for i := 2; i <= 5; i++ {
		g.PostEvolveCheck(ctx, 79.0, i, nil)
		expectedStagnant := i - 1 // First non-improvement call sets stagnantCount=1
		assert.Equal(t, expectedStagnant, g.StagnantCount(), "generation %d", i)
	}
}

func TestPostEvolveCheck_RegressionBelowBaseline(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(85.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()

	// First call establishes best known above baseline
	g.PostEvolveCheck(ctx, 90.0, 1, nil)

	// Second call drops below baseline
	result := g.PostEvolveCheck(ctx, 80.0, 2, nil)

	assert.True(t, result.ShouldStop)
	require.Len(t, result.Events, 1)
	assert.Equal(t, GuardrailCritical, result.Events[0].Level)
	assert.Equal(t, "baseline_regression", result.Events[0].Rule)
	assert.Contains(t, result.Events[0].Message, "best score regressed below baseline")
}

func TestPostEvolveCheck_LineageConcentrationWarning(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(70.0),
		WithMaxStagnantGenerations(5),
		WithMaxLineageShare(0.5), // Set low threshold
	)

	ctx := context.Background()

	// Lineage shares with one dominant lineage (>50%)
	lineageShares := map[string]int{
		"lineage-a": 80,
		"lineage-b": 10,
		"lineage-c": 10,
	}

	result := g.PostEvolveCheck(ctx, 85.0, 1, lineageShares)

	assert.False(t, result.ShouldStop) // Warning should not stop
	require.Len(t, result.Events, 1)
	assert.Equal(t, GuardrailWarning, result.Events[0].Level)
	assert.Equal(t, "lineage_concentration", result.Events[0].Rule)
	assert.Contains(t, result.Events[0].Message, "lineage concentration")
}

func TestPostEvolveCheck_LineageWithinThreshold(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(70.0),
		WithMaxStagnantGenerations(5),
		WithMaxLineageShare(0.9), // High threshold
	)

	ctx := context.Background()

	// Lineage shares within threshold
	lineageShares := map[string]int{
		"lineage-a": 60,
		"lineage-b": 30,
		"lineage-c": 10,
	}

	result := g.PostEvolveCheck(ctx, 85.0, 1, lineageShares)

	assert.False(t, result.ShouldStop)
	assert.Empty(t, result.Events)
}

func TestEventsRecordedAndRetrievable(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(3),
	)

	ctx := context.Background()

	// Trigger an event via PreEvolveCheck
	g.PreEvolveCheck(ctx, 75.0, 1, 100, 60)

	// Trigger an event via PostEvolveCheck
	g.PostEvolveCheck(ctx, 70.0, 2, nil)

	events := g.Events()
	require.Len(t, events, 2)

	// Verify first event (unevaluated population)
	assert.Equal(t, GuardrailCritical, events[0].Level)
	assert.Equal(t, "unevaluated_population", events[0].Rule)

	// Verify second event (baseline regression)
	assert.Equal(t, GuardrailCritical, events[1].Level)
	assert.Equal(t, "baseline_regression", events[1].Rule)

	// Manually record a custom event
	customEvent := GuardrailEvent{
		Level:           GuardrailInfo,
		Rule:            "custom_rule",
		Message:         "test event",
		Generation:      3,
		Timestamp:       time.Now(),
		SuggestedAction: "none",
	}
	g.RecordEvent(customEvent)

	events = g.Events()
	require.Len(t, events, 3)
	assert.Equal(t, "custom_rule", events[2].Rule)
}

func TestReset_ClearsState(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(3),
	)

	ctx := context.Background()

	// Generate some state
	g.PreEvolveCheck(ctx, 75.0, 1, 100, 60)
	g.PostEvolveCheck(ctx, 90.0, 2, nil)
	g.PostEvolveCheck(ctx, 88.0, 3, nil)

	// Verify state exists before reset
	assert.Greater(t, g.StagnantCount(), 0)
	assert.NotEmpty(t, g.Events())

	// Reset
	g.Reset()

	// Verify cleared state
	assert.Equal(t, 0, g.StagnantCount())
	assert.Empty(t, g.Events())
}

func TestConcurrentAccessSafety(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()
	var wg sync.WaitGroup

	// Launch multiple goroutines that concurrently access guardrails
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Mix of read and write operations
			switch id % 4 {
			case 0:
				g.PreEvolveCheck(ctx, float64(id*10), id, 100, id%30)
			case 1:
				lineageShares := map[string]int{
					"a": 50 + id,
					"b": 50 - id,
				}
				g.PostEvolveCheck(ctx, float64(80+id), id+1, lineageShares)
			case 2:
				_ = g.StagnantCount()
			case 3:
				_ = g.Events()
			}
		}(i)
	}

	// Wait for all goroutines to complete with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock or race condition
		t.Log("Concurrent access test passed")
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent access test timed out - possible deadlock")
	}
}

func TestPreEvolveCheck_ZeroPopulation(t *testing.T) {
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()
	result := g.PreEvolveCheck(ctx, 75.0, 1, 0, 0)

	// Should not panic on zero population
	assert.False(t, result.ShouldStop)
	assert.Empty(t, result.Events)
}

func TestPostEvolveCheck_NilLineageShares(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(70.0),
		WithMaxStagnantGenerations(5),
		WithMaxLineageShare(0.5),
	)

	ctx := context.Background()

	// Should handle nil lineageShares gracefully
	result := g.PostEvolveCheck(ctx, 85.0, 1, nil)

	assert.False(t, result.ShouldStop)
	assert.Empty(t, result.Events)
}

func TestPostEvolveCheck_ZeroBaseline(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(0), // Zero baseline means disabled
		WithMaxStagnantGenerations(5),
	)

	ctx := context.Background()

	// Should not trigger regression check when baseline is 0
	result := g.PostEvolveCheck(ctx, -10.0, 1, nil)

	assert.False(t, result.ShouldStop)
	assert.Empty(t, result.Events)
}

func TestEvents_MaxEventsLimit(t *testing.T) {
	defer discardLogs()()
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(3),
	)

	// Set a small max events limit
	g.mu.Lock()
	g.MaxEvents = 5
	g.mu.Unlock()

	ctx := context.Background()

	// Generate more events than MaxEvents allows
	for i := 0; i < 10; i++ {
		g.PreEvolveCheck(ctx, 75.0, i+1, 100, 60+i)
	}

	events := g.Events()
	// Should have at most MaxEvents
	assert.LessOrEqual(t, len(events), 5)
}
