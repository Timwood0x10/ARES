package evolution

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
)

// TestDimensionMapping_CandidateScopedOnly is M9's boundary test: a dimension may only come
// from a source that actually varies per candidate.
//
// The global channels (workflow/scheduler/recovery) are excluded because the aggregator makes
// them ignore the strategy id — every candidate receives the same value, so as dimensions they
// are constants that add width without changing dominance or crowding distance. The opt-in
// channels (collaboration/tool_call) are excluded because their default weight is zero, so most
// deployments have no data for them.
func TestDimensionMapping_CandidateScopedOnly(t *testing.T) {
	window := WindowResult{
		PerSource: map[string]SourceStat{
			"strategy":       {Mean: 0.8, Count: 5},
			"dimension_eval": {Mean: 0.6, Count: 3},
			// Present in the window, but global: must NOT become dimensions.
			"workflow":  {Mean: 0.9, Count: 4},
			"scheduler": {Mean: 0.7, Count: 4},
			"recovery":  {Mean: 0.5, Count: 2},
			// Opt-in channels with data must still not appear (not in the mapping).
			"collaboration": {Mean: 0.4, Count: 1},
			"tool_call":     {Mean: 0.3, Count: 1},
		},
	}

	dims := MapCandidateDimensions(window)
	require.Len(t, dims, 2, "only the per-candidate sources may become dimensions: %v", dims)
	assert.Equal(t, 0.8, dims["success_rate"])
	assert.Equal(t, 0.6, dims["quality"])
	for _, forbidden := range []string{"workflow", "scheduler", "recovery", "collaboration", "tool_call", "cost", "latency"} {
		assert.NotContains(t, dims, forbidden, "%q must not be a dimension", forbidden)
	}
}

// TestDimensionMapping_DirectionAndNormalization pins the half that would be silently wrong:
// the aggregator's value is already normalised to [0,1] with "higher is better", and both
// mapped dimensions are Maximize. Passing the mean through is therefore correct; inverting it,
// or scaling it, would apply a rule the evidence does not have.
func TestDimensionMapping_DirectionAndNormalization(t *testing.T) {
	window := WindowResult{
		PerSource: map[string]SourceStat{
			"strategy":       {Mean: 0.42, Count: 9},
			"dimension_eval": {Mean: 1.0, Count: 1},
		},
	}
	dims := MapCandidateDimensions(window)

	for dim, value := range dims {
		require.Contains(t, genome.DefaultDimensionDirections, dim,
			"every emitted dimension must have a declared direction")
		assert.Equal(t, genome.Maximize, genome.DefaultDimensionDirections[dim],
			"%q is maximised; the aggregator already reports higher-is-better", dim)
		assert.GreaterOrEqual(t, value, 0.0)
		assert.LessOrEqual(t, value, 1.0)
	}
	assert.Equal(t, 0.42, dims["success_rate"], "the mean is passed through, not rescaled")
	assert.Equal(t, 1.0, dims["quality"])
}

// TestDimensionMapping_OmitsWhatItCannotMeasure: a source with no samples must be ABSENT, not
// zero. Zero is a measurement ("everything failed") and would rank a cold-start candidate as if
// it had been tried and failed.
func TestDimensionMapping_OmitsWhatItCannotMeasure(t *testing.T) {
	dims := MapCandidateDimensions(WindowResult{
		PerSource: map[string]SourceStat{
			"strategy": {Mean: 0.5, Count: 4},
			// dimension_eval absent entirely.
		},
	})
	assert.Equal(t, map[string]float64{"success_rate": 0.5}, dims)

	// A present source with zero samples is the same fact as an absent one.
	dims = MapCandidateDimensions(WindowResult{
		PerSource: map[string]SourceStat{"strategy": {Mean: 0.0, Count: 0}},
	})
	assert.Empty(t, dims, "a source with no samples is a gap, not a zero score")
}

// TestMultiObjectiveScorerFromWindow_NoEvidenceMeansNoDimensions: the scorer must hand back an
// empty vector so the GA can tell "nothing measured" apart from "measured badly".
func TestMultiObjectiveScorerFromWindow_NoEvidenceMeansNoDimensions(t *testing.T) {
	scorer := MultiObjectiveScorerFromWindow(func(string) WindowResult { return WindowResult{} })
	dims, aggregate := scorer("strategy-1")
	assert.Empty(t, dims)
	assert.Zero(t, aggregate)

	withEvidence := MultiObjectiveScorerFromWindow(func(id string) WindowResult {
		assert.Equal(t, "strategy-1", id, "the window must be fetched for the candidate being scored")
		return WindowResult{PerSource: map[string]SourceStat{"strategy": {Mean: 0.75, Count: 2}}}
	})
	dims, _ = withEvidence("strategy-1")
	assert.Equal(t, map[string]float64{"success_rate": 0.75}, dims)
}
