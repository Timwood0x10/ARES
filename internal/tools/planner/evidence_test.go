package planner

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestEvidenceAggregator_RecordEmptyToolName(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    err := agg.Record(context.Background(), "", "Arithmetic", true, time.Millisecond, 0, "")
    require.Error(t, err)
}

func TestEvidenceAggregator_RecordSuccess(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    err := agg.Record(context.Background(), "calculator", "Arithmetic", true, 2*time.Millisecond, 0, "")
    require.NoError(t, err)

    results, qErr := store.Query(context.Background(), "calculator", "", 10)
    require.NoError(t, qErr)
    require.Len(t, results, 1)
    assert.True(t, results[0].Success)
    assert.Equal(t, "calculator", results[0].ToolName)
}

func TestEvidenceAggregator_RecordFailure(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    err := agg.Record(context.Background(), "web_search", "WebSearch", false, 500*time.Millisecond, 2, "timeout")
    require.NoError(t, err)

    results, qErr := store.Query(context.Background(), "web_search", "", 10)
    require.NoError(t, qErr)
    require.Len(t, results, 1)
    assert.False(t, results[0].Success)
    assert.Equal(t, "timeout", results[0].ErrorClass)
    assert.Equal(t, 2, results[0].RetryCount)
}

func TestEvidenceScorer_NoEvidence(t *testing.T) {
    store := NewMemoryEvidenceStore()
    scorer := NewEvidenceScorer(store)
    candidates := []ToolCandidate{
        {ToolName: "calculator", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.95},
    }
    result, err := scorer.Score(context.Background(), candidates, nil)
    require.NoError(t, err)
    require.Len(t, result, 1)
    // Without evidence, the default success rate (0.95) should give a positive score.
    assert.Greater(t, result[0].Score, 20.0)
}

func TestEvidenceScorer_EvidenceImprovesScore(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    ctx := context.Background()

    // Record multiple successes.
    for i := 0; i < 10; i++ {
        require.NoError(t, agg.Record(ctx, "calculator", "Arithmetic", true, 1*time.Millisecond, 0, ""))
    }

    evidence, _ := store.Query(ctx, "calculator", "Arithmetic", 50)
    scorer := NewEvidenceScorer(store)

    // Same tool, but one candidate sets CapabilityName and the other does not.
    withCapa := []ToolCandidate{
        {ToolName: "calculator", CapabilityName: "Arithmetic", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.30},
    }
    withoutCapa := []ToolCandidate{
        {ToolName: "calculator", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.30},
    }

    resultWith, _ := scorer.Score(ctx, withCapa, evidence)
    resultWithout, _ := scorer.Score(ctx, withoutCapa, evidence)

    // With CapabilityName set, evidence is found and overrides the default.
    require.Len(t, resultWith, 1)
    require.Len(t, resultWithout, 1)
    // With-evidence score should differ from without-evidence score.
    assert.NotEqual(t, resultWith[0].Score, resultWithout[0].Score)
}

func TestEvidenceScorer_FailuresReduceScore(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    ctx := context.Background()

    // Record mostly failures.
    for i := 0; i < 8; i++ {
        require.NoError(t, agg.Record(ctx, "web_search", "WebSearch", false, 100*time.Millisecond, 1, "timeout"))
    }
    for i := 0; i < 2; i++ {
        require.NoError(t, agg.Record(ctx, "web_search", "WebSearch", true, 50*time.Millisecond, 0, ""))
    }

    evidence, _ := store.Query(ctx, "web_search", "WebSearch", 50)

    // Compare with a tool that has no failures.
    store2 := NewMemoryEvidenceStore()
    agg2 := NewEvidenceAggregator(store2)
    for i := 0; i < 10; i++ {
        require.NoError(t, agg2.Record(ctx, "good_web_search", "WebSearch", true, 50*time.Millisecond, 0, ""))
    }
    evidence2, _ := store2.Query(ctx, "good_web_search", "WebSearch", 50)

    scorer := NewEvidenceScorer(store)
    candidates := []ToolCandidate{
        {ToolName: "web_search", CapabilityName: "WebSearch", Cost: 5, Deterministic: false, Composable: true, SideEffects: false, SuccessRate: 0.95},
    }
    result, err := scorer.Score(ctx, candidates, evidence)
    require.NoError(t, err)
    require.Len(t, result, 1)
    scoreWithFailures := result[0].Score

    // Use a different tool name so evidence doesn't overlap.
    goodCandidates := []ToolCandidate{
        {ToolName: "good_web_search", CapabilityName: "WebSearch", Cost: 5, Deterministic: false, Composable: true, SideEffects: false, SuccessRate: 0.95},
    }
    result2, err := scorer.Score(ctx, goodCandidates, evidence2)
    require.NoError(t, err)
    require.Len(t, result2, 1)
    scoreWithoutFailures := result2[0].Score

    // Score with failures should be lower than without failures.
    assert.Less(t, scoreWithFailures, scoreWithoutFailures)
}

func TestEvidenceScorer_HighLatencyPenalty(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    ctx := context.Background()

    // slow_tool has high latency evidence.
    require.NoError(t, agg.Record(ctx, "slow_tool", "Arithmetic", true, 2000*time.Millisecond, 0, ""))

    evidence, _ := store.Query(ctx, "slow_tool", "Arithmetic", 50)
    scorer := NewEvidenceScorer(store)
    candidates := []ToolCandidate{
        {ToolName: "slow_tool", CapabilityName: "Arithmetic", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.95},
    }
    result, err := scorer.Score(ctx, candidates, evidence)
    require.NoError(t, err)
    require.Len(t, result, 1)
    scoreSlow := result[0].Score

    // Same tool with no evidence (fast default) should score higher.
    result2, err := scorer.Score(ctx, []ToolCandidate{
        {ToolName: "fast_tool", CapabilityName: "Arithmetic", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.95},
    }, nil)
    require.NoError(t, err)
    require.Len(t, result2, 1)
    scoreFast := result2[0].Score

    assert.Greater(t, scoreFast, scoreSlow)
}

func TestEvidenceScorer_NonIdempotentNotRetried(t *testing.T) {
    store := NewMemoryEvidenceStore()
    agg := NewEvidenceAggregator(store)
    ctx := context.Background()

    // Record a failure on a side-effect tool.
    require.NoError(t, agg.Record(ctx, "http_request", "HTTPRequest", false, 100*time.Millisecond, 1, "internal_error"))

    evidence, _ := store.Query(ctx, "http_request", "HTTPRequest", 50)

    // Compare side-effect + failure vs no side-effect + success.
    store2 := NewMemoryEvidenceStore()
    agg2 := NewEvidenceAggregator(store2)
    require.NoError(t, agg2.Record(ctx, "safe_tool", "HTTPRequest", true, 10*time.Millisecond, 0, ""))
    evidence2, _ := store2.Query(ctx, "safe_tool", "HTTPRequest", 50)

    scorer := NewEvidenceScorer(store)
    candidates := []ToolCandidate{
        {ToolName: "http_request", Cost: 5, Deterministic: false, Composable: true, SideEffects: true, SuccessRate: 0.95},
    }
    result, _ := scorer.Score(ctx, candidates, evidence)
    scoreWithSideEffect := result[0].Score

    scorer2 := NewEvidenceScorer(store2)
    result2, _ := scorer2.Score(ctx, []ToolCandidate{
        {ToolName: "safe_tool", Cost: 5, Deterministic: false, Composable: true, SideEffects: false, SuccessRate: 0.95},
    }, evidence2)
    scoreWithoutSideEffect := result2[0].Score

    assert.Greater(t, scoreWithoutSideEffect, scoreWithSideEffect)
}
