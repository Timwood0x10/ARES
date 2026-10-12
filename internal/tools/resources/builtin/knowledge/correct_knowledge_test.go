package builtin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/Timwood0x10/ares/internal/knowledge"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

type mockKnowledgeRepo struct {
	mock.Mock
}

func (m *mockKnowledgeRepo) GetByID(ctx context.Context, namespace, id string) (*storage_models.KnowledgeChunk, error) {
	args := m.Called(ctx, namespace, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage_models.KnowledgeChunk), args.Error(1)
}

func (m *mockKnowledgeRepo) Update(ctx context.Context, chunk *storage_models.KnowledgeChunk) error {
	args := m.Called(ctx, chunk)
	return args.Error(0)
}

var _ repositories.KnowledgeRepositoryInterface = (*mockKnowledgeRepo)(nil)

func TestCorrectKnowledge_New(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	ck := NewCorrectKnowledge(repo)
	assert.NotNil(t, ck)
	assert.Equal(t, "correct_knowledge", ck.Name())
}

func TestCorrectKnowledge_Execute_MissingChunkID(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"corrected_content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestCorrectKnowledge_Execute_MissingContent(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"chunk_id": "1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestCorrectKnowledge_Execute_GetByIDError(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	repo.On("GetByID", mock.Anything, testDefaultNamespace, "1").Return(nil, errors.New("db error"))
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"chunk_id":          "1",
		"corrected_content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	repo.AssertExpectations(t)
}

func TestCorrectKnowledge_Execute_ChunkNotFound(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	repo.On("GetByID", mock.Anything, testDefaultNamespace, "1").Return(nil, nil)
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"chunk_id":          "1",
		"corrected_content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	repo.AssertExpectations(t)
}

func TestCorrectKnowledge_Execute_Success(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	now := time.Now()
	chunk := &storage_models.KnowledgeChunk{
		ID:        "1",
		Content:   "old content",
		UpdatedAt: now,
		Metadata:  map[string]interface{}{},
	}
	repo.On("GetByID", mock.Anything, testDefaultNamespace, "1").Return(chunk, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(c *storage_models.KnowledgeChunk) bool {
		return c.Content == "corrected content"
	})).Return(nil)
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"chunk_id":          "1",
		"corrected_content": "corrected content",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	repo.AssertExpectations(t)
}

func TestCorrectKnowledge_Execute_UpdateError(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	chunk := &storage_models.KnowledgeChunk{
		ID:      "1",
		Content: "old content",
	}
	repo.On("GetByID", mock.Anything, testDefaultNamespace, "1").Return(chunk, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(errors.New("update failed"))
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"chunk_id":          "1",
		"corrected_content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	repo.AssertExpectations(t)
}

func TestCorrectKnowledge_Execute_MetadataNil(t *testing.T) {
	repo := &mockKnowledgeRepo{}
	chunk := &storage_models.KnowledgeChunk{
		ID:      "1",
		Content: "old content",
	}
	repo.On("GetByID", mock.Anything, testDefaultNamespace, "1").Return(chunk, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(c *storage_models.KnowledgeChunk) bool {
		return c.Metadata != nil && c.Metadata["correction"] == true
	})).Return(nil)
	ck := NewCorrectKnowledge(repo)
	result, err := ck.Execute(context.Background(), map[string]interface{}{
		"chunk_id":          "1",
		"corrected_content": "new content",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	repo.AssertExpectations(t)
}

// TestCorrectKnowledge_Execute_ScopeFromContext is the #63 regression for this
// tool: tenant_id used to be a REQUIRED model-supplied parameter, so a
// hallucinated (or hostile) value could read and rewrite another tenant's —
// or, under M5, another user's — knowledge. The scope now comes from the
// request context, and the parameter is ignored.
func TestCorrectKnowledge_Execute_ScopeFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "user scoped context resolves the user namespace",
			ctx:  userctx.With(tenantctx.With(context.Background(), "tenant-1"), "user-1"),
			want: knowledge.UserNamespace("tenant-1", "user-1"),
		},
		{
			name: "tenant only resolves the tenant namespace",
			ctx:  tenantctx.With(context.Background(), "tenant-1"),
			want: "tenant-1",
		},
		{
			name: "unscoped falls back to the default namespace",
			ctx:  context.Background(),
			want: testDefaultNamespace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockKnowledgeRepo{}
			// Stop after the lookup: the scope is what this test pins.
			repo.On("GetByID", mock.Anything, tt.want, "1").Return(nil, errors.New("scope asserted"))
			ck := NewCorrectKnowledge(repo)

			result, err := ck.Execute(tt.ctx, map[string]interface{}{
				"chunk_id":          "1",
				"corrected_content": "new content",
				// Must be ignored: it never reaches the repository.
				"tenant_id": "attacker-tenant",
			})
			assert.NoError(t, err)
			assert.False(t, result.Success)
			repo.AssertExpectations(t)
		})
	}
}
