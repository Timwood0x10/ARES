// Package repositories provides data access interfaces and implementations.
package repositories

import (
	"context"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// KnowledgeRepositoryInterface defines the interface for knowledge base data access.
type KnowledgeRepositoryInterface interface {
	// GetByID retrieves a knowledge chunk by ID.
	GetByID(ctx context.Context, id string) (*storage_models.KnowledgeChunk, error)

	// Update updates an existing knowledge chunk.
	Update(ctx context.Context, chunk *storage_models.KnowledgeChunk) error
}
