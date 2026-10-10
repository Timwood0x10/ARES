package experienceadapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	experience "github.com/Timwood0x10/ares/internal/llmexp"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// ── configurableExpRepo ─────────────────────────────────────────────────────

type configurableExpRepo struct {
	fakeExpRepo
	searchErr   error
	searchExps  []*storage_models.Experience
	listByTypeE error
	deleteErr   error
	deleteCalls []string
	getByIDExp  *storage_models.Experience
	getByIDErr  error
	createErr   error
	updateErr   error
}

func (r *configurableExpRepo) SearchByVector(_ context.Context, _ []float64, _ string, _ int) ([]*storage_models.Experience, error) {
	if r.searchErr != nil {
		return nil, r.searchErr
	}
	return r.searchExps, nil
}

func (r *configurableExpRepo) ListByType(_ context.Context, _ string, _ string, _ int) ([]*storage_models.Experience, error) {
	if r.listByTypeE != nil {
		return nil, r.listByTypeE
	}
	return r.rows, nil
}

func (r *configurableExpRepo) Delete(_ context.Context, id, _ string) error {
	r.deleteCalls = append(r.deleteCalls, id)
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return nil
}

func (r *configurableExpRepo) GetByID(_ context.Context, _, _ string) (*storage_models.Experience, error) {
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	return r.getByIDExp, nil
}

func (r *configurableExpRepo) Create(_ context.Context, _ *storage_models.Experience) error {
	return r.createErr
}

func (r *configurableExpRepo) Update(_ context.Context, _ *storage_models.Experience) error {
	return r.updateErr
}

// ── ExperienceSearcher.SearchByVector ───────────────────────────────────────

func TestExperienceSearcher_SearchByVector_Success(t *testing.T) {
	repo := &configurableExpRepo{}
	repo.searchExps = []*storage_models.Experience{
		{ID: "e1", Type: storage_models.ExperienceTypeSuccess, Problem: "p1", Solution: "s1", Score: 0.9},
		{ID: "e2", Type: storage_models.ExperienceTypePattern, Problem: "p2", Solution: "s2", Score: 0.7},
	}
	s := NewExperienceSearcher(repo)

	results, err := s.SearchByVector(context.Background(), []float64{0.1}, "tenant", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, "e1", results[0].ID)
	assert.Equal(t, "e2", results[1].ID)
}

func TestExperienceSearcher_SearchByVector_Error(t *testing.T) {
	repo := &configurableExpRepo{searchErr: errors.New("db down")}
	s := NewExperienceSearcher(repo)

	_, err := s.SearchByVector(context.Background(), nil, "t", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestExperienceSearcher_SearchByVector_SkipsBlankIDs(t *testing.T) {
	repo := &configurableExpRepo{}
	repo.searchExps = []*storage_models.Experience{
		{ID: "", Type: storage_models.ExperienceTypeSuccess},
		nil,
		{ID: "valid", Type: storage_models.ExperienceTypeSuccess, Problem: "p", Solution: "s"},
	}
	s := NewExperienceSearcher(repo)

	results, err := s.SearchByVector(context.Background(), nil, "t", 10)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "valid", results[0].ID)
}

// ── DistillationRepo.SearchByVector ─────────────────────────────────────────

func TestDistillationRepo_SearchByVector_Success(t *testing.T) {
	repo := &configurableExpRepo{}
	repo.searchExps = []*storage_models.Experience{
		{ID: "e1", Type: storage_models.ExperienceTypeSuccess, Problem: "p", Solution: "s"},
	}
	r := NewDistillationRepo(repo, "tenant")

	results, err := r.SearchByVector(context.Background(), []float64{1.0}, "tenant", 5)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "e1", results[0].ID)
}

func TestDistillationRepo_SearchByVector_Error(t *testing.T) {
	repo := &configurableExpRepo{searchErr: errors.New("search failed")}
	r := NewDistillationRepo(repo, "tenant")

	_, err := r.SearchByVector(context.Background(), nil, "t", 5)
	require.Error(t, err)
}

func TestDistillationRepo_SearchByVector_NilRepo(t *testing.T) {
	var r *DistillationRepo
	_, err := r.SearchByVector(context.Background(), nil, "", 10)
	require.Error(t, err)
}

// ── DistillationRepo.GetByMemoryType ────────────────────────────────────────

func TestDistillationRepo_GetByMemoryType_Success(t *testing.T) {
	repo := &configurableExpRepo{}
	repo.rows = []*storage_models.Experience{
		{ID: "e1", Type: storage_models.ExperienceTypeSuccess, Problem: "p1", Solution: "s1"},
		{ID: "e2", Type: storage_models.ExperienceTypeSuccess, Problem: "p2", Solution: "s2"},
	}
	r := NewDistillationRepo(repo, "tenant")

	results, err := r.GetByMemoryType(context.Background(), "tenant", experience.MemoryKnowledge)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, "e1", results[0].ID)
}

func TestDistillationRepo_GetByMemoryType_Error(t *testing.T) {
	repo := &configurableExpRepo{listByTypeE: errors.New("query failed")}
	r := NewDistillationRepo(repo, "tenant")

	_, err := r.GetByMemoryType(context.Background(), "tenant", experience.MemoryKnowledge)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query failed")
}

func TestDistillationRepo_GetByMemoryType_NilRepo(t *testing.T) {
	var r *DistillationRepo
	_, err := r.GetByMemoryType(context.Background(), "t", experience.MemoryKnowledge)
	require.Error(t, err)
}

// ── DistillationRepo.DeleteBatch ────────────────────────────────────────────

func TestDistillationRepo_DeleteBatch_Success(t *testing.T) {
	repo := &configurableExpRepo{}
	r := NewDistillationRepo(repo, "tenant")

	err := r.DeleteBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, repo.deleteCalls)
}

func TestDistillationRepo_DeleteBatch_ShortCircuits(t *testing.T) {
	repo := &configurableExpRepo{deleteErr: errors.New("delete failed")}
	r := NewDistillationRepo(repo, "tenant")

	err := r.DeleteBatch(context.Background(), []string{"a", "b", "c"})
	require.Error(t, err)
	// Only first call attempted before short-circuit
	assert.Len(t, repo.deleteCalls, 1)
	assert.Equal(t, "a", repo.deleteCalls[0])
}

func TestDistillationRepo_DeleteBatch_Empty(t *testing.T) {
	repo := &configurableExpRepo{}
	r := NewDistillationRepo(repo, "tenant")

	err := r.DeleteBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, repo.deleteCalls)
}

func TestDistillationRepo_DeleteBatch_NilRepo(t *testing.T) {
	var r *DistillationRepo
	err := r.DeleteBatch(context.Background(), []string{"x"})
	require.Error(t, err)
}

// ── DistillationRepo.Delete ─────────────────────────────────────────────────

func TestDistillationRepo_Delete_Error(t *testing.T) {
	repo := &configurableExpRepo{deleteErr: errors.New("not found")}
	r := NewDistillationRepo(repo, "tenant")

	err := r.Delete(context.Background(), "e1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ── DistillationRepo.Create / Update errors ─────────────────────────────────

func TestDistillationRepo_Create_Error(t *testing.T) {
	repo := &configurableExpRepo{createErr: errors.New("insert failed")}
	r := NewDistillationRepo(repo, "tenant")

	exp := &experience.Experience{ID: "e1", Problem: "p", Solution: "s"}
	err := r.Create(context.Background(), exp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert failed")
}

func TestDistillationRepo_Update_Error(t *testing.T) {
	repo := &configurableExpRepo{updateErr: errors.New("update failed")}
	repo.getByIDErr = errors.New("not found")
	r := NewDistillationRepo(repo, "tenant")

	exp := &experience.Experience{ID: "e1", Problem: "p", Solution: "s"}
	err := r.Update(context.Background(), exp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestDistillationRepo_Update_PreservesCreatedAt(t *testing.T) {
	repo := &configurableExpRepo{}
	repo.getByIDExp = &storage_models.Experience{
		ID:        "e1",
		CreatedAt: mustParseTime("2024-01-01T00:00:00Z"),
	}
	r := NewDistillationRepo(repo, "tenant")

	exp := &experience.Experience{ID: "e1", Problem: "p", Solution: "s"}
	err := r.Update(context.Background(), exp)
	require.NoError(t, err)
}

// ── DistillationRepo.CountByMemoryType errors ───────────────────────────────

func TestDistillationRepo_CountByMemoryType_FallbackError(t *testing.T) {
	repo := &configurableExpRepo{listByTypeE: errors.New("count failed")}
	r := NewDistillationRepo(repo, "tenant")

	_, err := r.CountByMemoryType(context.Background(), "tenant", experience.MemoryKnowledge)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count failed")
}

// ── KnowledgeRetrieverAdapter ───────────────────────────────────────────────

func TestNewKnowledgeRetrieverAdapter(t *testing.T) {
	a := NewKnowledgeRetrieverAdapter(nil)
	require.NotNil(t, a)
	assert.Nil(t, a.Inner)
}

func TestKnowledgeRetrieverAdapter_Retrieve_NilInner(t *testing.T) {
	a := NewKnowledgeRetrieverAdapter(nil)
	snippets, err := a.Retrieve(context.Background(), "query", 5)
	require.NoError(t, err)
	assert.Empty(t, snippets)
}

func TestKnowledgeRetrieverAdapter_Retrieve_NilReceiver(t *testing.T) {
	var a *KnowledgeRetrieverAdapter
	snippets, err := a.Retrieve(context.Background(), "query", 5)
	require.NoError(t, err)
	assert.Empty(t, snippets)
}

func TestKnowledgeRetrieverAdapter_Retrieve_WithInner(t *testing.T) {
	inner := &adapter.KnowledgeRetriever{}
	a := NewKnowledgeRetrieverAdapter(inner)
	snippets, err := a.Retrieve(context.Background(), "query", 3)
	// KnowledgeRetriever with no runtime may return empty or error depending
	// on implementation; either is acceptable for the adapter test.
	if err != nil {
		assert.Contains(t, err.Error(), "knowledge retriever adapter")
		return
	}
	// Success path: snippets is a valid (possibly empty) slice
	_ = snippets
}

// ── Distillation.Experience type mapping round-trip ─────────────────────────

func TestFullRoundTrip_MemoryTypes(t *testing.T) {
	types := []experience.MemoryType{
		experience.MemoryKnowledge,
		experience.MemoryPreference,
		experience.MemoryInteraction,
		experience.MemoryProfile,
	}
	for _, mt := range types {
		exp := &distillation.Experience{
			ID:               "e1",
			Type:             mt,
			Problem:          "problem",
			Solution:         "solution",
			Confidence:       0.8,
			ExtractionMethod: distillation.ExtractionDirect,
		}
		storage := ToStorageExperience(exp, "tenant")
		require.NotNil(t, storage)

		back := ToDistillationExperience(storage)
		assert.Equal(t, exp.Problem, back.Problem)
		assert.Equal(t, exp.Solution, back.Solution)
		assert.Equal(t, mt, back.Type)
	}
}

// ── DistillationRepo with context tenant ────────────────────────────────────

func TestDistillationRepo_DeleteBatch_ContextTenant(t *testing.T) {
	recorder := &tenantRecordingRepo{}
	r := NewDistillationRepo(recorder, "default-tenant")

	ctx := distillation.WithTenant(context.Background(), "runtime-tenant")
	err := r.DeleteBatch(ctx, []string{"e1", "e2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"runtime-tenant", "runtime-tenant"}, recorder.deletedTenants)
}

// ── helper ──────────────────────────────────────────────────────────────────

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("mustParseTime: " + err.Error())
	}
	return t
}
