// distiller_updateconfig_test.go locks REVIEW 3.3#1: UpdateConfig must
// propagate threshold changes to the pipeline components that snapshot them
// at construction time (ImportanceScorer.ShouldKeep, ConflictResolver.
// DetectConflict, NoiseFilter.IsNoise). Pre-fix, only d.config was swapped,
// so the scorer/resolver/noiseFilter kept running with the original
// construction-time values while the top-N phases read the new config.
package distillation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateConfigPropagatesToComponents(t *testing.T) {
	repo := NewMockExperienceRepository([]Experience{
		{
			ID:         "existing-1",
			Problem:    "connect database",
			Solution:   "check the connection string",
			Confidence: 0.9,
			// cos([1,0,0,0], [1,0.5,0,0]) = 1/sqrt(1.25) ≈ 0.894 — between
			// the initial (0.95) and updated (0.85) conflict bars.
			Vector: []float64{1, 0.5, 0, 0},
		},
	})

	initial := DefaultDistillationConfig()
	// Start strict: high importance bar, near-identical-vector conflict bar,
	// noise filters ON.
	initial.MinImportance = 0.95
	initial.ConflictThreshold = 0.95
	initial.EnableCodeFilter = true

	d := NewDistiller(initial, NewMockEmbeddingService(), repo)

	// Baseline: with the initial config the components use the strict values.
	assert.False(t, d.scorer.ShouldKeep(0.7), "0.7 must be below the initial 0.95 bar")
	_, err := d.resolver.DetectConflict(context.Background(), []float64{1, 0, 0, 0}, "tenant")
	assert.ErrorIs(t, err, ErrNoConflict, "similarity ~0.894 must stay below the initial 0.95 conflict bar")
	assert.True(t, d.noiseFilter.IsNoise("```go func main() {} ```"), "code block must be noise with the filter on")

	// Runtime reconfiguration: relax all three thresholds.
	updated := DefaultDistillationConfig()
	updated.MinImportance = 0.5
	updated.ConflictThreshold = 0.85
	updated.EnableCodeFilter = false
	d.UpdateConfig(updated)

	// The components must observe the NEW thresholds, not the ones captured
	// at NewDistiller time.
	assert.True(t, d.scorer.ShouldKeep(0.7), "0.7 must clear the updated 0.5 bar")
	assert.Equal(t, 0.5, d.scorer.GetMinImportance())

	conflict, err := d.resolver.DetectConflict(context.Background(), []float64{1, 0, 0, 0}, "tenant")
	require.NoError(t, err)
	require.NotNil(t, conflict, "similarity ~0.894 must trip the updated 0.85 conflict bar")
	assert.Equal(t, "existing-1", conflict.ID)

	assert.False(t, d.noiseFilter.IsNoise("```go func main() {} ```"),
		"code block must not be noise once the filter is disabled")
}

func TestUpdateConfigNilIsNoOp(t *testing.T) {
	d := NewDistiller(nil, NewMockEmbeddingService(), NewMockExperienceRepository(nil))
	before := d.scorer.GetMinImportance()
	d.UpdateConfig(nil)
	assert.Equal(t, before, d.scorer.GetMinImportance(), "nil config must leave the scorer untouched")
}
