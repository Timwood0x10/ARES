// adapters_update_createdat_test.go locks REVIEW 3.3#8: ToStorageExperience
// stamps CreatedAt=time.Now() (it is shared with the Create path), and
// DistillationRepo.Update forwarded that stamp verbatim, so every update
// reset the experience's age signal in backends that store the whole struct
// (the in-memory repository; the PG UPDATE happens to ignore the column).
// The adapter now preserves the stored CreatedAt.
package experienceadapters

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	experience "github.com/Timwood0x10/ares/internal/llmexp"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// createdAtRepo is a fake that stores whole rows like the in-memory backend.
type createdAtRepo struct {
	fakeExpRepo
	rows    map[string]*storage_models.Experience
	updated *storage_models.Experience
}

func (r *createdAtRepo) GetByID(ctx context.Context, tenantID, id string) (*storage_models.Experience, error) {
	if row, ok := r.rows[id]; ok {
		return row, nil
	}
	return nil, nil
}

func (r *createdAtRepo) Update(ctx context.Context, exp *storage_models.Experience) error {
	r.updated = exp
	if r.rows == nil {
		r.rows = map[string]*storage_models.Experience{}
	}
	r.rows[exp.ID] = exp
	return nil
}

func TestUpdatePreservesOriginalCreatedAt(t *testing.T) {
	original := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &createdAtRepo{rows: map[string]*storage_models.Experience{
		"exp-1": {
			ID:        "exp-1",
			TenantID:  "tenant-a",
			Type:      storage_models.ExperienceTypeSolution,
			CreatedAt: original,
		},
	}}
	adapter := NewDistillationRepo(repo, "tenant-a")

	err := adapter.Update(context.Background(), &experience.Experience{
		ID:       "exp-1",
		Problem:  "p",
		Solution: "s",
	})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)

	assert.True(t, repo.updated.CreatedAt.Equal(original),
		"update must preserve the stored CreatedAt (age signal), got %v", repo.updated.CreatedAt)
}
