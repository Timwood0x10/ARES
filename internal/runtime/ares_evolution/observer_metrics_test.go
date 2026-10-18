package evolution

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
)

func metricRecords(t *testing.T, store evidence.Store, source string) []evidence.Evidence {
	t.Helper()
	recs, err := store.Query(context.Background(), evidence.Filter{Source: source})
	require.NoError(t, err)
	return recs
}

// payloadOf decodes a record payload (the stores keep it as JSON bytes).
func payloadOf(t *testing.T, rec evidence.Evidence) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Payload, &out))
	return out
}

// TestObserver_WritesLatencyAndCostMetrics is M9③'s collection half: the two optimisation
// targets must reach the evidence store as their OWN records, because the fitness record's
// scalar is the fitness value and the aggregator reads exactly one value per record.
func TestObserver_WritesLatencyAndCostMetrics(t *testing.T) {
	store := evidence.NewMemoryStore()
	obs := &RuntimeObserver{evStore: store}

	obs.writeEvidence(context.Background(), StrategySample{
		StrategyID:  "strategy-1",
		Score:       0.9,
		Completed:   true,
		LatencyCost: 0.25,
		SpendCost:   0.5,
		At:          time.Now(),
	})

	latency := metricRecords(t, store, latencyMetricSource)
	require.Len(t, latency, 1)
	assert.InDelta(t, 0.25, payloadOf(t, latency[0])["value"], 1e-9,
		"the metric is a COST (0 = best), matching the Minimize direction")
	assert.Equal(t, "strategy-1", payloadOf(t, latency[0])[evidenceKeyStrategyID])

	spend := metricRecords(t, store, costMetricSource)
	require.Len(t, spend, 1)
	assert.InDelta(t, 0.5, payloadOf(t, spend[0])["value"], 1e-9)

	// The fitness record is still the only thing the fitness channel sees.
	assert.Len(t, metricRecords(t, store, observerEvidenceSource), 1)
}

// TestObserver_SkipsMetricsForNonCompletedTasks: a task with no terminal completed event has
// no comparable wall time or spend. Writing 0 there would read as "instant and free" — the
// best possible value on a Minimize objective — and would reward exactly the tasks that
// produced nothing.
func TestObserver_SkipsMetricsForNonCompletedTasks(t *testing.T) {
	store := evidence.NewMemoryStore()
	obs := &RuntimeObserver{evStore: store}

	obs.writeEvidence(context.Background(), StrategySample{
		StrategyID: "strategy-1",
		Score:      0,
		Completed:  false,
		At:         time.Now(),
	})

	assert.Empty(t, metricRecords(t, store, latencyMetricSource))
	assert.Empty(t, metricRecords(t, store, costMetricSource))
}
