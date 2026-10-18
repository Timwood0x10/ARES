package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aresevolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// newScoringPopulation builds a population that the dimension pass can read.
func newScoringPopulation(t *testing.T) *genome.Population {
	t.Helper()
	base := &mutation.Strategy{ID: "base", Params: map[string]any{"t": 0.7}, Score: 0.5}
	pop, err := genome.NewPopulation(context.Background(), base, &mockGenomeMutator{})
	require.NoError(t, err)
	return pop
}

// scoreThrough runs the service's installation step and then the population's combined entry
// point — the same two calls the real scoring path makes.
func scoreThrough(svc *Service, pop *genome.Population, scalar float64) {
	svc.installDimensionScorer(pop)
	pop.ScoreAgentsWithDimensions(func(*mutation.Strategy) float64 { return scalar })
}

// TestInstallDimensionScorer_FillsFromExecutionEvidence is M9's wiring: a candidate with real
// execution evidence gets DimensionScores, which is what stops NSGA-II from degenerating to
// single-objective selection.
func TestInstallDimensionScorer_FillsFromExecutionEvidence(t *testing.T) {
	pop := newScoringPopulation(t)
	svc := &Service{config: &SystemConfig{
		DimensionWindow: func(id string) aresevolution.WindowResult {
			if id != "base" {
				return aresevolution.WindowResult{}
			}
			return aresevolution.WindowResult{PerSource: map[string]aresevolution.SourceStat{
				"strategy":       {Mean: 0.8, Count: 4},
				"dimension_eval": {Mean: 0.6, Count: 2},
			}}
		},
	}}

	scoreThrough(svc, pop, 0.5)

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	seen := false
	for _, a := range snap {
		if a.ID != "base" {
			continue
		}
		seen = true
		assert.Equal(t, map[string]float64{"success_rate": 0.8, "quality": 0.6}, a.DimensionScores)
		assert.InDelta(t, genome.AggregateDimensions(a.DimensionScores, genome.DefaultDimensionWeights),
			a.Score, 1e-9, "the aggregate must come from the same dimensions")
	}
	assert.True(t, seen, "the fixture must contain the base strategy")
}

// TestInstallDimensionScorer_LeavesUnevidencedCandidatesAlone: filling dimensions must not
// change the score of a candidate with no evidence. Reporting 0 there would be a measurement
// it never earned, and it would drop it out of selection.
func TestInstallDimensionScorer_LeavesUnevidencedCandidatesAlone(t *testing.T) {
	pop := newScoringPopulation(t)
	svc := &Service{config: &SystemConfig{
		DimensionWindow: func(string) aresevolution.WindowResult { return aresevolution.WindowResult{} },
	}}

	scoreThrough(svc, pop, 0.42)

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		assert.Nil(t, a.DimensionScores, "no evidence means no dimensions, not zero dimensions")
		assert.Equal(t, 0.42, a.Score, "the scalar score must survive untouched")
	}
}

// TestInstallDimensionScorer_IsOffWithoutAWindow pins the default: with no window source the
// previous behaviour is what happens, so installing this cannot change an existing deployment.
func TestInstallDimensionScorer_IsOffWithoutAWindow(t *testing.T) {
	pop := newScoringPopulation(t)

	scoreThrough(&Service{config: &SystemConfig{}}, pop, 0.33)

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		assert.Nil(t, a.DimensionScores)
		assert.Equal(t, 0.33, a.Score)
	}
}
