// Package repositories provides data access interfaces and implementations.
package repositories

import (
	"context"

	storage_models "goagentx/internal/storage/postgres/models"
)

// ExperienceRepositoryInterface defines the interface for experience data access.
type ExperienceRepositoryInterface interface {
	// Create inserts a new experience into the database.
	Create(ctx context.Context, exp *storage_models.Experience) error

	// GetByID retrieves an experience by ID.
	GetByID(ctx context.Context, id string) (*storage_models.Experience, error)

	// Update updates an existing experience.
	Update(ctx context.Context, exp *storage_models.Experience) error

	// Delete removes an experience by its ID.
	Delete(ctx context.Context, id string) error

	// SearchByVector performs vector similarity search for experiences.
	SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*storage_models.Experience, error)

	// SearchByKeyword performs keyword-based search for experiences.
	SearchByKeyword(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.Experience, error)

	// IncrementUsageCount increments the usage count of an experience.
	IncrementUsageCount(ctx context.Context, id string) error

	// ListByType retrieves experiences by type.
	ListByType(ctx context.Context, expType, tenantID string, limit int) ([]*storage_models.Experience, error)

	// ListByAgent retrieves experiences for a specific agent.
	ListByAgent(ctx context.Context, agentID, tenantID string, limit int) ([]*storage_models.Experience, error)
}
