package retrievalservice_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/retrievalservice"
)

type mockRetrievalRepository struct {
	mu    sync.Mutex
	items map[string]*core.KnowledgeItem

	createErr error
	getErr    error
	updateErr error
	deleteErr error
	searchErr error
	listErr   error

	createCalls int
	getCalls    int
	updateCalls int
	deleteCalls int
	searchCalls int
	listCalls   int
}

func newMockRetrievalRepository() *mockRetrievalRepository {
	return &mockRetrievalRepository{
		items: make(map[string]*core.KnowledgeItem),
	}
}

func (m *mockRetrievalRepository) CreateKnowledge(_ context.Context, item *core.KnowledgeItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	if m.createErr != nil {
		return m.createErr
	}
	if item == nil {
		return errors.New("item is nil")
	}
	if _, exists := m.items[item.ID]; exists {
		return errors.New("knowledge item already exists")
	}
	cp := *item
	m.items[item.ID] = &cp
	return nil
}

func (m *mockRetrievalRepository) GetKnowledge(_ context.Context, tenantID, itemID string) (*core.KnowledgeItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if tenantID == "" {
		return nil, errors.New("tenant ID is empty")
	}
	if itemID == "" {
		return nil, errors.New("item ID is empty")
	}
	item, exists := m.items[itemID]
	if !exists {
		return nil, retrievalservice.ErrKnowledgeNotFound
	}
	if item.TenantID != tenantID {
		return nil, retrievalservice.ErrKnowledgeNotFound
	}
	cp := *item
	return &cp, nil
}

func (m *mockRetrievalRepository) UpdateKnowledge(_ context.Context, item *core.KnowledgeItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls++
	if m.updateErr != nil {
		return m.updateErr
	}
	if item == nil {
		return errors.New("item is nil")
	}
	if _, exists := m.items[item.ID]; !exists {
		return retrievalservice.ErrKnowledgeNotFound
	}
	cp := *item
	m.items[item.ID] = &cp
	return nil
}

func (m *mockRetrievalRepository) DeleteKnowledge(_ context.Context, itemID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if itemID == "" {
		return errors.New("item ID is empty")
	}
	if _, exists := m.items[itemID]; !exists {
		return retrievalservice.ErrKnowledgeNotFound
	}
	delete(m.items, itemID)
	return nil
}

func (m *mockRetrievalRepository) SearchKnowledge(_ context.Context, _ *core.RetrievalRequest) ([]*core.RetrievalResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchCalls++
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return []*core.RetrievalResult{}, nil
}

func (m *mockRetrievalRepository) ListKnowledge(_ context.Context, tenantID string, _ *core.KnowledgeFilter) ([]*core.KnowledgeItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]*core.KnowledgeItem, 0)
	for _, item := range m.items {
		if item.TenantID == tenantID {
			cp := *item
			result = append(result, &cp)
		}
	}
	return result, nil
}

func TestNewService(t *testing.T) {
	t.Run("nil config returns error", func(t *testing.T) {
		svc, err := retrievalservice.NewService(nil)
		assert.Nil(t, svc)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidConfig)
	})

	t.Run("nil BaseConfig applies defaults", func(t *testing.T) {
		svc, err := retrievalservice.NewService(&retrievalservice.Config{
			Repo: newMockRetrievalRepository(),
		})
		require.NoError(t, err)
		require.NotNil(t, svc)
	})

	t.Run("valid config returns service", func(t *testing.T) {
		svc, err := retrievalservice.NewService(&retrievalservice.Config{
			BaseConfig: &core.BaseConfig{
				RequestTimeout: 10 * time.Second,
				MaxRetries:     5,
				RetryDelay:     2 * time.Second,
			},
			Repo: newMockRetrievalRepository(),
		})
		require.NoError(t, err)
		require.NotNil(t, svc)
	})
}

func TestSearch(t *testing.T) {
	ctx := context.Background()

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.Search(ctx, "", "query")
		assert.Nil(t, results)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.searchCalls)
	})

	t.Run("empty query returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.Search(ctx, "tenant-1", "")
		assert.Nil(t, results)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidQuery)
		assert.Equal(t, 0, mock.searchCalls)
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.searchErr = errors.New("db down")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.Search(ctx, "tenant-1", "query")
		assert.Nil(t, results)
		assert.ErrorContains(t, err, "db down")
		assert.ErrorContains(t, err, "search knowledge")
		assert.Equal(t, 1, mock.searchCalls)
	})

	t.Run("success returns results", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.searchErr = nil
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.Search(ctx, "tenant-1", "query")
		require.NoError(t, err)
		assert.NotNil(t, results)
		assert.Equal(t, 1, mock.searchCalls)
	})
}

func TestSearchWithConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("nil request returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, nil)
		assert.Nil(t, results)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidConfig)
		assert.Equal(t, 0, mock.searchCalls)
	})

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, &core.RetrievalRequest{
			TenantID: "",
			Query:    "query",
		})
		assert.Nil(t, results)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.searchCalls)
	})

	t.Run("empty query returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, &core.RetrievalRequest{
			TenantID: "tenant-1",
			Query:    "",
		})
		assert.Nil(t, results)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidQuery)
		assert.Equal(t, 0, mock.searchCalls)
	})

	t.Run("nil Config applies defaults", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.searchErr = nil
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, &core.RetrievalRequest{
			TenantID: "tenant-1",
			Query:    "query",
			Config:   nil,
		})
		require.NoError(t, err)
		assert.NotNil(t, results)
		assert.Equal(t, 1, mock.searchCalls)
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.searchErr = errors.New("timeout")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, &core.RetrievalRequest{
			TenantID: "tenant-1",
			Query:    "query",
			Config:   &core.RetrievalConfig{},
		})
		assert.Nil(t, results)
		assert.ErrorContains(t, err, "timeout")
		assert.ErrorContains(t, err, "search knowledge")
	})

	t.Run("success returns results", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, &core.RetrievalRequest{
			TenantID: "tenant-1",
			Query:    "query",
			Config: &core.RetrievalConfig{
				Mode:     core.RetrievalModeAdvanced,
				TopK:     5,
				MinScore: 0.5,
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, results)
		assert.Equal(t, 1, mock.searchCalls)
	})

	t.Run("success with filters", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		results, err := svc.SearchWithConfig(ctx, &core.RetrievalRequest{
			TenantID: "tenant-1",
			Query:    "query",
			Config: &core.RetrievalConfig{
				Filters: map[string]interface{}{
					"category": "tech",
				},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, results)
		assert.Equal(t, 1, mock.searchCalls)
	})
}

func TestAddKnowledge(t *testing.T) {
	ctx := context.Background()

	t.Run("nil item returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.AddKnowledge(ctx, nil)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidConfig)
		assert.Equal(t, 0, mock.createCalls)
	})

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.AddKnowledge(ctx, &core.KnowledgeItem{
			TenantID: "",
			Content:  "content",
		})
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.createCalls)
	})

	t.Run("empty content returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.AddKnowledge(ctx, &core.KnowledgeItem{
			TenantID: "tenant-1",
			Content:  "",
		})
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidContent)
		assert.Equal(t, 0, mock.createCalls)
	})

	t.Run("empty ID auto-generates", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.AddKnowledge(ctx, &core.KnowledgeItem{
			TenantID: "tenant-1",
			Content:  "content",
			Source:   "src",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.ID, "kb_")
		assert.Equal(t, "tenant-1", result.TenantID)
		assert.Equal(t, "content", result.Content)
		assert.NotZero(t, result.CreatedAt)
		assert.NotZero(t, result.UpdatedAt)
		assert.Equal(t, 1, mock.createCalls)
	})

	t.Run("preserves existing ID", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.AddKnowledge(ctx, &core.KnowledgeItem{
			ID:       "custom-id",
			TenantID: "tenant-1",
			Content:  "content",
		})
		require.NoError(t, err)
		assert.Equal(t, "custom-id", result.ID)
		assert.Equal(t, 1, mock.createCalls)
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.createErr = errors.New("storage full")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.AddKnowledge(ctx, &core.KnowledgeItem{
			TenantID: "tenant-1",
			Content:  "content",
		})
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "storage full")
		assert.ErrorContains(t, err, "create knowledge")
	})
}

func TestGetKnowledge(t *testing.T) {
	ctx := context.Background()

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.GetKnowledge(ctx, "", "item-1")
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.getCalls)
	})

	t.Run("empty itemID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.GetKnowledge(ctx, "tenant-1", "")
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidItemID)
		assert.Equal(t, 0, mock.getCalls)
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.getErr = errors.New("connection lost")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.GetKnowledge(ctx, "tenant-1", "item-1")
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "connection lost")
		assert.ErrorContains(t, err, "get knowledge")
	})

	t.Run("nil result returns ErrKnowledgeNotFound", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.GetKnowledge(ctx, "tenant-1", "nonexistent")
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrKnowledgeNotFound)
		assert.True(t, errors.Is(err, apperrors.ErrNotFound))
		assert.Equal(t, 1, mock.getCalls)
	})

	t.Run("success returns item", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
			ID:       "known-item",
			TenantID: "tenant-1",
			Content:  "some content",
		})
		require.NoError(t, err)

		result, err := svc.GetKnowledge(ctx, "tenant-1", "known-item")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "known-item", result.ID)
		assert.Equal(t, "tenant-1", result.TenantID)
		assert.Equal(t, "some content", result.Content)
	})
}

func TestUpdateKnowledge(t *testing.T) {
	ctx := context.Background()

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.UpdateKnowledge(ctx, "", &core.KnowledgeItem{ID: "item-1"})
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.getCalls)
		assert.Equal(t, 0, mock.updateCalls)
	})

	t.Run("nil item returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.UpdateKnowledge(ctx, "tenant-1", nil)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidConfig)
	})

	t.Run("empty item ID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.UpdateKnowledge(ctx, "tenant-1", &core.KnowledgeItem{ID: ""})
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidItemID)
		assert.Equal(t, 0, mock.getCalls)
	})

	t.Run("not found returns ErrKnowledgeNotFound", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.UpdateKnowledge(ctx, "tenant-1", &core.KnowledgeItem{ID: "nonexistent"})
		assert.Nil(t, result)
		assert.ErrorIs(t, err, retrievalservice.ErrKnowledgeNotFound)
		assert.Equal(t, 1, mock.getCalls)
		assert.Equal(t, 0, mock.updateCalls)
	})

	t.Run("get knowledge error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.getErr = errors.New("db error")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		result, err := svc.UpdateKnowledge(ctx, "tenant-1", &core.KnowledgeItem{ID: "item-1"})
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "db error")
		assert.ErrorContains(t, err, "get knowledge")
		assert.Equal(t, 1, mock.getCalls)
	})

	t.Run("update repo error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
			ID:       "updatable",
			TenantID: "tenant-1",
			Content:  "original",
		})
		require.NoError(t, err)

		mock.updateErr = errors.New("update failed")
		result, err := svc.UpdateKnowledge(ctx, "tenant-1", &core.KnowledgeItem{
			ID:      "updatable",
			Content: "updated",
		})
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "update failed")
		assert.ErrorContains(t, err, "update knowledge")
	})

	t.Run("success updates item", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		before := time.Now().Unix()
		created, err := svc.AddKnowledge(ctx, &core.KnowledgeItem{
			ID:       "updatable",
			TenantID: "tenant-1",
			Content:  "original",
		})
		require.NoError(t, err)

		result, err := svc.UpdateKnowledge(ctx, "tenant-1", &core.KnowledgeItem{
			ID:        "updatable",
			Content:   "updated",
			Source:    "new-source",
			CreatedAt: created.CreatedAt,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "updatable", result.ID)
		assert.Equal(t, "updated", result.Content)
		assert.Equal(t, "new-source", result.Source)
		assert.Equal(t, created.CreatedAt, result.CreatedAt)
		assert.GreaterOrEqual(t, result.UpdatedAt, before)
	})
}

func TestDeleteKnowledge(t *testing.T) {
	ctx := context.Background()

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		err = svc.DeleteKnowledge(ctx, "", "item-1")
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.getCalls)
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("empty itemID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		err = svc.DeleteKnowledge(ctx, "tenant-1", "")
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidItemID)
		assert.Equal(t, 0, mock.getCalls)
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("get knowledge error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.getErr = errors.New("db error")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		err = svc.DeleteKnowledge(ctx, "tenant-1", "item-1")
		assert.ErrorContains(t, err, "db error")
		assert.ErrorContains(t, err, "get knowledge")
		assert.Equal(t, 1, mock.getCalls)
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("not found returns ErrKnowledgeNotFound", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		err = svc.DeleteKnowledge(ctx, "tenant-1", "nonexistent")
		assert.ErrorIs(t, err, retrievalservice.ErrKnowledgeNotFound)
		assert.Equal(t, 1, mock.getCalls)
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("delete error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
			ID:       "deletable",
			TenantID: "tenant-1",
			Content:  "to be deleted",
		})
		require.NoError(t, err)

		mock.deleteErr = errors.New("delete failed")
		err = svc.DeleteKnowledge(ctx, "tenant-1", "deletable")
		assert.ErrorContains(t, err, "delete failed")
		assert.ErrorContains(t, err, "delete knowledge")
	})

	t.Run("success deletes item", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
			ID:       "deletable",
			TenantID: "tenant-1",
			Content:  "to be deleted",
		})
		require.NoError(t, err)

		err = svc.DeleteKnowledge(ctx, "tenant-1", "deletable")
		require.NoError(t, err)

		_, err = svc.GetKnowledge(ctx, "tenant-1", "deletable")
		assert.ErrorIs(t, err, retrievalservice.ErrKnowledgeNotFound)
	})
}

func TestListKnowledge(t *testing.T) {
	ctx := context.Background()

	t.Run("empty tenantID returns error", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		items, pagination, err := svc.ListKnowledge(ctx, "", nil)
		assert.Nil(t, items)
		assert.Nil(t, pagination)
		assert.ErrorIs(t, err, retrievalservice.ErrInvalidTenantID)
		assert.Equal(t, 0, mock.listCalls)
	})

	t.Run("nil filter uses defaults", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		items, pagination, err := svc.ListKnowledge(ctx, "tenant-1", nil)
		require.NoError(t, err)
		assert.NotNil(t, items)
		require.NotNil(t, pagination)
		assert.Equal(t, int64(0), pagination.Total)
		assert.Equal(t, 1, pagination.Page)
		assert.Equal(t, 1, pagination.TotalPages)
		assert.False(t, pagination.HasMore)
		assert.Equal(t, 1, mock.listCalls)
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		mock.listErr = errors.New("list failed")
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		items, pagination, err := svc.ListKnowledge(ctx, "tenant-1", nil)
		assert.Nil(t, items)
		assert.Nil(t, pagination)
		assert.ErrorContains(t, err, "list failed")
		assert.ErrorContains(t, err, "list knowledge")
	})

	t.Run("pagination with no items", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		items, pagination, err := svc.ListKnowledge(ctx, "tenant-1", &core.KnowledgeFilter{
			Pagination: &core.PaginationRequest{
				Page:     1,
				PageSize: 10,
			},
		})
		require.NoError(t, err)
		assert.Empty(t, items)
		require.NotNil(t, pagination)
		assert.Equal(t, int64(0), pagination.Total)
		assert.Equal(t, 1, pagination.Page)
		assert.Equal(t, 10, pagination.PageSize)
		assert.Equal(t, 0, pagination.TotalPages)
		assert.False(t, pagination.HasMore)
	})

	t.Run("pagination with items", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		for i := 1; i <= 5; i++ {
			_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
				TenantID: "tenant-1",
				Content:  "item",
			})
			require.NoError(t, err)
		}

		items, pagination, err := svc.ListKnowledge(ctx, "tenant-1", &core.KnowledgeFilter{
			Pagination: &core.PaginationRequest{
				Page:     1,
				PageSize: 2,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, items)
		require.NotNil(t, pagination)
		assert.Equal(t, int64(5), pagination.Total)
		assert.Equal(t, 1, pagination.Page)
		assert.Equal(t, 2, pagination.PageSize)
		assert.Equal(t, 3, pagination.TotalPages)
		assert.True(t, pagination.HasMore)
		assert.Len(t, items, 5)
	})

	t.Run("pagination hasMore false on last page", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		for i := 1; i <= 3; i++ {
			_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
				TenantID: "tenant-1",
				Content:  "item",
			})
			require.NoError(t, err)
		}

		items, pagination, err := svc.ListKnowledge(ctx, "tenant-1", &core.KnowledgeFilter{
			Pagination: &core.PaginationRequest{
				Page:     2,
				PageSize: 2,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(3), pagination.Total)
		assert.Equal(t, 2, pagination.Page)
		assert.Equal(t, 2, pagination.PageSize)
		assert.Equal(t, 2, pagination.TotalPages)
		assert.False(t, pagination.HasMore)
		assert.Len(t, items, 3)
	})

	t.Run("pagination hasMore on first page", func(t *testing.T) {
		mock := newMockRetrievalRepository()
		svc, err := retrievalservice.NewService(&retrievalservice.Config{Repo: mock})
		require.NoError(t, err)

		for i := 1; i <= 5; i++ {
			_, err = svc.AddKnowledge(ctx, &core.KnowledgeItem{
				TenantID: "tenant-1",
				Content:  "item",
			})
			require.NoError(t, err)
		}

		_, pagination, err := svc.ListKnowledge(ctx, "tenant-1", &core.KnowledgeFilter{
			Pagination: &core.PaginationRequest{
				Page:     1,
				PageSize: 2,
			},
		})
		require.NoError(t, err)
		assert.True(t, pagination.HasMore, "when page < totalPages, HasMore should be true")
	})
}

func TestErrorSentinelWrapping(t *testing.T) {
	t.Run("ErrKnowledgeNotFound wraps apperrors.ErrNotFound", func(t *testing.T) {
		assert.True(t, errors.Is(retrievalservice.ErrKnowledgeNotFound, apperrors.ErrNotFound),
			"ErrKnowledgeNotFound should wrap apperrors.ErrNotFound")
	})

	t.Run("other sentinels do not wrap ErrNotFound", func(t *testing.T) {
		assert.False(t, errors.Is(retrievalservice.ErrInvalidTenantID, apperrors.ErrNotFound))
		assert.False(t, errors.Is(retrievalservice.ErrInvalidQuery, apperrors.ErrNotFound))
		assert.False(t, errors.Is(retrievalservice.ErrInvalidConfig, apperrors.ErrNotFound))
		assert.False(t, errors.Is(retrievalservice.ErrInvalidContent, apperrors.ErrNotFound))
		assert.False(t, errors.Is(retrievalservice.ErrInvalidItemID, apperrors.ErrNotFound))
		assert.False(t, errors.Is(retrievalservice.ErrAccessDenied, apperrors.ErrNotFound))
		assert.False(t, errors.Is(retrievalservice.ErrSearchFailed, apperrors.ErrNotFound))
	})
}

func TestMemoryRepository(t *testing.T) {
	ctx := context.Background()

	t.Run("NewMemoryRepository creates empty store", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		require.NotNil(t, repo)

		items, err := repo.ListKnowledge(ctx, "tenant-1", nil)
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("CreateKnowledge stores item", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		item := &core.KnowledgeItem{
			ID:       "test-1",
			TenantID: "tenant-1",
			Content:  "content",
		}
		err := repo.CreateKnowledge(ctx, item)
		require.NoError(t, err)

		got, err := repo.GetKnowledge(ctx, "tenant-1", "test-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "content", got.Content)
	})

	t.Run("CreateKnowledge nil item returns error", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.CreateKnowledge(ctx, nil)
		assert.Error(t, err)
	})

	t.Run("CreateKnowledge duplicate returns error", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "dup",
			TenantID: "t1",
			Content:  "first",
		})
		require.NoError(t, err)

		err = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "dup",
			TenantID: "t1",
			Content:  "second",
		})
		assert.Error(t, err)
	})

	t.Run("GetKnowledge enforces tenant isolation", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "shared",
			TenantID: "tenant-a",
			Content:  "secret",
		})
		require.NoError(t, err)

		item, err := repo.GetKnowledge(ctx, "tenant-b", "shared")
		assert.Nil(t, item)
		assert.ErrorIs(t, err, retrievalservice.ErrKnowledgeNotFound)
	})

	t.Run("UpdateKnowledge updates item", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "u",
			TenantID: "t1",
			Content:  "old",
		})
		require.NoError(t, err)

		err = repo.UpdateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "u",
			TenantID: "t1",
			Content:  "new",
		})
		require.NoError(t, err)

		got, err := repo.GetKnowledge(ctx, "t1", "u")
		require.NoError(t, err)
		assert.Equal(t, "new", got.Content)
	})

	t.Run("UpdateKnowledge nonexistent returns error", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.UpdateKnowledge(ctx, &core.KnowledgeItem{
			ID:      "ghost",
			Content: "x",
		})
		assert.Error(t, err)
	})

	t.Run("DeleteKnowledge removes item", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "del",
			TenantID: "t1",
			Content:  "x",
		})
		require.NoError(t, err)

		err = repo.DeleteKnowledge(ctx, "del")
		require.NoError(t, err)

		_, err = repo.GetKnowledge(ctx, "t1", "del")
		assert.ErrorIs(t, err, retrievalservice.ErrKnowledgeNotFound)
	})

	t.Run("DeleteKnowledge nonexistent returns error", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		err := repo.DeleteKnowledge(ctx, "ghost")
		assert.Error(t, err)
	})

	t.Run("SearchKnowledge returns matching results", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "a",
			TenantID: "t1",
			Content:  "how to configure the database",
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "b",
			TenantID: "t1",
			Content:  "database connection pool settings",
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "c",
			TenantID: "t1",
			Content:  "frontend styling guide",
		})

		results, err := repo.SearchKnowledge(ctx, &core.RetrievalRequest{
			TenantID: "t1",
			Query:    "database",
			Config: &core.RetrievalConfig{
				TopK:     10,
				MinScore: 0.01,
			},
		})
		require.NoError(t, err)
		require.Len(t, results, 2)
	})

	t.Run("SearchKnowledge enforces tenant isolation", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "x",
			TenantID: "t1",
			Content:  "secret data",
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "y",
			TenantID: "t2",
			Content:  "other data",
		})

		results, err := repo.SearchKnowledge(ctx, &core.RetrievalRequest{
			TenantID: "t1",
			Query:    "data",
			Config: &core.RetrievalConfig{
				TopK:     10,
				MinScore: 0.0,
			},
		})
		require.NoError(t, err)
		assert.Len(t, results, 1)
	})

	t.Run("SearchKnowledge respects TopK", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		for i := 0; i < 5; i++ {
			_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
				ID:       fmt.Sprintf("t-%d", i),
				TenantID: "t1",
				Content:  "matching content",
			})
		}

		results, err := repo.SearchKnowledge(ctx, &core.RetrievalRequest{
			TenantID: "t1",
			Query:    "content",
			Config: &core.RetrievalConfig{
				TopK:     3,
				MinScore: 0.0,
			},
		})
		require.NoError(t, err)
		assert.Len(t, results, 3)
	})

	t.Run("SearchKnowledge filters by MinScore", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "perfect",
			TenantID: "t1",
			Content:  "exact",
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "partial",
			TenantID: "t1",
			Content:  "something completely different here",
		})

		results, err := repo.SearchKnowledge(ctx, &core.RetrievalRequest{
			TenantID: "t1",
			Query:    "exact",
			Config: &core.RetrievalConfig{
				TopK:     10,
				MinScore: 0.9, // only exact matches score 1.0
			},
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "perfect", results[0].ID)
		assert.Equal(t, 1.0, results[0].Score)
	})

	t.Run("SearchKnowledge filters by category", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "tech",
			TenantID: "t1",
			Content:  "database optimization",
			Category: "technology",
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "fin",
			TenantID: "t1",
			Content:  "database finance report",
			Category: "finance",
		})

		results, err := repo.SearchKnowledge(ctx, &core.RetrievalRequest{
			TenantID: "t1",
			Query:    "database",
			Config: &core.RetrievalConfig{
				TopK:     10,
				MinScore: 0.0,
				Filters: map[string]interface{}{
					"category": "technology",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "tech", results[0].ID)
	})

	t.Run("ListKnowledge filters by source", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "s1",
			TenantID: "t1",
			Content:  "a",
			Source:   "wiki",
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "s2",
			TenantID: "t1",
			Content:  "b",
			Source:   "docs",
		})

		items, err := repo.ListKnowledge(ctx, "t1", &core.KnowledgeFilter{
			Source: "wiki",
		})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "s1", items[0].ID)
	})

	t.Run("ListKnowledge filters by tags", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "tag1",
			TenantID: "t1",
			Content:  "a",
			Tags:     []string{"go", "backend"},
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "tag2",
			TenantID: "t1",
			Content:  "b",
			Tags:     []string{"python", "backend"},
		})
		_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
			ID:       "tag3",
			TenantID: "t1",
			Content:  "c",
			Tags:     nil,
		})

		items, err := repo.ListKnowledge(ctx, "t1", &core.KnowledgeFilter{
			Tags: []string{"go"},
		})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "tag1", items[0].ID)
	})

	t.Run("ListKnowledge applies pagination", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		for i := 0; i < 10; i++ {
			_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
				ID:       fmt.Sprintf("page-%d", i),
				TenantID: "t1",
				Content:  fmt.Sprintf("item %d", i),
			})
		}

		items, err := repo.ListKnowledge(ctx, "t1", &core.KnowledgeFilter{
			Pagination: &core.PaginationRequest{
				Offset: 0,
				Limit:  3,
			},
		})
		require.NoError(t, err)
		assert.Len(t, items, 3)
	})

	t.Run("ListKnowledge empty result for out-of-range offset", func(t *testing.T) {
		repo := retrievalservice.NewMemoryRepository()
		for i := 0; i < 3; i++ {
			_ = repo.CreateKnowledge(ctx, &core.KnowledgeItem{
				TenantID: "t1",
				Content:  fmt.Sprintf("item %d", i),
			})
		}

		items, err := repo.ListKnowledge(ctx, "t1", &core.KnowledgeFilter{
			Pagination: &core.PaginationRequest{
				Offset: 100,
				Limit:  10,
			},
		})
		require.NoError(t, err)
		assert.Empty(t, items)
	})
}
