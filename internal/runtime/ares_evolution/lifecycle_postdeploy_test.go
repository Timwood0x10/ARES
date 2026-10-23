package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// capturingLog records what would be sealed, without touching a file.
type capturingLog struct {
	mu   sync.Mutex
	recs []ExperimentRecord
}

func (c *capturingLog) Append(_ context.Context, rec ExperimentRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, rec)
	return nil
}

func (c *capturingLog) List(context.Context) ([]ExperimentRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ExperimentRecord(nil), c.recs...), nil
}

// TestPostDeployOutcome_AbsentStaysNil is the rule the plan states explicitly: no samples means
// PostDeploy stays nil. A zero-valued outcome would claim "measured, and it did nothing" about a
// period that was never measured.
func TestPostDeployOutcome_AbsentStaysNil(t *testing.T) {
	out := postDeployOutcome(WindowResult{}, mutation.Strategy{ID: "s1"}, time.Now())
	assert.Nil(t, out)
}

// TestPostDeployOutcome_PopulatedFromMetricsChannel: latency and cost come from the metrics
// channel, which by design never contributes to the fitness mean.
func TestPostDeployOutcome_PopulatedFromMetricsChannel(t *testing.T) {
	win := WindowResult{
		Mean:  0.8,
		Count: 12,
		Ok:    true,
		Metrics: map[string]SourceStat{
			latencyMetricSource: {Mean: 120, Count: 4},
			costMetricSource:    {Mean: 0.3, Count: 4},
		},
	}

	out := postDeployOutcome(win, mutation.Strategy{ID: "s1"}, time.Now())

	require.NotNil(t, out)
	assert.Equal(t, "s1", out.StrategyID)
	assert.True(t, out.Success)
	assert.InDelta(t, 0.8, out.Score, 1e-9)
	assert.InDelta(t, 120.0, out.LatencyCost, 1e-9, "latency is copied as the normalised cost it is")
	assert.InDelta(t, 0.3, out.SpendCost, 1e-9)
	assert.Zero(t, out.LatencyMs, "milliseconds cannot be derived from a normalised cost")
}

// TestPostDeployOutcome_MetricsWithoutFitnessIsNotSuccess: metrics alone say what it cost, not
// whether it worked. Reporting Success=false would blame the strategy for a quiet system, so the
// record is written with the metrics and Success=false, and the doc says why.
func TestPostDeployOutcome_MetricsWithoutFitnessIsNotSuccess(t *testing.T) {
	win := WindowResult{
		Metrics: map[string]SourceStat{
			latencyMetricSource: {Mean: 90, Count: 2},
		},
	}

	out := postDeployOutcome(win, mutation.Strategy{ID: "s2"}, time.Now())

	require.NotNil(t, out, "a metric sample is still an observation")
	assert.False(t, out.Success, "without fitness evidence there is nothing to call a success")
	assert.InDelta(t, 90.0, out.LatencyCost, 1e-9)
}

// TestTickPostDeploy_SealsAfterTheWindow: the window closes after postDeployCycles ticks and
// seals ONE record carrying the observation, joined to the verdict by the same proposal key.
func TestTickPostDeploy_SealsAfterTheWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	store := evidence.NewMemoryStore()
	// Scoped to the strategy: the window is queried by strategy ID, so evidence written with an
	// empty scope would be invisible to it (and the test would pass for the wrong reason).
	seedScopedValue(t, store, latencyMetricSource, evidence.KindFitness, "s1", "", floatPtr(120), now.Add(-time.Second))
	seedScopedValue(t, store, costMetricSource, evidence.KindFitness, "s1", "", floatPtr(0.4), now.Add(-time.Second))

	log := &capturingLog{}
	l := &StrategyLifecycle{
		agg:           NewRuntimeFitnessAggregator(store, DefaultAggregatorConfig()),
		experimentLog: log,
		proposalKey:   func(string) string { return "proposal-1" },
	}
	l.openPostDeployWatch(&mutation.Strategy{ID: "s1", Score: 70})

	for i := 0; i < postDeployCycles-1; i++ {
		l.tickPostDeploy(ctx)
	}
	require.Empty(t, log.recs, "the window must stay open until the last tick")

	l.tickPostDeploy(ctx)

	require.Len(t, log.recs, 1)
	rec := log.recs[0]
	require.NotNil(t, rec.PostDeploy, "the closing tick must carry the observation")
	assert.Equal(t, "s1", rec.PostDeploy.StrategyID)
	assert.Equal(t, ExperimentOutcomeAccepted, rec.Outcome)
	assert.Equal(t, "post_deploy", rec.Verification[0].Gate)
	assert.Equal(t, "proposal-1", rec.ProposalKey, "the observation must join the verdict it belongs to")
	assert.Nil(t, l.postDeploy, "the window is closed, so a second observation cannot be sealed")
}

// TestTickPostDeploy_NoSamplesSealsNothing is the other half of the rule: a window with no
// evidence closes silently rather than recording a zero.
func TestTickPostDeploy_NoSamplesSealsNothing(t *testing.T) {
	ctx := context.Background()
	log := &capturingLog{}
	l := &StrategyLifecycle{
		agg:           NewRuntimeFitnessAggregator(evidence.NewMemoryStore(), DefaultAggregatorConfig()),
		experimentLog: log,
		proposalKey:   func(string) string { return "proposal-1" },
	}
	l.openPostDeployWatch(&mutation.Strategy{ID: "s1"})

	for i := 0; i < postDeployCycles; i++ {
		l.tickPostDeploy(ctx)
	}

	assert.Empty(t, log.recs, "no samples must not become a zero-valued record")
	assert.Nil(t, l.postDeploy)
}

// TestOpenPostDeployWatch_WithoutAJoinKeyOpensNothing: a window that could never be sealed
// honestly must not be opened in the first place.
func TestOpenPostDeployWatch_WithoutAJoinKeyOpensNothing(t *testing.T) {
	l := &StrategyLifecycle{experimentLog: &capturingLog{}} // no proposalKey

	l.openPostDeployWatch(&mutation.Strategy{ID: "s1"})

	assert.Nil(t, l.postDeploy)
}
