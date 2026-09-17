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

// TestKnowledgeSearch_Execute_ServerTenantWithoutParam is the #63
// regression: tenant_id used to be a required LLM-supplied parameter, so
// isolation depended on the model's honesty. The tenant is now server-side:
// a request WITHOUT any tenant_id must be served under serverTenantID.
func TestKnowledgeSearch_Execute_ServerTenantWithoutParam(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, serverTenantID, "test").Return([]*RetrievalResult{}, nil)
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"query": "test",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	s.AssertExpectations(t)
}

// TestKnowledgeSearch_Execute_IgnoresCallerTenantID is the #63 hostile-path
// regression: an LLM-supplied tenant_id (here pretending to be another
// tenant) must be IGNORED — the searcher still sees serverTenantID.
func TestKnowledgeSearch_Execute_IgnoresCallerTenantID(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, serverTenantID, "test").Return([]*RetrievalResult{}, nil)
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "some-other-tenant",
		"query":     "test",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	s.AssertExpectations(t)
}

func TestKnowledgeSearch_Execute_MissingQuery(t *testing.T) {
	s := &mockSearcher{}
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeSearch_Execute_SearcherError(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, "default", "test").Return(nil, errors.New("search failed"))
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"query": "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	s.AssertExpectations(t)
}

func TestKnowledgeSearch_Execute_Success(t *testing.T) {
	s := &mockSearcher{}
	s.On("Search", mock.Anything, "default", "test").Return([]*RetrievalResult{
		{ID: "1", Score: 0.95, Content: "result content", Source: "src", Metadata: map[string]interface{}{"key": "val"}},
	}, nil)
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"query": "test",
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
	s.On("Search", mock.Anything, "default", "test").Return([]*RetrievalResult{}, nil)
	ks := NewKnowledgeSearch(s)
	result, err := ks.Execute(context.Background(), map[string]interface{}{
		"query": "test",
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

// TestKnowledgeUpdate_Execute_ServerTenantWithoutParam is the #63
// regression: tenant_id is no longer a tool parameter; the update path
// operates on serverTenantID.
func TestKnowledgeUpdate_Execute_ServerTenantWithoutParam(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("GetKnowledge", mock.Anything, serverTenantID, "1").Return(nil, nil)
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success) // item not found under the server tenant
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_MissingItemID(t *testing.T) {
	svc := &mockKnowledgeService{}
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeUpdate_Execute_MissingContent(t *testing.T) {
	svc := &mockKnowledgeService{}
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeUpdate_Execute_GetError(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("GetKnowledge", mock.Anything, "default", "1").Return(nil, errors.New("not found"))
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"content": "new content",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_Success(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("GetKnowledge", mock.Anything, "default", "1").Return(&KnowledgeItem{
		ID: "1", TenantID: "default", Content: "old", CreatedAt: now, UpdatedAt: now,
	}, nil)
	svc.On("UpdateKnowledge", mock.Anything, "default", mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.Content == "updated content" && item.Source == "new-source"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "updated content", UpdatedAt: now,
	}, nil)
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"content": "updated content",
		"source":  "new-source",
		"reason":  "correction",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_WithTags(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("GetKnowledge", mock.Anything, "default", "1").Return(&KnowledgeItem{
		ID: "1", TenantID: "default", Content: "old", CreatedAt: now, UpdatedAt: now,
	}, nil)
	svc.On("UpdateKnowledge", mock.Anything, "default", mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.Content == "updated" && len(item.Tags) == 2 && item.Tags[0] == "tag1"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "updated", UpdatedAt: now,
	}, nil)
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"content": "updated",
		"tags":    []interface{}{"tag1", "tag2"},
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeUpdate_Execute_UpdateError(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("GetKnowledge", mock.Anything, "default", "1").Return(&KnowledgeItem{
		ID: "1", TenantID: "default", Content: "old", CreatedAt: now, UpdatedAt: now,
	}, nil)
	svc.On("UpdateKnowledge", mock.Anything, "default", mock.Anything).Return(nil, errors.New("db error"))
	ku := NewKnowledgeUpdate(svc)
	result, err := ku.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"content": "updated",
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

// TestKnowledgeAdd_Execute_ServerTenant is the #63 regression: items are
// always created under serverTenantID regardless of any caller-supplied
// tenant_id.
func TestKnowledgeAdd_Execute_ServerTenant(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("AddKnowledge", mock.Anything, mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.TenantID == serverTenantID && item.Content == "test"
	})).Return(&KnowledgeItem{ID: "new-id", TenantID: serverTenantID, Content: "test"}, nil)
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "attacker-tenant",
		"content":   "test",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeAdd_Execute_MissingContent(t *testing.T) {
	svc := &mockKnowledgeService{}
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeAdd_Execute_Success(t *testing.T) {
	svc := &mockKnowledgeService{}
	now := time.Now()
	svc.On("AddKnowledge", mock.Anything, mock.MatchedBy(func(item *KnowledgeItem) bool {
		return item.TenantID == "default" && item.Content == "test content" && item.Source == "src"
	})).Return(&KnowledgeItem{
		ID: "1", Content: "test content", CreatedAt: now,
	}, nil)
	ka := NewKnowledgeAdd(svc)
	result, err := ka.Execute(context.Background(), map[string]interface{}{
		"content": "test content",
		"source":  "src",
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
		"content": "test",
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
		"content":  "test",
		"category": "cat",
		"tags":     []interface{}{"a"},
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

// TestKnowledgeDelete_Execute_ServerTenant is the #63 regression: deletion
// runs under serverTenantID; a caller-supplied tenant_id is ignored.
func TestKnowledgeDelete_Execute_ServerTenant(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("DeleteKnowledge", mock.Anything, serverTenantID, "1").Return(nil)
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"tenant_id": "attacker-tenant",
		"item_id":   "1",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeDelete_Execute_MissingItemID(t *testing.T) {
	svc := &mockKnowledgeService{}
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestKnowledgeDelete_Execute_Success(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("DeleteKnowledge", mock.Anything, "default", "1").Return(nil)
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
		"reason":  "outdated",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	svc.AssertExpectations(t)
}

func TestKnowledgeDelete_Execute_DeleteError(t *testing.T) {
	svc := &mockKnowledgeService{}
	svc.On("DeleteKnowledge", mock.Anything, "default", "1").Return(errors.New("db error"))
	kd := NewKnowledgeDelete(svc)
	result, err := kd.Execute(context.Background(), map[string]interface{}{
		"item_id": "1",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	svc.AssertExpectations(t)
}
