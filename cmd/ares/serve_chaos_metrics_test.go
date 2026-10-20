package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/arena"
)

// TestRecoveryMetricsFromSnapshot_CarriesConsistency pins the producer→panel
// mapping for P4: the panel payload must carry the rate, the sample count, and
// the measured bit, or the collector would still be a black hole even though
// the fabric feeds it.
func TestRecoveryMetricsFromSnapshot_CarriesConsistency(t *testing.T) {
	snap := arena.MetricsSnapshot{
		TotalRecoveries:     2,
		AvgRecoveryTime:     250 * time.Millisecond,
		DataConsistencyRate: 95,
		ConsistencySamples:  20,
	}

	m := recoveryMetricsFromSnapshot(snap)
	require.NotNil(t, m)
	assert.Equal(t, 2, m.TotalRecoveries)
	assert.Equal(t, 250*time.Millisecond, m.AvgRecoveryTime)
	assert.InDelta(t, 95.0, m.DataConsistencyRate, 0.01)
	assert.Equal(t, 20, m.ConsistencySamples)
	assert.True(t, m.ConsistencyMeasured)
}

// TestRecoveryMetricsFromSnapshot_BelowThresholdIsNotMeasured keeps the
// "measured zero vs unmeasured zero" distinction at the panel boundary.
func TestRecoveryMetricsFromSnapshot_BelowThresholdIsNotMeasured(t *testing.T) {
	m := recoveryMetricsFromSnapshot(arena.MetricsSnapshot{
		DataConsistencyRate: 0, // not published below the threshold
		ConsistencySamples:  19,
	})
	assert.False(t, m.ConsistencyMeasured)
	assert.Equal(t, 19, m.ConsistencySamples)
}
