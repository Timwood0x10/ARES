// Package repositories unit tests for the in-memory experience repository.
package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/errors"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// TestMemoryExperienceRepository_GetByID_NotFoundMatchesPGContract locks the
// not-found contract: the memory implementation returns errors.ErrRecordNotFound
// — the same sentinel the PG repository returns on sql.ErrNoRows — instead of
// the previous (nil, nil), so a caller cannot silently misread "not found" as
// "no error, empty result" when switching stores.
func TestMemoryExperienceRepository_GetByID_NotFoundMatchesPGContract(t *testing.T) {
	repo := NewMemoryExperienceRepository()
	ctx := context.Background()

	// A tenant mismatch is also "not found" (tenant-scoped lookup).
	_, err := repo.GetByID(ctx, "tenant-1", "missing-id")
	require.ErrorIs(t, err, errors.ErrRecordNotFound)

	// Hit path: created row, matching tenant.
	created := &storage_models.Experience{
		ID:       "exp-1",
		TenantID: "tenant-1",
		Type:     storage_models.ExperienceTypeQuery,
		Input:    "in",
		Output:   "out",
	}
	require.NoError(t, repo.Create(ctx, created))
	got, err := repo.GetByID(ctx, "tenant-1", created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)

	// Same id under a DIFFERENT tenant stays not-found (tenant isolation).
	_, err = repo.GetByID(ctx, "tenant-2", created.ID)
	require.ErrorIs(t, err, errors.ErrRecordNotFound)
}

// TestMemoryExperienceRepository_Update_TenantMismatchRejected locks REVIEW
// 2.6#35: Update must verify the stored row's TenantID before replacing it —
// a tenant passing its own TenantID on a foreign id previously overwrote the
// other tenant's experience (cross-tenant corruption).
func TestMemoryExperienceRepository_Update_TenantMismatchRejected(t *testing.T) {
	repo := NewMemoryExperienceRepository()
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &storage_models.Experience{
		ID: "exp-1", TenantID: "tenant-a", Input: "a-input",
	}))

	// tenant-b claims the same row id with its own tenant scope: must be
	// rejected, not silently overwrite.
	err := repo.Update(ctx, &storage_models.Experience{
		ID: "exp-1", TenantID: "tenant-b", Input: "hijacked",
	})
	require.Error(t, err, "cross-tenant Update must fail")

	got, err := repo.GetByID(ctx, "tenant-a", "exp-1")
	require.NoError(t, err)
	assert.Equal(t, "a-input", got.Input, "the foreign update must not have overwritten the row")
}

// TestMemoryExperienceRepository_UpdateEmbeddingDoesNotShareCallerSlice locks
// REVIEW 2.6#36: UpdateEmbedding must copy the caller's vector before
// storing it. Embedding buffers are routinely reused by the embedding
// client, and storing the caller's backing array let later buffer reuse
// race with readers holding previously returned copies.
func TestMemoryExperienceRepository_UpdateEmbeddingDoesNotShareCallerSlice(t *testing.T) {
	repo := NewMemoryExperienceRepository()
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &storage_models.Experience{
		ID: "exp-1", TenantID: "tenant-a", Input: "in",
	}))

	buf := []float64{1, 2, 3}
	require.NoError(t, repo.UpdateEmbedding(ctx, "tenant-a", "exp-1", buf, "e5", 1))

	// Caller mutates the buffer afterwards (e.g. a pooled embedding buffer).
	for i := range buf {
		buf[i] = 999
	}

	got, err := repo.GetByID(ctx, "tenant-a", "exp-1")
	require.NoError(t, err)
	require.Len(t, got.Embedding, 3)
	assert.Equal(t, []float64{1, 2, 3}, got.Embedding,
		"the stored embedding must be a private copy, not the caller's buffer")
}

// TestMemoryExperienceRepository_ListPathsReturnDeepCopies locks the other
// half of REVIEW 2.6#36: SearchByKeyword / ListByType / ListByAgent must
// return deep copies. The old shallow copies shared the Embedding slice and
// Metadata map with the stored rows, so mutating a returned experience raced
// with the store's internal state.
func TestMemoryExperienceRepository_ListPathsReturnDeepCopies(t *testing.T) {
	repo := NewMemoryExperienceRepository()
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &storage_models.Experience{
		ID:        "exp-1",
		TenantID:  "tenant-a",
		Problem:   "fix login bug",
		Type:      storage_models.ExperienceTypeQuery,
		Embedding: []float64{1, 2},
		Metadata:  map[string]interface{}{"k": "v"},
	}))

	// All three list/search paths hand out copies.
	byKeyword, err := repo.SearchByKeyword(ctx, "login", "tenant-a", 10)
	require.NoError(t, err)
	require.Len(t, byKeyword, 1)
	byKeyword[0].Embedding[0] = 999
	byKeyword[0].Metadata["k"] = "mutated"

	byType, err := repo.ListByType(ctx, "", "tenant-a", 10)
	require.NoError(t, err)
	require.Len(t, byType, 1)
	byType[0].Embedding[1] = 999

	byAgent, err := repo.ListByAgent(ctx, "", "tenant-a", 10)
	require.NoError(t, err)
	require.Len(t, byAgent, 1)
	byAgent[0].Metadata["k"] = "mutated-again"

	final, err := repo.GetByID(ctx, "tenant-a", "exp-1")
	require.NoError(t, err)
	assert.Equal(t, []float64{1, 2}, final.Embedding,
		"mutations through returned copies must not corrupt the stored embedding")
	assert.Equal(t, "v", final.Metadata["k"],
		"mutations through returned copies must not corrupt the stored metadata")
}

// TestMemoryExperienceRepository_DecrementRankMatchesPGSemantics locks the
// REVIEW 3.7 parity fix: the memory DecrementRank applies the same 10%
// penalty with a floor of 0 as the PG repository
// (score = GREATEST(score - score*0.1, 0)). The previous absolute
// exp.Score-- made the two stores diverge wildly (a -1.0 step is negligible
// on a 0-100 scale and catastrophic on a 0-1 scale).
func TestMemoryExperienceRepository_DecrementRankMatchesPGSemantics(t *testing.T) {
	repo := NewMemoryExperienceRepository()
	ctx := context.Background()

	cases := []struct {
		name  string
		score float64
		want  float64
	}{
		{name: "mid scale", score: 10.0, want: 9.0},
		{name: "unit scale", score: 1.0, want: 0.9},
		{name: "fractional", score: 0.05, want: 0.045},
		{name: "zero stays zero", score: 0, want: 0},
		{name: "negative floors to zero", score: -1.0, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := &storage_models.Experience{
				ID:       "exp-" + tc.name,
				TenantID: "tenant-1",
				Score:    tc.score,
			}
			require.NoError(t, repo.Create(ctx, exp))

			require.NoError(t, repo.DecrementRank(ctx, "tenant-1", exp.ID))
			got, err := repo.GetByID(ctx, "tenant-1", exp.ID)
			require.NoError(t, err)
			assert.InDelta(t, tc.want, got.Score, 1e-9)
		})
	}
}
