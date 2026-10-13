package evolution

// fitness_aggregator_dimension_test.go is the end-to-end proof for defect G1 /
// P0-1(ii): the REAL producer (the dimension judge's bridge) and the REAL
// consumer (the fitness aggregator) must agree. Before this, the bridge wrote
// Source "dimension_judge" while the accuracy query asked for the source
// "dimension_eval" (which is the KIND), so the query matched nothing, ever — the
// DimensionEval weight (0.25) contributed no samples and GA optimised on outcome
// plus cost/latency alone, with no correctness signal and no error.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// TestAggregator_ReadsDimensionAccuracyFromTheBridge seeds the store through the
// production producer and asserts the aggregator's window sees the accuracy
// signal.
func TestAggregator_ReadsDimensionAccuracyFromTheBridge(t *testing.T) {
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	bridge := eval.NewDimensionJudgeBridge(store)
	require.NoError(t, bridge.Emit(ctx, "golden-1", "eval", &eval.DimensionJudgeResponse{
		Correctness: 3, Completeness: 3, Efficiency: 2, Safety: 2, Reason: "correct and complete",
	}))

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())
	res := agg.Window(ctx, "")

	stat, ok := res.PerSource["dimension_eval"]
	require.True(t, ok,
		"the accuracy signal produced by the dimension judge must be visible to the aggregator")
	assert.Equal(t, 1, stat.Count, "the record must be counted in the window")
	assert.InDelta(t, 1.0, stat.Mean, 1e-9, "and its scalar value must be the mean the judge wrote")
}

// TestAggregator_SkipsLegacyDimensionEvidenceWithoutAScalar pins the
// compatibility boundary for records written by a build that PREDATES
// Evidence.Value: those payloads carry the old Go-named fields and no `value`
// key, and they must be SKIPPED, not folded in as 0.0 — a zero would read as
// "every judgment failed" and drag the whole source's mean down.
//
// The payload below is the exact legacy shape (encoding/json used the Go field
// names, and Evidence had no Value field), so the case is fixed against the
// thing that can actually exist rather than against a hand-written stub.
//
// Recorded residual (accepted, not hidden): the check is "the payload carries a
// numeric lowercase `value` in [0,1]", not "this record came from the bridge".
// A legacy payload that happened to carry a lowercase `value` key for an
// unrelated reason would be counted. No producer ever wrote a dimension_eval
// payload with that key before Value existed, and guarding it properly would
// mean a per-format version tag — a schema change for a case the shipped code
// cannot produce.
func TestAggregator_SkipsLegacyDimensionEvidenceWithoutAScalar(t *testing.T) {
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	legacy := `{"TaskID":"golden-1","Role":"eval","Verdict":"pass","Flag":"",` +
		`"Dimensions":[{"Name":"correctness","Score":3,"Max":3,"Pass":true}],` +
		`"Confidence":0.9,"Source":"dimension_judge",` +
		`"CreatedAt":"2026-10-13T00:00:00Z","Meta":null}`
	require.NotContains(t, legacy, `"value"`, "the fixture must be a scalar-less legacy payload")
	require.NoError(t, store.Append(ctx, evidence.Evidence{
		Kind:    evidence.KindDimensionEval,
		Source:  eval.DimensionJudgeSource,
		Payload: []byte(legacy),
	}))

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())
	res := agg.Window(ctx, "")

	_, ok := res.PerSource["dimension_eval"]
	assert.False(t, ok,
		"a legacy record without a scalar must contribute nothing rather than count as 0.0")
}
