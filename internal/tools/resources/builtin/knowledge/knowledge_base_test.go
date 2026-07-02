package builtin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockSearcher struct {
	mock.Mock
}

func (m *mockSearcher) Search(ctx context.Context, tenantID, query string) ([]*RetrievalResult, error) {
	args := m.Called(ctx, tenantID, query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*RetrievalResult), args.Error(1)
}

type mockKnowledgeService struct {
	mock.Mock
}

func (m *mockKnowledgeService) GetKnowledge(ctx context.Context, tenantID, itemID string) (*KnowledgeItem, error) {
	args := m.Called(ctx, tenantID, itemID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*KnowledgeItem), args.Error(1)
}

func (m *mockKnowledgeService) UpdateKnowledge(ctx context.Context, tenantID string, item *KnowledgeItem) (*KnowledgeItem, error) {
	args := m.Called(ctx, tenantID, item)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*KnowledgeItem), args.Error(1)
}

func (m *mockKnowledgeService) AddKnowledge(ctx context.Context, item *KnowledgeItem) (*KnowledgeItem, error) {
	args := m.Called(ctx, item)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*KnowledgeItem), args.Error(1)
}

func (m *mockKnowledgeService) DeleteKnowledge(ctx context.Context, tenantID, itemID string) error {
	args := m.Called(ctx, tenantID, itemID)
	return args.Error(0)
}

func TestKnowledgeSearch_New(t *testing.T) {
	s := &mockSearcher{}
	ks := NewKnowledgeSearch(s)
	assert.NotNil(t, ks)
	assert.Equal(t, "knowledge_search", ks.Name())
}

func TestKnowledgeSearch_Execute_MissingTenantID(t *testing.T) {
	s := &mockSearcher{}
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"query": "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeSearch_Execute_MissingQuery(t *testing.T) {
	s := &mockSearcher{}
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeSearch_Execute_SearcherError(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, "t1", "test").Return(nil, errors.New("search failed"))
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"query":     "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	s.AssertExpectations(t)
}

func TestKnowledgeSearch_Execute_Success(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, "t1", "test").Return([]*RetrievalResult{
		{ID: "1", Score: 0.95, Content: "result content", Source: "src", Metadata: map[string]interface{}{"key": "val"}},
	}, nil)
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"query":     "test",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 1, data["total"])
	assert.Equal(t, "test", data["query"])
	s.AssertExpectations(t)
}

func TestKnowledgeSearch_Execute_EmptyResults(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, "t1", "test").Return([]*RetrievalResult{}, nil)
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"query":     "test",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 0, data["total"])
}

func TestKnowledgeUpdate_New(t *testing.T) {
	svc := &mockKnowledgeService{}
	ku := NewKnowledgeUpdate(svc)
	assert.NotNil(t, ku)
	assert.Equal(t, "knowledge_update", ku.Name())
}

func TestKnowledgeUpdate_Execute_MissingTenantID(t *testing.T) {
	svc := &mockKnowledgeService{}
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeUpdate_Execute_MissingItemID(t *testing.T) {
	svc := &mockKnowledgeService{}
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"content":   "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeUpdate_Execute_MissingContent(t *testing.T) {
	svc := &mockKnowledgeService{}
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeUpdate_Execute_GetError(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("GetKnowledge", mock.Anything, "t1", "1").Return(nil, errors.New("not found"))
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
		"content":   "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_Success(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("GetKnowledge", mock.Anything, "t1", "1").Return(&KnowledgeItem{
		ID: "1", TenantID: "t1", Content: "old", CreatedAt: now, UpdatedAt: now,
	}, nil)
	svc.On("UpdateKnowledge", mock.Anything, "t1", mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.Content == "updated content" && item.Source == "new-source"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "updated content", UpdatedAt: now,
	}, nil)
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
		"content":   "updated content",
		"source":    "new-source",
		"reason":    "correction",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_WithTags(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("GetKnowledge", mock.Anything, "t1", "1").Return(&KnowledgeItem{
		ID: "1", TenantID: "t1", Content: "old", CreatedAt: now, UpdatedAt: now,
	}, nil)
	svc.On("UpdateKnowledge", mock.Anything, "t1", mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.Content == "updated" && len(item.Tags) == 2 && item.Tags[0] == "tag1"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "updated", UpdatedAt: now,
	}, nil)
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
		"content":   "updated",
		"tags":      []interface{}{"tag1", "tag2"},
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_UpdateError(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("GetKnowledge", mock.Anything, "t1", "1").Return(&KnowledgeItem{
		ID: "1", TenantID: "t1", Content: "old", CreatedAt: now, UpdatedAt: now,
	}, nil)
	svc.On("UpdateKnowledge", mock.Anything, "t1", mock.Anything).Return(nil, errors.New("db error"))
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
		"content":   "updated",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeAdd_New(t *testing.T) {
	svc := &mockKnowledgeService{}
	ka := NewKnowledgeAdd(svc)
	assert.NotNil(t, ka)
	assert.Equal(t, "knowledge_add", ka.Name())
}

func TestKnowledgeAdd_Execute_MissingTenantID(t *testing.T) {
	svc := &mockKnowledgeService{}
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"content": "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeAdd_Execute_MissingContent(t *testing.T) {
	svc := &mockKnowledgeService{}
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeAdd_Execute_Success(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("AddKnowledge", mock.Anything, mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.TenantID == "t1" && item.Content == "test content" && item.Source == "src"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "test content", CreatedAt: now,
	}, nil)
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"content":   "test content",
		"source":    "src",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeAdd_Execute_AddError(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("AddKnowledge", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"content":   "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeAdd_Execute_WithOptionalFields(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("AddKnowledge", mock.Anything, mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.Category == "cat" && len(item.Tags) == 1 && item.Tags[0] == "a"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "test", CreatedAt: now,
	}, nil)
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"content":   "test",
		"category":  "cat",
		"tags":      []interface{}{"a"},
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeDelete_New(t *testing.T) {
	svc := &mockKnowledgeService{}
	kd := NewKnowledgeDelete(svc)
	assert.NotNil(t, kd)
	assert.Equal(t, "knowledge_delete", kd.Name())
}

func TestKnowledgeDelete_Execute_MissingTenantID(t *testing.T) {
	svc := &mockKnowledgeService{}
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeDelete_Execute_MissingItemID(t *testing.T) {
	svc := &mockKnowledgeService{}
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeDelete_Execute_Success(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("DeleteKnowledge", mock.Anything, "t1", "1").Return(nil)
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
		"reason":    "outdated",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeDelete_Execute_DeleteError(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("DeleteKnowledge", mock.Anything, "t1", "1").Return(errors.New("db error"))
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "t1",
		"item_id":   "1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	svc.AssertExpectations(t)
}
