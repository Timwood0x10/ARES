// Package core provides core abstractions for retrieval operations.
package core

import "context"

// RetrievalMode represents the retrieval mode.
type RetrievalMode string

const (
	// RetrievalModeSimple represents simple retrieval mode.
	RetrievalModeSimple RetrievalMode = "simple"
	// RetrievalModeAdvanced represents advanced retrieval mode.
	RetrievalModeAdvanced RetrievalMode = "advanced"
	// RetrievalModeHybrid represents hybrid retrieval mode.
	RetrievalModeHybrid RetrievalMode = "hybrid"
)

// RetrievalConfig represents configuration for retrieval operations.
type RetrievalConfig struct {
	// Mode is the retrieval mode to use.
	Mode RetrievalMode
	// TopK is the number of top results to return.
	TopK int
	// MinScore is the minimum similarity score threshold.
	MinScore float64
	// Rerank enables reranking of results.
	Rerank bool
	// Filters are optional filter criteria.
	Filters map[string]interface{}
}

// RetrievalResult represents a single retrieval result.
type RetrievalResult struct {
	// ID is the unique identifier for the result.
	ID string
	// Content is the retrieved content.
	Content string
	// Source is the source of the content.
	Source string
	// SubSource is the sub-source or category.
	SubSource string
	// Score is the similarity score.
	Score float64
	// Metadata is optional metadata.
	Metadata Metadata
}

// RetrievalRequest represents a retrieval request.
type RetrievalRequest struct {
	// TenantID is the tenant identifier for isolation.
	TenantID string
	// Query is the search query text.
	Query string
	// Config is the retrieval configuration.
	Config *RetrievalConfig
}

// KnowledgeItem represents a knowledge base item.
type KnowledgeItem struct {
	// ID is the unique identifier for the item.
	ID string
	// TenantID is the tenant identifier.
	TenantID string
	// Content is the item content.
	Content string
	// Source is the source of the item.
	Source string
	// Category is the category of the item.
	Category string
	// Tags are tags associated with the item.
	Tags []string
	// Embedding is the vector embedding of the content.
	Embedding []float32
	// CreatedAt is the timestamp when the item was created.
	CreatedAt int64
	// UpdatedAt is the timestamp when the item was last updated.
	UpdatedAt int64
	// Metadata is optional metadata.
	Metadata Metadata
}

// RetrievalRepository defines the interface for retrieval data access operations.
type RetrievalRepository interface {
	// CreateKnowledge creates a new knowledge item.
	// Args:
	// ctx - operation context.
	// item - the knowledge item to create.
	// Returns error if creation fails.
	CreateKnowledge(ctx context.Context, item *KnowledgeItem) error

	// GetKnowledge retrieves a knowledge item by ID within a tenant scope.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier for isolation.
	// itemID - the knowledge item identifier.
	// Returns the knowledge item or error if not found.
	GetKnowledge(ctx context.Context, tenantID, itemID string) (*KnowledgeItem, error)

	// UpdateKnowledge updates an existing knowledge item.
	// Args:
	// ctx - operation context.
	// item - the knowledge item to update.
	// Returns error if update fails.
	UpdateKnowledge(ctx context.Context, item *KnowledgeItem) error

	// DeleteKnowledge deletes a knowledge item by ID.
	// Args:
	// ctx - operation context.
	// itemID - the knowledge item identifier.
	// Returns error if deletion fails.
	DeleteKnowledge(ctx context.Context, itemID string) error

	// SearchKnowledge searches for knowledge items.
	// Args:
	// ctx - operation context.
	// query - the search query.
	// Returns list of search results or error.
	SearchKnowledge(ctx context.Context, request *RetrievalRequest) ([]*RetrievalResult, error)

	// ListKnowledge lists knowledge items with optional filtering.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier.
	// filter - optional filter criteria.
	// Returns list of knowledge items or error.
	ListKnowledge(ctx context.Context, tenantID string, filter *KnowledgeFilter) ([]*KnowledgeItem, error)
}

// KnowledgeFilter represents filter criteria for listing knowledge items.
type KnowledgeFilter struct {
	// Source filters by source.
	Source string
	// Category filters by category.
	Category string
	// Tags filters by tags.
	Tags []string
	// Pagination represents pagination parameters.
	Pagination *PaginationRequest
}

// RetrievalService defines the interface for retrieval business logic operations.
type RetrievalService interface {
	// Search performs a knowledge base search.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier.
	// query - the search query text.
	// Returns list of retrieval results or error.
	Search(ctx context.Context, tenantID, query string) ([]*RetrievalResult, error)

	// SearchWithConfig performs search with custom configuration.
	// Args:
	// ctx - operation context.
	// request - the retrieval request.
	// Returns list of retrieval results or error.
	SearchWithConfig(ctx context.Context, request *RetrievalRequest) ([]*RetrievalResult, error)

	// AddKnowledge adds a new knowledge item.
	// Args:
	// ctx - operation context.
	// item - the knowledge item to add.
	// Returns the created item or error.
	AddKnowledge(ctx context.Context, item *KnowledgeItem) (*KnowledgeItem, error)

	// GetKnowledge retrieves a knowledge item by ID.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier.
	// itemID - the knowledge item identifier.
	// Returns the knowledge item or error if not found.
	GetKnowledge(ctx context.Context, tenantID, itemID string) (*KnowledgeItem, error)

	// UpdateKnowledge updates an existing knowledge item.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier.
	// item - the knowledge item to update.
	// Returns the updated item or error.
	UpdateKnowledge(ctx context.Context, tenantID string, item *KnowledgeItem) (*KnowledgeItem, error)

	// DeleteKnowledge deletes a knowledge item.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier.
	// itemID - the knowledge item identifier.
	// Returns error if deletion fails.
	DeleteKnowledge(ctx context.Context, tenantID, itemID string) error

	// ListKnowledge lists knowledge items with optional filtering.
	// Args:
	// ctx - operation context.
	// tenantID - the tenant identifier.
	// filter - optional filter criteria.
	// Returns list of knowledge items and pagination info, or error.
	ListKnowledge(ctx context.Context, tenantID string, filter *KnowledgeFilter) ([]*KnowledgeItem, *PaginationResponse, error)
}
