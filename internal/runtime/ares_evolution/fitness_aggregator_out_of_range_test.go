package evolution

// fitness_aggregator_out_of_range_test.go covers the observability half of the
// [0,1] contract on evidence values.
//
// The aggregator has always SKIPPED a record whose scalar falls outside [0,1] (it
// cannot use it), but the skip used to be invisible: the only symptom was "this
// source has no evidence", which reads as "nothing ran" rather than "something ran
// and wrote the wrong unit" — the same silent-void failure mode the dimension_eval
// weight already suffered once (a query keyed on a string no producer wrote, so a
// 0.25 weight contributed nothing forever). The window now carries the count, and a
// rate-limited warning names the source and the offending value.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// seedValue writes one unscoped fitness record with the given scalar. A nil value
// produces a record with NO scalar at all (the diagnosis-only shape).
func seedValue(t *testing.T, store *evidence.MemoryStore, source string, kind evidence.EvidenceKind, value *float64, at time.Time) {
	t.Helper()
	seedScopedValue(t, store, source, kind, "", "", value, at)
}

// seedScopedValue is seedValue with the payload's strategy_id / tool_step_id set,
// which is what the client-side scope filters key on.
func seedScopedValue(t *testing.T, store *evidence.MemoryStore, source string, kind evidence.EvidenceKind, strategyID, toolStepID string, value *float64, at time.Time) {
	t.Helper()
	payload := map[string]any{}
	if value != nil {
		payload["value"] = *value
	}
	if strategyID != "" {
		payload["strategy_id"] = strategyID
	}
	if toolStepID != "" {
		payload["tool_step_id"] = toolStepID
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, store.Append(context.Background(), evidence.Evidence{
		ID:        source + "-" + at.Format("150405.000000000"),
		Source:    source,
		Kind:      kind,
		Payload:   raw,
		Timestamp: at,
	}))
}

func floatPtr(v float64) *float64 { return &v }

// TestAggregator_OutOfRangeEvidenceIsCountedAndExcluded pins both halves: the mean
// is computed WITHOUT the unusable records (the existing behaviour), and the count
// of what was dropped is reported (the new one). 3600 stands for a producer writing
// milliseconds; 1.4 stands for one writing a value that is already a ratio but
// scaled wrong. Both are the same defect class.
func TestAggregator_OutOfRangeEvidenceIsCountedAndExcluded(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	now := time.Now()

	seedValue(t, store, "workflow", evidence.KindFitness, floatPtr(0.8), now.Add(-4*time.Second))
	seedValue(t, store, "workflow", evidence.KindFitness, floatPtr(3600), now.Add(-3*time.Second))
	seedValue(t, store, "workflow", evidence.KindFitness, floatPtr(0.6), now.Add(-2*time.Second))
	seedValue(t, store, "workflow", evidence.KindFitness, floatPtr(1.4), now.Add(-time.Second))

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())
	window := agg.Window(ctx, "")

	require.Contains(t, window.PerSource, "workflow")
	assert.InDelta(t, 0.7, window.PerSource["workflow"].Mean, 1e-9,
		"the mean must be computed from the usable records only")
	assert.Equal(t, 2, window.Count)
	assert.Equal(t, 2, window.OutOfRange,
		"the dropped records must be visible: silence is what let a unit error hide")
}

// TestAggregator_AbsentValueIsNotOutOfRange keeps the two cases apart. A record
// with no scalar at all is a diagnosis-only record by design — skipping it is
// correct and not worth alarming about — so it must not inflate the counter that
// exists to point at a unit error.
func TestAggregator_AbsentValueIsNotOutOfRange(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	now := time.Now()

	seedValue(t, store, "workflow", evidence.KindFitness, nil, now.Add(-time.Second))
	seedValue(t, store, "workflow", evidence.KindFitness, floatPtr(0.5), now)

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())
	window := agg.Window(ctx, "")

	assert.Equal(t, 0, window.OutOfRange, "an absent value is not a unit error")
	assert.Equal(t, 1, window.Count)
	assert.InDelta(t, 0.5, window.Mean, 1e-9)
}

// TestAggregator_DimensionChannelReportsOutOfRange covers the accuracy channel: it
// is queried by KIND rather than through the source table, so it is a separate code
// path into the same range check and could regress on its own.
func TestAggregator_DimensionChannelReportsOutOfRange(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	now := time.Now()

	seedValue(t, store, "dimension_judge", evidence.KindDimensionEval, floatPtr(0.9), now.Add(-time.Second))
	seedValue(t, store, "dimension_judge", evidence.KindDimensionEval, floatPtr(1.5), now)

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())
	window := agg.Window(ctx, "")

	assert.Equal(t, 1, window.OutOfRange, "a 0-100 dimension value is the classic unit error here")
	require.Contains(t, window.PerSource, "dimension_eval")
	assert.InDelta(t, 0.9, window.PerSource["dimension_eval"].Mean, 1e-9)
}

// TestAggregator_FullyOutOfRangeChannelIsStillReported is the shape that slipped
// through the first version: EVERY record of the channel out of range. The mean falls
// back to cold start and the audit gate stays closed (Count == 0), so the counter is
// the only thing that can tell "this channel is empty" from "this channel is broken" —
// and it must fire here, not only when some samples happened to be usable. Counting it
// inside the usable-samples branch reported 0 while the warning fired, i.e. the number
// and the log contradicted each other.
func TestAggregator_FullyOutOfRangeChannelIsStillReported(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	now := time.Now()

	seedValue(t, store, "dimension_judge", evidence.KindDimensionEval, floatPtr(90), now.Add(-2*time.Second))
	seedValue(t, store, "dimension_judge", evidence.KindDimensionEval, floatPtr(55), now.Add(-time.Second))

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())
	window := agg.Window(ctx, "")

	assert.Equal(t, 2, window.OutOfRange,
		"a channel whose records are ALL unusable is exactly the case the counter exists for")
	assert.Equal(t, 0, window.Count)
	assert.False(t, window.Ok, "no usable samples must keep the audit gate closed")
	assert.InDelta(t, DefaultAggregatorConfig().ColdStartScore, window.Mean, 1e-9)
	assert.NotContains(t, window.PerSource, "dimension_eval",
		"a channel with no usable sample must not appear in PerSource")
}

// TestAggregator_OutOfRangeCountsOnlyTheScopedPopulation is the second shape the first
// version got wrong: the range check ran BEFORE the strategy / tool-step filters, so
// WindowToolStepAt attributed another tool step's bad records to this one. The counter
// must describe the same population the mean is computed over.
func TestAggregator_OutOfRangeCountsOnlyTheScopedPopulation(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	now := time.Now()

	// This tool step: one good record. Another tool step and another strategy: the
	// bad values, which are not this window's business.
	seedScopedValue(t, store, toolCallEvidenceSource, evidence.KindFitness, "s1", "grep#limit", floatPtr(0.9), now.Add(-3*time.Second))
	seedScopedValue(t, store, toolCallEvidenceSource, evidence.KindFitness, "s1", "other-step", floatPtr(3600), now.Add(-2*time.Second))
	seedScopedValue(t, store, toolCallEvidenceSource, evidence.KindFitness, "other-strategy", "grep#limit", floatPtr(1.4), now.Add(-time.Second))

	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())

	scoped := agg.WindowToolStepAt(ctx, "s1", "grep#limit", time.Time{}, time.Time{})
	assert.Equal(t, 0, scoped.OutOfRange,
		"another population's bad records must not be charged to this tool step")
	assert.InDelta(t, 0.9, scoped.Mean, 1e-9)
	assert.Equal(t, 1, scoped.Count)

	// And the bad record that IS this tool step's own is reported as such.
	seedScopedValue(t, store, toolCallEvidenceSource, evidence.KindFitness, "s1", "grep#limit", floatPtr(7200), now)
	scoped = agg.WindowToolStepAt(ctx, "s1", "grep#limit", time.Time{}, time.Time{})
	assert.Equal(t, 1, scoped.OutOfRange)
	assert.Equal(t, 1, scoped.Count, "the unusable record is not a sample")
}

// TestWarnOutOfRangeValue_OncePerSource pins the rate limit: one warning per
// (source, kind), so a producer writing the wrong unit for a whole source cannot
// turn the recovery tick into a log sink, and a DIFFERENT source still gets its own
// warning (the second one is a separate finding, not noise).
//
// The source name is unique to this test on purpose: the limiter is process-global,
// and any other test touching the same name would make the first call return false.
func TestWarnOutOfRangeValue_OncePerSource(t *testing.T) {
	defer discardLogs()()
	// The limiter is process-wide, so without a reset this test only passes on its FIRST run in
	// a process — which is exactly how it behaved as a flaky test under -count>1.
	resetOutOfRangeWarnings()
	t.Cleanup(resetOutOfRangeWarnings)
	ctx := context.Background()
	const source = "unit-test-out-of-range-source"

	assert.True(t, warnOutOfRangeValue(ctx, source, evidence.KindFitness, 3600),
		"the first out-of-range value for a source must be reported")
	assert.False(t, warnOutOfRangeValue(ctx, source, evidence.KindFitness, 3600.5),
		"and the second must not: the source is already known to be wrong")

	assert.True(t, warnOutOfRangeValue(ctx, source+"-2", evidence.KindFitness, 1.4),
		"a different source is a different finding")
}
