// Package repositories provides data access interfaces and implementations.
package repositories

import (
	"context"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// ExperienceRepositoryInterface defines the interface for experience data access.
// All id-scoped methods require a tenantID parameter to enforce tenant isolation
// at the SQL layer (REVIEW #36).
type ExperienceRepositoryInterface interface {
	// Create inserts a new experience into the database.
	Create(ctx context.Context, exp *storage_models.Experience) error

	// GetByID retrieves an experience by ID within the given tenant.
	GetByID(ctx context.Context, tenantID, id string) (*storage_models.Experience, error)

	// Update updates an existing experience. The experience must carry a
	// non-empty TenantID; the SQL predicate includes tenant_id.
	Update(ctx context.Context, exp *storage_models.Experience) error

	// UpdateEmbedding writes back only the vector columns of one row. Narrower
	// than Update on purpose: the async embedding worker and a synchronous
	// fallback can target the same row, and a full-row update would clobber
	// concurrent writes to the non-vector columns.
	UpdateEmbedding(ctx context.Context, tenantID, id string, embedding []float64, model string, version int) error

	// Delete removes an experience by its ID.
	Delete(ctx context.Context, id, tenantID string) error

	// SearchByVector performs vector similarity search for experiences.
	SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*storage_models.Experience, error)

	// SearchByKeyword performs keyword-based search for experiences.
	SearchByKeyword(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.Experience, error)

	// IncrementUsageCount increments the usage count of an experience
	// within the given tenant.
	IncrementUsageCount(ctx context.Context, tenantID, id string) error

	// DecrementRank decreases the score of an experience as negative feedback.
	// This is used when an experience leads to a failed task.
	DecrementRank(ctx context.Context, tenantID, id string) error

	// ListByType retrieves experiences by type.
	ListByType(ctx context.Context, expType, tenantID string, limit int) ([]*storage_models.Experience, error)

	// ListByAgent retrieves experiences for a specific agent.
	ListByAgent(ctx context.Context, agentID, tenantID string, limit int) ([]*storage_models.Experience, error)
}
