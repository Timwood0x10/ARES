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

	// HybridSearch performs vector recall (cosine) plus lexical (keyword) scoring
	// and returns ranked results. The caller supplies QueryVector (computed via
	// its EmbeddingService); when QueryVector is nil the search degrades to
	// lexical-only. Stores do NOT embed — they are storage-agnostic.
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]ScoredObject, error)

	// ListByStatus returns objects in namespace ns matching the given status.
	// Empty status matches objects with no status (backward compatibility).
	ListByStatus(ctx context.Context, ns string, status ObjectStatus, limit int) ([]*KnowledgeObject, error)

	// UpdateStatus transitions an object's lifecycle status.
	UpdateStatus(ctx context.Context, id string, status ObjectStatus) error

	// Promote moves a candidate to active and records its computed Quality.
	Promote(ctx context.Context, id string, q *Quality) error
}

// HybridSearchRequest configures a HybridSearch call.
type HybridSearchRequest struct {
	Query        string
	QueryVector  []float32 // computed by the caller via its EmbeddingService; nil = lexical-only
	Namespace    string
	Types        []ObjectType
	TopK         int // vector recall cap (default 20)
	FinalK       int // final result cap (default 5)
	MinScore     float64
	Model        string         // embedding model name (selects which Representation to compare)
	StatusFilter []ObjectStatus // default: only StatusActive (+ empty for back-compat)
}

// ScoredObject is a HybridSearch result with its component scores.
type ScoredObject struct {
	Object       *KnowledgeObject
	VectorScore  float64
	LexicalScore float64
	FinalScore   float64
}
