// Package repositories provides data access interfaces and implementations.
package repositories

import (
	"context"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// KnowledgeRepositoryInterface defines the interface for knowledge base data access.
// All id-scoped methods require a tenantID parameter to enforce tenant isolation
// at the SQL layer (REVIEW #36).
type KnowledgeRepositoryInterface interface {
	// GetByID retrieves a knowledge chunk by ID within the given tenant.
	GetByID(ctx context.Context, tenantID, id string) (*storage_models.KnowledgeChunk, error)

	// Update updates an existing knowledge chunk. The chunk must carry a
	// non-empty TenantID; the SQL predicate includes tenant_id to prevent
	// cross-tenant writes.
	Update(ctx context.Context, chunk *storage_models.KnowledgeChunk) error
}
