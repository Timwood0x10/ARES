package genome

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestEvolveAfterScoring_FillsDimensions is why the dimension pass lives on the Population
// rather than in one caller: `EvolveAfterScoring` is the IN-PROCESS evolution loop, and the
// public service is the other one. Wiring the pass into only one of them makes "nsga2 is
// multi-objective" true on one path and a lie on the other — which is exactly the state this
// test was written to end.
func TestEvolveAfterScoring_FillsDimensions(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{
		ID: "base", Version: 1,
		Params: map[string]any{"temperature": 0.7},
		Score:  50.0, CreatedAt: testNow(),
	}
	pop, err := NewPopulation(ctx, base, &mockGenomeMutator{}, WithPopulationSize(5))
	require.NoError(t, err)

	crosser, err := NewCrossover(WithSeed(42))
	require.NoError(t, err)

	// Every candidate gets a dimension, so the assertion covers the post-evolve population
	// rather than one surviving id.
	pop.SetMultiObjectiveScorer(func(*mutation.Strategy) (map[string]float64, float64) {
		return map[string]float64{"success_rate": 0.7}, 0.7
	})

	require.NoError(t, pop.EvolveAfterScoring(ctx,
		func(*mutation.Strategy) float64 { return 0.5 }, &mockGenomeMutator{}, crosser))

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		assert.Equal(t, map[string]float64{"success_rate": 0.7}, a.DimensionScores,
			"the loop's FINAL pass must fill dimensions (id=%s)", a.ID)
		assert.Equal(t, 0.7, a.Score, "the aggregate follows the dimensions")
	}
}

// TestEvolveAfterScoring_WithoutAScorerLeavesDimensionsEmpty pins the default: the zero value
// of the population is scalar-only, so installing this machinery cannot change an existing
// deployment's selection.
func TestEvolveAfterScoring_WithoutAScorerLeavesDimensionsEmpty(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{
		ID: "base", Version: 1,
		Params: map[string]any{"temperature": 0.7},
		Score:  50.0, CreatedAt: testNow(),
	}
	pop, err := NewPopulation(ctx, base, &mockGenomeMutator{}, WithPopulationSize(5))
	require.NoError(t, err)

	crosser, err := NewCrossover(WithSeed(42))
	require.NoError(t, err)

	require.NoError(t, pop.EvolveAfterScoring(ctx,
		func(*mutation.Strategy) float64 { return 0.5 }, &mockGenomeMutator{}, crosser))

	snap, _ := pop.Snapshot()
	require.NotEmpty(t, snap)
	for _, a := range snap {
		assert.Nil(t, a.DimensionScores, "no installed scorer means no dimensions")
	}
}
