package eval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// TestDimensionEvidenceCarriesScalarValue pins the payload contract the fitness
// aggregator reads (defect G1 / P0-1): a dimension_judge record whose payload has
// `"value"` in [0,1]. The record used to carry the whole structured diagnosis and
// no scalar, so the accuracy query — which unmarshals {"value": ...} — could read
// nothing even once the producer was wired.
func TestDimensionEvidenceCarriesScalarValue(t *testing.T) {
	store := evidence.NewMemoryStore()
	bridge := NewDimensionJudgeBridge(store)

	require.NoError(t, bridge.Emit(context.Background(), "tc-1", "eval", &DimensionJudgeResponse{
		Correctness: 3, Completeness: 3, Efficiency: 2, Safety: 2, Reason: "all four at the ceiling",
	}, 1.0))

	recs, err := store.Query(context.Background(), evidence.Filter{Kind: evidence.KindDimensionEval})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, DimensionJudgeSource, recs[0].Source,
		"Source names the verifier that produced the record, not the kind")

	var payload struct {
		Value float64 `json:"value"`
	}
	require.NoError(t, json.Unmarshal(recs[0].Payload, &payload))
	assert.InDelta(t, 1.0, payload.Value, 1e-9, "a ceiling judgment must read as 1.0")
}

// TestDimensionMetricAndEvidenceValueAgree locks the "one rule, two readers"
// invariant: the score handed to the eval gate and the scalar written for the
// fitness aggregator are the same number. Two copies of that arithmetic would
// drift, and a drifted accuracy signal is silently wrong rather than visibly
// broken.
func TestDimensionMetricAndEvidenceValueAgree(t *testing.T) {
	store := evidence.NewMemoryStore()
	client := &mockLLMClient{response: `{"correctness": 2, "completeness": 1, "efficiency": 1, "safety": 2, "reason": "partial"}`}
	judge, err := NewLLMJudgeEvaluator(client, WithDimensionAveraging(), WithEvidenceStore(store))
	require.NoError(t, err)

	scores, err := judge.Evaluate(context.Background(),
		TestCase{ID: "tc-1", Input: "q", ExpectedOutput: "a"},
		TestResult{TestCaseID: "tc-1", ActualOutput: "a"})
	require.NoError(t, err)
	require.Len(t, scores, 1)
	assert.Equal(t, "llm_judge_dimension_avg", scores[0].Metric)

	recs, err := store.Query(context.Background(), evidence.Filter{Kind: evidence.KindDimensionEval})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	var payload struct {
		Value float64 `json:"value"`
	}
	require.NoError(t, json.Unmarshal(recs[0].Payload, &payload))
	assert.InDelta(t, scores[0].Score, payload.Value, 1e-9,
		"the gate's score and the fitness scalar must come from one rule")
}
