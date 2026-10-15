package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// scriptedEvalClient is a judge LLM returning a fixed rubric JSON, so the
// production wiring can be tested without an LLM.
type scriptedEvalClient struct{ response string }

func (c *scriptedEvalClient) Generate(context.Context, string) (string, error) {
	return c.response, nil
}

// TestSetupEvaluatorsRegistersTheReportingJudge is a WIRING guard on the judgement
// readout: the decorator that logs how much of the accuracy signal deterministic
// checks anchor is worthless if the registry still hands out the bare judge, because
// nothing else would ever read the counters (P0-1(ii)). It asserts the registered
// evaluator IS the reporter, and that it still scores.
func TestSetupEvaluatorsRegistersTheReportingJudge(t *testing.T) {
	registry, err := setupEvaluators(&scriptedEvalClient{
		response: `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "ok"}`,
	}, nil)
	require.NoError(t, err)

	registered, ok := registry.Get("llm_judge")
	require.True(t, ok, "the judge must stay registered under its name")
	if _, reporting := registered.(*eval.AgreementReportingEvaluator); !reporting {
		t.Fatalf("registered judge = %T, want the agreement-reporting decorator: the readout would never run", registered)
	}

	scores, err := registered.Evaluate(context.Background(), eval.TestCase{ID: "tc-1", Input: "q"}, eval.TestResult{ActualOutput: "a"})
	require.NoError(t, err)
	require.Len(t, scores, 1)
	assert.Equal(t, "llm_judge_dimension_avg", scores[0].Metric)
}

// TestSetupEvaluatorsPersistsDimensionEvidence is the production-wiring half of
// P0-1(ii): the registry the bootstrap hands to the eval gate must score through
// the dimension path AND leave dimension_eval evidence behind when a store is
// wired — that evidence is the only correctness signal the runtime path produces
// for GA fitness (defect G1). Before this wiring the judge scored, persisted
// nothing, and the fitness aggregator's DimensionEval weight had nothing to weigh.
func TestSetupEvaluatorsPersistsDimensionEvidence(t *testing.T) {
	store := evidence.NewMemoryStore()
	registry, err := setupEvaluators(&scriptedEvalClient{
		response: `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "good"}`,
	}, store)
	require.NoError(t, err)

	judge, ok := registry.Get("llm_judge")
	require.True(t, ok, "the eval gate looks the judge up by name")

	scores, err := judge.Evaluate(context.Background(),
		eval.TestCase{ID: "golden-1", Input: "q", ExpectedOutput: "a"},
		eval.TestResult{TestCaseID: "golden-1", ActualOutput: "a"})
	require.NoError(t, err)
	require.Len(t, scores, 1)
	assert.Equal(t, "llm_judge_dimension_avg", scores[0].Metric,
		"the gate must score through the dimension path (still 0..1, same scale)")
	assert.InDelta(t, 1.0, scores[0].Score, 1e-9)

	recs, err := store.Query(context.Background(), evidence.Filter{Kind: evidence.KindDimensionEval})
	require.NoError(t, err)
	assert.Len(t, recs, 1, "the production judge must persist the accuracy evidence")
}

// TestSetupEvaluatorsWithoutStoreStillScores pins the optional-dependency
// posture: without a store nothing is persisted and the score is unchanged, so a
// deployment that has no evidence store does not lose its eval gate.
func TestSetupEvaluatorsWithoutStoreStillScores(t *testing.T) {
	registry, err := setupEvaluators(&scriptedEvalClient{
		response: `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2}`,
	}, nil)
	require.NoError(t, err)
	judge, ok := registry.Get("llm_judge")
	require.True(t, ok)

	scores, err := judge.Evaluate(context.Background(), eval.TestCase{ID: "c"}, eval.TestResult{TestCaseID: "c"})
	require.NoError(t, err)
	require.Len(t, scores, 1)
	assert.InDelta(t, 1.0, scores[0].Score, 1e-9)
}
