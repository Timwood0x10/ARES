// Package repositories provides data access interfaces and implementations.
package repositories

import (
	"context"
)

// DistilledMemoryRepositoryInterface defines the interface for distilled memory data access.
type DistilledMemoryRepositoryInterface interface {
	// Create creates a new distilled memory.
	Create(ctx context.Context, memory *DistilledMemory) error

	// SearchByVector searches for memories by vector similarity.
	SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*DistilledMemory, error)

	// GetByUserID retrieves memories for a specific user.
	GetByUserID(ctx context.Context, tenantID, userID string, limit int) ([]*DistilledMemory, error)

	// UpdateAccessCount updates the access count for a memory.
	UpdateAccessCount(ctx context.Context, id, tenantID string) error

	// DeleteExpired deletes expired memories and returns the count.
	DeleteExpired(ctx context.Context) (int64, error)
}
