package experienceadapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	experience "github.com/Timwood0x10/ares/internal/llmexp"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// The DEEP_CODE_REVIEW_2026 HIGH regression #11: CountByMemoryType must be
// able to report counts beyond the old ListByType(1000) plateau so
// DistillationConfig.MaxSolutionsPerTenant (default 5000) can actually
// trigger. The fix adds an in-place CountByType fast path (used by the PG
// repository) and raises the fallback listing bound above the cap.

// fakeExpRepo is a minimal ExperienceRepositoryInterface whose ListByType
// records the limit it was called with.
type fakeExpRepo struct {
	rows      []*storage_models.Experience
	listLimit int
	listCalls int
}

func (f *fakeExpRepo) Create(ctx context.Context, exp *storage_models.Experience) error {
	return nil
}
func (f *fakeExpRepo) GetByID(ctx context.Context, tenantID, id string) (*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) Update(ctx context.Context, exp *storage_models.Experience) error { return nil }
func (f *fakeExpRepo) UpdateEmbedding(ctx context.Context, tenantID, id string, embedding []float64, model string, version int) error {
	return nil
}
func (f *fakeExpRepo) Delete(ctx context.Context, id, tenantID string) error { return nil }
func (f *fakeExpRepo) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) SearchByKeyword(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) IncrementUsageCount(ctx context.Context, tenantID, id string) error {
	return nil
}
func (f *fakeExpRepo) DecrementRank(ctx context.Context, tenantID, id string) error { return nil }
func (f *fakeExpRepo) ListByType(ctx context.Context, expType, tenantID string, limit int) ([]*storage_models.Experience, error) {
	f.listCalls++
	f.listLimit = limit
	return f.rows, nil
}
func (f *fakeExpRepo) ListByAgent(ctx context.Context, agentID, tenantID string, limit int) ([]*storage_models.Experience, error) {
	return nil, nil
}

// countingExpRepo adds the optional CountByType fast path.
type countingExpRepo struct {
	fakeExpRepo
	count    int
	counted  bool
	expType  string
	tenantID string
}

func (c *countingExpRepo) CountByType(ctx context.Context, expType, tenantID string) (int, error) {
	c.counted = true
	c.expType = expType
	c.tenantID = tenantID
	return c.count, nil
}

// TestCountByMemoryTypeFastPathExceedsListLimit pins the fix: a repository
// with the CountByType fast path reports the true count (here 6000 — above
// the old 1000 plateau and above the 5000 default cap) without listing any
// rows.
func TestCountByMemoryTypeFastPathExceedsListLimit(t *testing.T) {
	repo := &countingExpRepo{count: 6000}
	adapter := NewDistillationRepo(repo, "tenant-a")

	count, err := adapter.CountByMemoryType(context.Background(), "tenant-a", experience.MemoryKnowledge)
	require.NoError(t, err)
	assert.Equal(t, 6000, count, "the in-place count must be returned verbatim")

	require.True(t, repo.counted, "the fast path must be used when available")
	assert.Equal(t, 0, repo.listCalls, "no rows may be materialized on the fast path")
	assert.Equal(t, storage_models.ExperienceTypeSuccess, repo.expType,
		"the storage type mapping must match memoryTypeToStorageType")
	assert.Equal(t, "tenant-a", repo.tenantID, "the tenant scope must be forwarded")
}

// TestCountByMemoryTypeFallbackLimitExceedsSolutionCap pins the fallback: a
// repository WITHOUT CountByType lists with a bound above the default
// MaxSolutionsPerTenant (5000), so a tenant over the cap is actually seen as
// over it (the old DefaultListLimit=1000 could never report more than 1000).
func TestCountByMemoryTypeFallbackLimitExceedsSolutionCap(t *testing.T) {
	rows := make([]*storage_models.Experience, 0, 6000)
	for i := 0; i < 6000; i++ {
		rows = append(rows, &storage_models.Experience{ID: "e", TenantID: "tenant-a", Type: storage_models.ExperienceTypeSuccess})
	}
	repo := &fakeExpRepo{rows: rows}
	adapter := NewDistillationRepo(repo, "tenant-a")

	count, err := adapter.CountByMemoryType(context.Background(), "tenant-a", experience.MemoryKnowledge)
	require.NoError(t, err)
	assert.Equal(t, 6000, count, "the fallback count must reflect the rows, not the plateau")

	require.Equal(t, 1, repo.listCalls)
	assert.Greater(t, repo.listLimit, 5000,
		"the fallback listing bound must exceed the default MaxSolutionsPerTenant so the cap can trigger")
}
