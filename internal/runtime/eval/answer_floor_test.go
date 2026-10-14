package eval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// TestDimensionFloor_OverrulesAGenerousJudge is the deterministic half of P0-1:
// the runtime already KNOWS the answer was a gap body or was depth-capped
// (defect P0-3), so no judge — however generous, however taken in by well-written
// gap prose — may lift that. The clamps are the same numbers the fitness observer
// applies (ares_events.AnswerQualityPenalty).
//
// The control cases are as important as the floored ones: a supplied answer and an
// executor that reports no provenance must keep the judge's score, or the floor
// would have traded reward hacking for a blanket penalty.
func TestDimensionFloor_OverrulesAGenerousJudge(t *testing.T) {
	// A judge that loves the text: all four dimensions at their ceiling.
	const generous = `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "reads well"}`

	tests := []struct {
		name   string
		result TestResult
		want   float64
	}{
		{
			name:   "gap body scores zero however well it reads",
			result: TestResult{TestCaseID: "c", AnswerBodySource: ares_events.AnswerBodySourceGap},
			want:   0,
		},
		{
			name:   "depth-capped answer is capped at the truncated tier",
			result: TestResult{TestCaseID: "c", Truncated: true},
			want:   0.5,
		},
		{
			name:   "a supplied answer keeps the judge's score",
			result: TestResult{TestCaseID: "c", AnswerBodySource: ares_events.AnswerBodySourceSupplied},
			want:   1.0,
		},
		{
			name:   "no provenance reported is no penalty (an LLM-only executor)",
			result: TestResult{TestCaseID: "c"},
			want:   1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: generous}, WithDimensionAveraging())
			require.NoError(t, err)

			scores, err := judge.Evaluate(context.Background(), TestCase{ID: "c", Input: "q"}, tt.result)
			require.NoError(t, err)
			require.Len(t, scores, 1)
			assert.InDelta(t, tt.want, scores[0].Score, 1e-9)
		})
	}
}

// TestDimensionFloor_ZeroesTheCorrectnessDimension: the floor is not only on the
// mean. The structured diagnosis — and therefore the evidence written for
// fitness — must agree that a gap body carries no correct content: a record with
// value 0 but correctness 3 would contradict itself, and the evidence is what a
// human reads when they ask "why did this score badly".
func TestDimensionFloor_ZeroesTheCorrectnessDimension(t *testing.T) {
	store := evidence.NewMemoryStore()
	judge, err := NewLLMJudgeEvaluator(
		&mockLLMClient{response: `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2}`},
		WithDimensionAveraging(), WithEvidenceStore(store))
	require.NoError(t, err)

	_, err = judge.Evaluate(context.Background(), TestCase{ID: "c", Input: "q"},
		TestResult{TestCaseID: "c", AnswerBodySource: ares_events.AnswerBodySourceGap})
	require.NoError(t, err)

	recs, err := store.Query(context.Background(), evidence.Filter{Kind: evidence.KindDimensionEval})
	require.NoError(t, err)
	require.Len(t, recs, 1)

	var payload struct {
		Value      float64 `json:"value"`
		Dimensions []struct {
			Name  string  `json:"Name"`
			Score float64 `json:"Score"`
		} `json:"Dimensions"`
	}
	require.NoError(t, json.Unmarshal(recs[0].Payload, &payload))
	assert.Zero(t, payload.Value, "the floored mean is what fitness must read")

	found := false
	for _, d := range payload.Dimensions {
		if d.Name == "correctness" {
			found = true
			assert.Zero(t, d.Score, "a gap body must carry no correctness")
		}
	}
	assert.True(t, found, "the correctness dimension must be present in the diagnosis")
}
