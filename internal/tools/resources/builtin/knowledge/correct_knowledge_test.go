package builtin

import (
	"context"
	"errors"
	"testing"
	"time"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockKnowledgeRepo struct {
	mock.Mock
}

func (m *mockKnowledgeRepo) GetByID(ctx context.Context, id string) (*storage_models.KnowledgeChunk, error) {
	args := m.Called(ctx, id)
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
	repo.On("GetByID", mock.Anything, "1").Return(nil, errors.New("db error"))
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
	repo.On("GetByID", mock.Anything, "1").Return(nil, nil)
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
	repo.On("GetByID", mock.Anything, "1").Return(chunk, nil)
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
	repo.On("GetByID", mock.Anything, "1").Return(chunk, nil)
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
	repo.On("GetByID", mock.Anything, "1").Return(chunk, nil)
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
