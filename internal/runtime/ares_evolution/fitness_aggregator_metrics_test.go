package evolution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// TestAggregator_MetricsDoNotMoveTheFitnessMean is the contract that made the "just add two
// sources" approach wrong (M9③): `Count` is the STAGING GATE, not a diagnostic counter, and a
// channel nobody opted into must not license a staging verdict.
//
// latency and cost are optimisation targets (Minimize). Folding them into the weighted mean
// would also make a slow-but-correct candidate score worse than a fast-and-wrong one — the
// very trade the multi-objective path exists to expose rather than hide.
func TestAggregator_MetricsDoNotMoveTheFitnessMean(t *testing.T) {
	ctx := context.Background()
	store := evidence.NewMemoryStore()
	now := time.Now()

	seedValue(t, store, "strategy", evidence.KindFitness, floatPtr(0.8), now)
	agg := NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig())

	before := agg.Window(ctx, "")
	require.Contains(t, before.PerSource, "strategy")
	assert.Empty(t, before.Metrics, "nothing metric-shaped was recorded yet")

	seedValue(t, store, latencyMetricSource, evidence.KindFitness, floatPtr(0.2), now)
	seedValue(t, store, costMetricSource, evidence.KindFitness, floatPtr(0.1), now)
	after := agg.Window(ctx, "")

	assert.InDelta(t, before.Mean, after.Mean, 1e-9,
		"metrics must not move the fitness mean: they are targets, not evidence")
	assert.Equal(t, before.Count, after.Count,
		"metrics must not advance Count: that counter gates a staging verdict")
	assert.Equal(t, before.Ok, after.Ok)

	// They are still readable, which is the point of the separate channel.
	require.Contains(t, after.Metrics, latencyMetricSource)
	require.Contains(t, after.Metrics, costMetricSource)
	assert.InDelta(t, 0.2, after.Metrics[latencyMetricSource].Mean, 1e-9)
	assert.InDelta(t, 0.1, after.Metrics[costMetricSource].Mean, 1e-9)
	assert.Equal(t, 1, after.Metrics[latencyMetricSource].Count)
	assert.NotContains(t, after.PerSource, latencyMetricSource,
		"a metric must not appear as a fitness source")
}
