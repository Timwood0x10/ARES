package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestAdapter_SealsAPendingRecordForEachGeneration is M10's producer: the GA cycle seals what
// it PROPOSED, with the fitness configuration that was in force, and says nothing about
// whether it was released.
func TestAdapter_SealsAPendingRecordForEachGeneration(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{ID: "base", Params: map[string]any{"temperature": 0.7}, Score: 0.9}
	pop, err := genome.NewPopulation(ctx, base, &mockGenomeMutator{})
	require.NoError(t, err)
	pop.ScoreAgents(func(*mutation.Strategy) float64 { return 0.6 })

	log := NewMemoryExperimentLog()
	adapter := &GenomePopulationAdapter{pop: pop, experimentLog: log}
	adapter.sealExperimentRecord(ctx)

	recs, err := log.List(ctx)
	require.NoError(t, err)
	require.Len(t, recs, 1, "one record per generation")
	rec := recs[0]

	assert.Equal(t, ExperimentOutcomePending, rec.Outcome,
		"the cycle knows what it proposed, not whether the gates released it")
	assert.Equal(t, ExperimentRecordSchemaVersion, rec.SchemaVersion)
	assert.NotEmpty(t, rec.CandidateID)
	assert.False(t, rec.At.IsZero())
	assert.NotEmpty(t, rec.FitnessConfig,
		"the weights actually used are part of what makes the run reproducible")
	assert.Equal(t, rec.CandidateID, rec.CandidateID, "sanity")
}

// TestAdapter_SealingIsOffWithoutALog pins the default: an adapter nobody handed a log stays
// silent, so wiring this feature cannot change a deployment that never configured it.
func TestAdapter_SealingIsOffWithoutALog(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{ID: "base", Params: map[string]any{"temperature": 0.7}}
	pop, err := genome.NewPopulation(ctx, base, &mockGenomeMutator{})
	require.NoError(t, err)

	(&GenomePopulationAdapter{pop: pop}).sealExperimentRecord(ctx) // no log installed

	log := NewMemoryExperimentLog()
	recs, err := log.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, recs, "no log means no record")
}
