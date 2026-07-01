// Package retrieval provides in-memory repository implementation for development/testing.
package retrievalservice

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Timwood0x10/ares/api/core"
)

// MemoryRepository provides an in-memory implementation of RetrievalRepository.
// This is useful for development and testing without a database.
type MemoryRepository struct {
	mu        sync.RWMutex
	knowledge map[string]*core.KnowledgeItem
	idCounter int64
}

// NewMemoryRepository creates a new in-memory retrieval repository.
// Returns new memory repository instance.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		knowledge: make(map[string]*core.KnowledgeItem),
	}
}

// CreateKnowledge creates a new knowledge item.
// Args:
// ctx - operation context.
// item - the knowledge item to create.
// Returns error if creation fails.
func (r *MemoryRepository) CreateKnowledge(ctx context.Context, item *core.KnowledgeItem) error {
	if item == nil {
		return fmt.Errorf("item is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Generate ID if not provided
	if item.ID == "" {
		r.idCounter++
		item.ID = fmt.Sprintf("kb_%d", r.idCounter)
	}

	if _, exists := r.knowledge[item.ID]; exists {
		return fmt.Errorf("knowledge item already exists: %s", item.ID)
	}

	r.knowledge[item.ID] = item
	return nil
}

// GetKnowledge retrieves a knowledge item by ID within a tenant scope.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier for isolation.
// itemID - the knowledge item identifier.
// Returns the knowledge item or error if not found.
func (r *MemoryRepository) GetKnowledge(ctx context.Context, tenantID, itemID string) (*core.KnowledgeItem, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant ID is empty")
	}
	if itemID == "" {
		return nil, fmt.Errorf("item ID is empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	item, exists := r.knowledge[itemID]
	if !exists {
		return nil, ErrKnowledgeNotFound
	}

	if item.TenantID != tenantID {
		return nil, ErrKnowledgeNotFound
	}

	// Return a copy to avoid mutation
	itemCopy := *item
	return &itemCopy, nil
}

// UpdateKnowledge updates an existing knowledge item.
// Args:
// ctx - operation context.
// item - the knowledge item to update.
// Returns error if update fails.
func (r *MemoryRepository) UpdateKnowledge(ctx context.Context, item *core.KnowledgeItem) error {
	if item == nil {
		return fmt.Errorf("item is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.knowledge[item.ID]; !exists {
		return fmt.Errorf("knowledge item not found: %s", item.ID)
	}

	r.knowledge[item.ID] = item
	return nil
}

// DeleteKnowledge deletes a knowledge item by ID.
// Args:
// ctx - operation context.
// itemID - the knowledge item identifier.
// Returns error if deletion fails.
func (r *MemoryRepository) DeleteKnowledge(ctx context.Context, itemID string) error {
	if itemID == "" {
		return fmt.Errorf("item ID is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.knowledge[itemID]; !exists {
		return fmt.Errorf("knowledge item not found: %s", itemID)
	}

	delete(r.knowledge, itemID)
	return nil
}

// SearchKnowledge searches for knowledge items.
// Args:
// ctx - operation context.
// request - the search request.
// Returns list of search results or error.
// Note: This is a simplified implementation that does text matching instead of vector similarity.
func (r *MemoryRepository) SearchKnowledge(ctx context.Context, request *core.RetrievalRequest) ([]*core.RetrievalResult, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make([]*core.RetrievalResult, 0)
	queryLower := strings.ToLower(request.Query)

	for _, item := range r.knowledge {
		// Filter by tenant
		if item.TenantID != request.TenantID {
			continue
		}

		// Apply filters
		if request.Config.Filters != nil {
			if category, ok := request.Config.Filters["category"].(string); ok && item.Category != category {
				continue
			}
			if source, ok := request.Config.Filters["source"].(string); ok && item.Source != source {
				continue
			}
		}

		// Calculate similarity score (simplified text matching)
		contentLower := strings.ToLower(item.Content)
		score := 0.0

		// Exact match
		if contentLower == queryLower {
			score = 1.0
		} else if strings.Contains(contentLower, queryLower) {
			// Partial match
			score = 0.7 + (float64(len(queryLower))/float64(len(contentLower)))*0.2
		} else {
			// Word-level matching
			queryWords := strings.Fields(queryLower)
			contentWords := strings.Fields(contentLower)
			matches := 0
			for _, qw := range queryWords {
				for _, cw := range contentWords {
					if strings.Contains(cw, qw) || strings.Contains(qw, cw) {
						matches++
						break
					}
				}
			}
			if len(queryWords) > 0 {
				score = float64(matches) / float64(len(queryWords)) * 0.6
			}
		}

		// Filter by minimum score
		if score < request.Config.MinScore {
			continue
		}

		results = append(results, &core.RetrievalResult{
			ID:        item.ID,
			Content:   item.Content,
			Source:    item.Source,
			SubSource: item.Category,
			Score:     score,
			Metadata:  item.Metadata,
		})
	}

	// Sort by score (descending)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit results
	topK := request.Config.TopK
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	return results, nil
}

// ListKnowledge lists knowledge items with optional filtering.
// Args:
// ctx - operation context.
// tenantID - the tenant identifier.
// filter - optional filter criteria.
// Returns list of knowledge items or error.
func (r *MemoryRepository) ListKnowledge(ctx context.Context, tenantID string, filter *core.KnowledgeFilter) ([]*core.KnowledgeItem, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant ID is empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	items := make([]*core.KnowledgeItem, 0)
	for _, item := range r.knowledge {
		// Filter by tenant
		if item.TenantID != tenantID {
			continue
		}

		// Apply filters
		if filter != nil {
			if filter.Source != "" && item.Source != filter.Source {
				continue
			}
			if filter.Category != "" && item.Category != filter.Category {
				continue
			}
			if len(filter.Tags) > 0 {
				matched := false
				for _, filterTag := range filter.Tags {
					for _, itemTag := range item.Tags {
						if itemTag == filterTag {
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
				if !matched {
					continue
				}
			}
		}

		// Return a copy to avoid mutation
		itemCopy := *item
		items = append(items, &itemCopy)
	}

	// Apply pagination
	if filter != nil && filter.Pagination != nil {
		limit := filter.Pagination.Limit
		if limit <= 0 {
			limit = filter.Pagination.PageSize
		}
		if limit <= 0 {
			limit = 100 // default limit
		}

		offset := filter.Pagination.Offset
		if offset <= 0 && filter.Pagination.Page > 0 {
			offset = (filter.Pagination.Page - 1) * limit
		}

		if offset >= len(items) {
			return []*core.KnowledgeItem{}, nil
		}

		if offset+limit > len(items) {
			limit = len(items) - offset
		}

		items = items[offset : offset+limit]
	}

	return items, nil
}
