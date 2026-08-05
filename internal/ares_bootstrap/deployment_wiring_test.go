// Package ares_bootstrap — deployment staging tests (Stage 7).
//
// Verifies the shadow runtime no longer reports a constant passing score:
// Evaluate must return the real recent fitness mean (or 0.0 when no evidence
// exists) so promotion only proceeds on observed performance.
package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeploymentStaging_NoEvidence_ReturnsZero verifies that with no fitness
// evidence the shadow score is 0.0 — promotion is blocked instead of the old
// constant 1.0 pass.
func TestDeploymentStaging_NoEvidence_ReturnsZero(t *testing.T) {
	reg := patch.NewRegistry()
	r := &deploymentStagingRuntime{reg: reg, evidenceStore: evidence.NewMemoryStore()}

	score, err := r.Evaluate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.0, score,
		"shadow score must be 0.0 without evidence (no fabricated pass)")
}

// TestDeploymentStaging_NilEvidence_ReturnsZero covers the nil-store guard.
func TestDeploymentStaging_NilEvidence_ReturnsZero(t *testing.T) {
	r := &deploymentStagingRuntime{reg: patch.NewRegistry(), evidenceStore: nil}

	score, err := r.Evaluate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.0, score)
}

// TestDeploymentStaging_WithFitness_ReturnsMean verifies the shadow score
// reflects real observed workflow fitness rather than a nominal value.
func TestDeploymentStaging_WithFitness_ReturnsMean(t *testing.T) {
	store := evidence.NewMemoryStore()
	// Seed fitness evidence: two events scoring 1.0 and 0.0 → mean 0.5.
	require.NoError(t, seedFitnessEvidence(t, store, 1.0))
	require.NoError(t, seedFitnessEvidence(t, store, 0.0))

	r := &deploymentStagingRuntime{reg: patch.NewRegistry(), evidenceStore: store}
	score, err := r.Evaluate(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 0.5, score, 0.001,
		"shadow score must equal the mean of observed workflow fitness")
}

// seedFitnessEvidence writes one workflow fitness evidence with the given value.
func seedFitnessEvidence(t *testing.T, store *evidence.MemoryStore, value float64) error {
	t.Helper()
	collector := evidence.NewCollector(store, "workflow")
	return collector.Emit(context.Background(), evidence.KindFitness, map[string]any{"value": value})
}
