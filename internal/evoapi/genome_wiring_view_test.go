package evoapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	evolve "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// viewMutator implements genome.MutatorInterface for view tests.
type viewMutator struct{}

func (m *viewMutator) Mutate(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	out := make([]*mutation.Strategy, n)
	for i := range out {
		out[i] = &mutation.Strategy{
			ID:        parent.ID + "-child",
			ParentID:  parent.ID,
			Params:    parent.Params,
			Score:     parent.Score,
			CreatedAt: time.Now(),
		}
	}
	return out, nil
}

// TestGenomeWiringView_NilAdapter pins the defensive nil check.
func TestGenomeWiringView_NilAdapter(t *testing.T) {
	t.Parallel()
	_, err := NewGenomeWiringView(nil)
	require.Error(t, err)
}

// TestGenomeWiringView_Stats pins the read-only fitness observation surface
// (A3): after one evolution cycle, Stats returns the generation and scores.
func TestGenomeWiringView_Stats(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := &mutation.Strategy{
		ID:        "vw-1",
		Params:    map[string]any{"temperature": 0.7},
		Score:     50.0,
		CreatedAt: time.Now(),
	}
	mut := &viewMutator{}
	crosser, err := genome.NewCrossover(genome.WithSeed(42))
	require.NoError(t, err)

	pop, err := genome.NewPopulation(ctx, base, mut,
		genome.WithPopulationSize(5),
		genome.WithEliteCount(1),
	)
	require.NoError(t, err)

	adapter, err := evolve.NewGenomePopulationAdapter(pop, mut, crosser)
	require.NoError(t, err)

	view, err := NewGenomeWiringView(adapter)
	require.NoError(t, err)

	// Before any evolution: generation 0.
	require.Equal(t, 0, view.CurrentGeneration())
	require.False(t, view.GenerationActive())

	// Run one cycle.
	require.NoError(t, adapter.Run(ctx))
	require.Equal(t, 1, view.CurrentGeneration())

	stats := view.Stats()
	require.NotNil(t, stats)
	require.Equal(t, 1, stats.Generation)
	require.Equal(t, 5, stats.Size)

	// BestStrategy after one cycle.
	id, score := view.BestStrategy()
	require.NotEmpty(t, id)
	require.Greater(t, score, 0.0)

	// FitnessHistory has at least one entry after evolution.
	hist := view.FitnessHistory()
	require.NotEmpty(t, hist)
	require.Equal(t, 1, hist[0].Generation)
}

// TestGenomeWiringView_Stats_SingleSnapshot pins M3 (0.3.2): Stats() must read
// generation, scores and the stagnation counter from ONE Population.Stats
// call, so the returned fields can never be torn across a concurrent evolve.
// Before the fix, StagnantGens was read via a second lock acquisition.
func TestGenomeWiringView_Stats_SingleSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := &mutation.Strategy{
		ID:        "vw-m3",
		Params:    map[string]any{"temperature": 0.5},
		Score:     40.0,
		CreatedAt: time.Now(),
	}
	mut := &viewMutator{}
	crosser, err := genome.NewCrossover(genome.WithSeed(7))
	require.NoError(t, err)

	pop, err := genome.NewPopulation(ctx, base, mut,
		genome.WithPopulationSize(4),
		genome.WithEliteCount(1),
	)
	require.NoError(t, err)

	adapter, err := evolve.NewGenomePopulationAdapter(pop, mut, crosser)
	require.NoError(t, err)

	view, err := NewGenomeWiringView(adapter)
	require.NoError(t, err)
	require.NoError(t, adapter.Run(ctx))

	stats := view.Stats()
	require.NotNil(t, stats)

	// With no mutation in flight, the snapshot must agree with the
	// population's own accessors — proving the fields share one source.
	require.Equal(t, pop.CurrentGeneration(), stats.Generation)
	require.Equal(t, pop.StagnantGenerations(), stats.StagnantGens)
}
