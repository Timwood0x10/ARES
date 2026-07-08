package knowledge

import "context"

// Query defines filter criteria for KnowledgeStore queries.
type Query struct {
	Types     []ObjectType `json:"types,omitempty"`
	Namespace string       `json:"namespace,omitempty"`
	Tags      []string     `json:"tags,omitempty"`
	Limit     int          `json:"limit,omitempty"`
	Offset    int          `json:"offset,omitempty"`
}

// KnowledgeStore is an optional persistence layer for KnowledgeObjects.
// It serves as Cache, Persistence, and History — not a required hop in the
// data path. Provider → Pipeline → KnowledgeRuntime bypasses Store entirely.
type KnowledgeStore interface {
	// Save persists one or more KnowledgeObjects. Creates or updates.
	Save(ctx context.Context, objects ...*KnowledgeObject) error

	// Get retrieves a KnowledgeObject by ID.
	// Returns ErrObjectNotFound if not found.
	Get(ctx context.Context, id string) (*KnowledgeObject, error)

	// Query retrieves KnowledgeObjects matching the given criteria.
	Query(ctx context.Context, q Query) ([]*KnowledgeObject, error)

	// Delete removes a KnowledgeObject by ID.
	Delete(ctx context.Context, id string) error

	// Search performs semantic search using the given embedding model.
	Search(ctx context.Context, text string, model string, limit int) ([]*KnowledgeObject, error)

	// SaveRepresentation stores an embedding vector.
	SaveRepresentation(ctx context.Context, rep *Representation) error

	// GetRepresentation retrieves an embedding vector by model.
	GetRepresentation(ctx context.Context, objectID string, model string) (*Representation, error)
}
