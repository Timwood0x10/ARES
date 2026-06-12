// package storage defines the vector storage interface.
// This is the contract that all vector backends must implement.
// PostgreSQL (pgvector), Qdrant, Milvus, SQLite-vec, or in-memory — all plug in here.
package storage

import "context"

// VectorStore defines the interface for vector similarity search.
// Implement this interface to add a new vector backend.
type VectorStore interface {
	// Search performs vector similarity search and returns results ordered by distance.
	//
	// Args:
	//   ctx - timeout and cancellation context.
	//   table - the collection/table to search in.
	//   embedding - the query vector.
	//   limit - max number of results.
	//
	// Returns:
	//   results - ordered by similarity (highest first).
	//   err - ErrRecordNotFound if no results, or backend error.
	Search(ctx context.Context, table string, embedding []float64, limit int) ([]*SearchResult, error)

	// AddEmbedding stores a vector with associated metadata.
	//
	// Args:
	//   ctx - timeout and cancellation context.
	//   table - the collection/table to store in.
	//   id - unique identifier for the vector.
	//   embedding - the vector data.
	//   metadata - associated metadata (stored alongside the vector).
	AddEmbedding(ctx context.Context, table, id string, embedding []float64, metadata map[string]any) error

	// CreateCollection creates a vector collection/table if it doesn't exist.
	//
	// Args:
	//   ctx - timeout and cancellation context.
	//   name - collection/table name.
	//   dimension - vector dimension (e.g., 1024, 1536).
	CreateCollection(ctx context.Context, name string, dimension int) error
}

// SearchResult represents a single vector search result.
type SearchResult struct {
	ID       string         `json:"id"`
	Score    float64        `json:"score"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
