// Package ares_bootstrap — deployment staging tests (Stage 7).
//
// Verifies the shadow runtime no longer reports a constant passing score:
// Evaluate must return the real recent fitness mean (or 0.0 when no evidence
// exists) so promotion only proceeds on observed performance.
package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	aresmemory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// newStagingRuntime builds a deploymentStagingRuntime over the given evidence
// store with the zero-value cold-start score (0.0 — the pre-B6 behavior the
// original tests pinned). Production construction (bootstrap.go) wires the
// shared aggregator plus an explicit 0.5 cold-start score.
func newStagingRuntime(store evidence.Store, reg *patch.Registry) *deploymentStagingRuntime {
	return &deploymentStagingRuntime{
		reg: reg,
		agg: evolution.NewRuntimeFitnessAggregator(store, evolution.DefaultAggregatorConfig()),
	}
}

// TestDeploymentStaging_NoEvidence_ReturnsZero verifies that with no fitness
// evidence the shadow score is 0.0 — promotion is blocked instead of the old
// constant 1.0 pass.
func TestDeploymentStaging_NoEvidence_ReturnsZero(t *testing.T) {
	reg := patch.NewRegistry()
	r := newStagingRuntime(evidence.NewMemoryStore(), reg)

	score, err := r.Evaluate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.0, score,
		"shadow score must be 0.0 without evidence (no fabricated pass)")
}

// TestDeploymentStaging_NilEvidence_ReturnsZero covers the nil-store guard:
// the aggregator is wired but has no store, Window reports count 0, and the
// runtime falls back to the (zero) cold-start score.
func TestDeploymentStaging_NilEvidence_ReturnsZero(t *testing.T) {
	r := newStagingRuntime(nil, patch.NewRegistry())

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

	r := newStagingRuntime(store, patch.NewRegistry())
	score, err := r.Evaluate(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 0.5, score, 0.001,
		"shadow score must equal the mean of observed workflow fitness")
}

// TestDeploymentStaging_ExplicitColdStartScore pins the B6 contract: when the
// construction site sets a cold-start score (bootstrap uses 0.5), a store
// with zero evidence returns that score instead of the universal 0.0 reject.
func TestDeploymentStaging_ExplicitColdStartScore(t *testing.T) {
	r := &deploymentStagingRuntime{
		reg:            patch.NewRegistry(),
		agg:            evolution.NewRuntimeFitnessAggregator(evidence.NewMemoryStore(), evolution.DefaultAggregatorConfig()),
		coldStartScore: 0.5,
	}
	score, err := r.Evaluate(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 0.5, score, 0.001,
		"cold-start patches must receive the configured fallback score")
}

// seedFitnessEvidence writes one workflow fitness evidence with the given value.
func seedFitnessEvidence(t *testing.T, store *evidence.MemoryStore, value float64) error {
	t.Helper()
	collector := evidence.NewCollector(store, "workflow")
	return collector.Emit(context.Background(), evidence.KindFitness, map[string]any{"value": value})
}

// TestDeploymentStaging_DoesNotMutateLiveRegistry pins the staging-isolation
// contract: a shadow Apply must not change the state the live registry's
// executors point at. Regression: staging previously called reg.Apply on the
// SAME registry the live runtime uses, so REJECTED patches had already
// mutated live memory config — and ID-bearing patches poisoned the shared
// idempotency map so the later promotion silently no-op'd.
func TestDeploymentStaging_DoesNotMutateLiveRegistry(t *testing.T) {
	ctx := context.Background()

	// The shared registry holds a REAL executor writing to a real config store.
	memStore := buildMemoryManager()
	reg := patch.NewRegistry()
	require.NoError(t, reg.RegisterComponent(aresmemory.NewMemoryPatchExecutor(memStore)))
	require.True(t, reg.CanApply("memory"), "memory patch component must be registered")

	r := newStagingRuntime(evidence.NewMemoryStore(), reg)

	p := patch.RuntimePatch{
		Type:   patch.PatchChangePlanner,
		Target: "memory",
		Value:  map[string]any{"max_history": 99},
		Reason: "test: must never reach live state from staging",
	}
	_, err := r.Apply(ctx, p)
	require.NoError(t, err)

	cfg := memStore.GetConfig()
	require.NotNil(t, cfg)
	assert.NotEqual(t, 99, cfg.MaxHistory,
		"staging apply must NOT mutate live memory config")
	assert.Equal(t, 1, r.applyCount, "staging bookkeeping records the shadow apply")

	// Rollback is a no-op (nothing was applied) and must not error.
	require.NoError(t, r.Rollback(ctx, &p))

	// A target with no registered executor is rejected by the preflight,
	// preserving the old "staging apply failed" rejection class.
	orphan := newStagingRuntime(evidence.NewMemoryStore(), patch.NewRegistry())
	_, err = orphan.Apply(ctx, patch.RuntimePatch{Type: patch.PatchChangePlanner, Target: "nope"})
	require.Error(t, err)
}
