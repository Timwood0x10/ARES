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

// newScoringPopulation builds a population whose members all carry the same scalar score,
// which is what the scalar pass leaves behind before dimensions are filled.
func newScoringPopulation(t *testing.T, scalar float64) *genome.Population {
	t.Helper()
	base := &mutation.Strategy{ID: "base", Params: map[string]any{"t": 0.7}, Score: scalar}
	pop, err := genome.NewPopulation(context.Background(), base, &mockGenomeMutator{})
	require.NoError(t, err)
	pop.ScoreAgents(func(*mutation.Strategy) float64 { return scalar })
	return pop
}

// TestScoreDimensions_FillsFromExecutionEvidence is M9's wiring: a candidate with real
// execution evidence gets DimensionScores, which is what stops NSGA-II from degenerating to
// single-objective selection.
func TestScoreDimensions_FillsFromExecutionEvidence(t *testing.T) {
	pop := newScoringPopulation(t, 0.5)

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
	svc.scoreDimensions(pop)

	dims := genome.DefaultDimensionWeights
	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		if a.ID != "base" {
			continue
		}
		assert.Equal(t, map[string]float64{"success_rate": 0.8, "quality": 0.6}, a.DimensionScores)
		assert.InDelta(t, genome.AggregateDimensions(a.DimensionScores, dims), a.Score, 1e-9,
			"the aggregate must be derived from the same dimensions, not a second source")
	}
}

// TestScoreDimensions_LeavesUnevidencedCandidatesAlone: filling dimensions must not change
// the score of a candidate that has no evidence. Reporting 0 there would be a measurement it
// never earned, and it would drop it out of selection entirely.
func TestScoreDimensions_LeavesUnevidencedCandidatesAlone(t *testing.T) {
	pop := newScoringPopulation(t, 0.42)

	svc := &Service{config: &SystemConfig{
		DimensionWindow: func(string) aresevolution.WindowResult { return aresevolution.WindowResult{} },
	}}
	svc.scoreDimensions(pop)

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		assert.Nil(t, a.DimensionScores, "no evidence means no dimensions, not zero dimensions")
		assert.Equal(t, 0.42, a.Score, "the scalar score must survive untouched")
	}
}

// TestScoreDimensions_IsOffWithoutAWindow pins the default: the previous behaviour is what
// happens when nobody configures a window source, so wiring this in cannot change an existing
// deployment's selection.
func TestScoreDimensions_IsOffWithoutAWindow(t *testing.T) {
	pop := newScoringPopulation(t, 0.33)

	(&Service{config: &SystemConfig{}}).scoreDimensions(pop)

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		assert.Nil(t, a.DimensionScores)
		assert.Equal(t, 0.33, a.Score)
	}
}
