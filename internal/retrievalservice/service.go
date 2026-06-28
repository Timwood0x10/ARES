// Package retrieval provides retrieval service implementation.
package retrievalservice

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/errors"
)

// Service provides retrieval operations for knowledge base.
type Service struct {
	repo   core.RetrievalRepository
	config *core.BaseConfig
}

// Config represents service configuration.
type Config struct {
	// BaseConfig is the base configuration.
	BaseConfig *core.BaseConfig
	// Repo is the retrieval repository.
	Repo core.RetrievalRepository
}

// NewService creates a new retrieval service instance.
// Args:
// config - service configuration.
// Returns new retrieval service instance or error.
func NewService(config *Config) (*Service, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}

	if config.BaseConfig == nil {
		config.BaseConfig = &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		}
	}

	return &Service{
		repo:   config.Repo,
		config: config.BaseConfig,
	}, nil
}

// Search performs a knowledge base search.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier.
// query - the search query text.
// Returns list of retrieval results or error.
func (s *Service) Search(ctx context.Context, tenantID, query string) ([]*core.RetrievalResult, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}

	if query == "" {
		return nil, ErrInvalidQuery
	}

	request := &core.RetrievalRequest{
		TenantID: tenantID,
		Query:    query,
		Config: &core.RetrievalConfig{
			Mode:     core.RetrievalModeSimple,
			TopK:     10,
			MinScore: 0.4,
		},
	}

	return s.SearchWithConfig(ctx, request)
}

// SearchWithConfig performs search with custom configuration.
// Args:
// ctx - operation context.
// request - the retrieval request.
// Returns list of retrieval results or error.
func (s *Service) SearchWithConfig(ctx context.Context, request *core.RetrievalRequest) ([]*core.RetrievalResult, error) {
	if request == nil {
		return nil, ErrInvalidConfig
	}

	if request.TenantID == "" {
		return nil, ErrInvalidTenantID
	}

	if request.Query == "" {
		return nil, ErrInvalidQuery
	}

	if request.Config == nil {
		request.Config = &core.RetrievalConfig{
			Mode:     core.RetrievalModeSimple,
			TopK:     10,
			MinScore: 0.4,
		}
	}

	results, err := s.repo.SearchKnowledge(ctx, request)
	if err != nil {
		return nil, errors.Wrap(err, "search knowledge")
	}

	return results, nil
}

// AddKnowledge adds a new knowledge item.
// Args:
// ctx - operation context.
// item - the knowledge item to add.
// Returns the created item or error.
func (s *Service) AddKnowledge(ctx context.Context, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	if item == nil {
		return nil, ErrInvalidConfig
	}

	if item.TenantID == "" {
		return nil, ErrInvalidTenantID
	}

	if item.Content == "" {
		return nil, ErrInvalidContent
	}

	// Generate ID if not provided
	if item.ID == "" {
		item.ID = generateKnowledgeID()
	}

	now := time.Now().Unix()
	item.CreatedAt = now
	item.UpdatedAt = now

	if err := s.repo.CreateKnowledge(ctx, item); err != nil {
		return nil, errors.Wrap(err, "create knowledge")
	}

	return item, nil
}

// GetKnowledge retrieves a knowledge item by ID.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier.
// itemID - the knowledge item identifier.
// Returns the knowledge item or error if not found.
func (s *Service) GetKnowledge(ctx context.Context, tenantID, itemID string) (*core.KnowledgeItem, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}

	if itemID == "" {
		return nil, ErrInvalidItemID
	}

	item, err := s.repo.GetKnowledge(ctx, tenantID, itemID)
	if err != nil {
		return nil, errors.Wrap(err, "get knowledge")
	}

	if item == nil {
		return nil, ErrKnowledgeNotFound
	}

	return item, nil
}

// UpdateKnowledge updates an existing knowledge item.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier.
// item - the knowledge item to update.
// Returns the updated item or error.
func (s *Service) UpdateKnowledge(ctx context.Context, tenantID string, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}

	if item == nil {
		return nil, ErrInvalidConfig
	}

	if item.ID == "" {
		return nil, ErrInvalidItemID
	}

	// Verify item exists and belongs to tenant
	existing, err := s.repo.GetKnowledge(ctx, tenantID, item.ID)
	if err != nil {
		return nil, errors.Wrap(err, "get knowledge")
	}

	if existing == nil {
		return nil, ErrKnowledgeNotFound
	}

	// Update timestamp
	item.UpdatedAt = time.Now().Unix()

	if err := s.repo.UpdateKnowledge(ctx, item); err != nil {
		return nil, errors.Wrap(err, "update knowledge")
	}

	return item, nil
}

// DeleteKnowledge deletes a knowledge item.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier.
// itemID - the knowledge item identifier.
// Returns error if deletion fails.
func (s *Service) DeleteKnowledge(ctx context.Context, tenantID, itemID string) error {
	if tenantID == "" {
		return ErrInvalidTenantID
	}

	if itemID == "" {
		return ErrInvalidItemID
	}

	// Verify item exists and belongs to tenant
	existing, err := s.repo.GetKnowledge(ctx, tenantID, itemID)
	if err != nil {
		return errors.Wrap(err, "get knowledge")
	}

	if existing == nil {
		return ErrKnowledgeNotFound
	}

	if err := s.repo.DeleteKnowledge(ctx, itemID); err != nil {
		return errors.Wrap(err, "delete knowledge")
	}

	return nil
}

// ListKnowledge lists knowledge items with optional filtering.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier.
// filter - optional filter criteria.
// Returns list of knowledge items and pagination info, or error.
func (s *Service) ListKnowledge(ctx context.Context, tenantID string, filter *core.KnowledgeFilter) ([]*core.KnowledgeItem, *core.PaginationResponse, error) {
	if tenantID == "" {
		return nil, nil, ErrInvalidTenantID
	}

	if filter == nil {
		filter = &core.KnowledgeFilter{}
	}

	items, err := s.repo.ListKnowledge(ctx, tenantID, filter)
	if err != nil {
		return nil, nil, errors.Wrap(err, "list knowledge")
	}

	// Calculate pagination info
	total := int64(len(items))
	page := 1
	pageSize := len(items)
	totalPages := 1
	hasMore := false

	if filter.Pagination != nil {
		if filter.Pagination.Page > 0 {
			page = filter.Pagination.Page
		}
		if filter.Pagination.PageSize > 0 {
			pageSize = filter.Pagination.PageSize
		}
		// Calculate total pages based on total items and page size
		if pageSize > 0 {
			totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
		}
		// Check if there are more pages
		hasMore = page < totalPages
	}

	pagination := &core.PaginationResponse{
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		HasMore:    hasMore,
	}

	return items, pagination, nil
}

// generateKnowledgeID generates a unique knowledge item ID.
func generateKnowledgeID() string {
	return "kb_" + uuid.New().String()
}
