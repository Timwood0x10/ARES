package knowledge

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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
	// An upsert whose ID already exists under a DIFFERENT namespace is refused
	// with ErrObjectNotFound and leaves the stored row untouched: objects are
	// keyed by ID alone, so without this guard a knowledge_update carrying
	// another tenant's ID would silently migrate that row into the caller's
	// namespace.
	Save(ctx context.Context, objects ...*KnowledgeObject) error

	// Get retrieves a KnowledgeObject by ID, scoped to the owning tenant.
	// Returns ErrObjectNotFound when no row with that ID exists IN THAT
	// TENANT — deliberately the same error as "no such ID", so a caller
	// cannot probe for another tenant's object IDs.
	Get(ctx context.Context, tenantID, id string) (*KnowledgeObject, error)

	// Query retrieves KnowledgeObjects matching the given criteria.
	Query(ctx context.Context, q Query) ([]*KnowledgeObject, error)

	// Delete removes a KnowledgeObject by ID, scoped to the owning tenant.
	// A row belonging to another tenant is reported as ErrObjectNotFound and
	// left untouched; a missing ID answers identically, so a caller cannot
	// enumerate which IDs exist under other tenants.
	Delete(ctx context.Context, tenantID, id string) error

	// Search performs semantic search using the given embedding model,
	// scoped to the owning tenant. Backends without vectors degrade to
	// lexical matching but must still apply the tenant scope.
	Search(ctx context.Context, tenantID, text string, model string, limit int) ([]*KnowledgeObject, error)

	// SaveRepresentation stores an embedding vector.
	SaveRepresentation(ctx context.Context, rep *Representation) error

	// GetRepresentation retrieves an embedding vector by model, scoped to
	// the owning object's tenant: a representation whose object belongs to
	// another tenant is reported as ErrObjectNotFound (indistinguishable
	// from a missing one, same anti-probing contract as Get).
	GetRepresentation(ctx context.Context, tenantID, objectID, model string) (*Representation, error)

	// HybridSearch performs vector recall (cosine) plus lexical (keyword) scoring
	// and returns ranked results. The caller supplies QueryVector (computed via
	// its EmbeddingService); when QueryVector is nil the search degrades to
	// lexical-only. Stores do NOT embed — they are storage-agnostic.
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]ScoredObject, error)

	// ListByStatus returns objects in namespace ns matching the given status.
	// Empty status matches objects with no status (backward compatibility).
	ListByStatus(ctx context.Context, ns string, status ObjectStatus, limit int) ([]*KnowledgeObject, error)

	// UpdateStatus transitions an object's lifecycle status, scoped to the
	// owning tenant. A row in another namespace is reported as ErrObjectNotFound
	// and left untouched.
	UpdateStatus(ctx context.Context, tenantID, id string, status ObjectStatus) error

	// Promote moves a candidate to active and records its computed Quality,
	// scoped to the owning tenant. A row in another namespace is reported as
	// ErrObjectNotFound and left untouched.
	Promote(ctx context.Context, tenantID, id string, q *Quality) error
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

// StoreSnapshot is a portable, self-describing export of a KnowledgeStore
// slice. It carries the schema version and the objects so an import on a
// different backend or a fresh instance can reconstruct the corpus without
// re-ingesting from source. The format is deliberately simple JSON: every
// KnowledgeObject already serialises to JSON, and wrapping in a versioned
// envelope makes future migrations detectable on read.
type StoreSnapshot struct {
	// SchemaVersion is the KnowledgeObject schema revision at export time.
	SchemaVersion int `json:"schema_version"`
	// ExportedAt is the wall-clock time the snapshot was taken.
	ExportedAt time.Time `json:"exported_at"`
	// Namespace is the namespace filter used for the export (empty = all).
	Namespace string `json:"namespace,omitempty"`
	// Objects is the exported knowledge objects.
	Objects []*KnowledgeObject `json:"objects"`
}

// TODO(tech-debt): ExportStore / ImportStore / StoreExporter have no
// production caller yet. Two gaps to close before real backup/migration use:
//   - the snapshot carries no Representations, so a round-trip silently drops
//     embeddings and recall degrades to lexical (the round-trip test only
//     checks objects);
//   - StoreSnapshot.SchemaVersion is written but never validated on import
//     (the real guard is the per-object MigrateObject).
//
// StoreExporter is an optional capability of a KnowledgeStore: the ability
// to export all objects in a namespace (or all namespaces) as a portable
// snapshot, and to import a snapshot back. Stores that do not implement
// this interface are still valid KnowledgeStores — export/import is an
// add-on for backup, migration, and GA reproducibility (M3).
//
// Export streams all objects matching the given namespace (empty = all
// namespaces) through the snapshot. Import persists the snapshot's objects
// via the store's existing Save path so all write-side guards (namespace
// isolation, schema stamping, quality gate) apply — import does NOT bypass
// the write contract.
type StoreExporter interface {
	Export(ctx context.Context, namespace string) (*StoreSnapshot, error)
	Import(ctx context.Context, snapshot *StoreSnapshot) error
}

// ExportStore creates a portable snapshot from a KnowledgeStore using the
// store's Query method. It does not require the store to implement
// StoreExporter; it falls back to Query when the store does not. The
// snapshot is stamped with the current schema version so future migrations
// can detect the shape it was written in.
func ExportStore(ctx context.Context, store KnowledgeStore, namespace string) (*StoreSnapshot, error) {
	// Fast path: store implements StoreExporter.
	if se, ok := store.(StoreExporter); ok {
		return se.Export(ctx, namespace)
	}
	// Fallback: use Query.
	q := Query{Limit: 0}
	if namespace != "" {
		q.Namespace = namespace
	}
	objects, err := store.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("export store: query: %w", err)
	}
	return &StoreSnapshot{
		SchemaVersion: CurrentSchemaVersion,
		ExportedAt:    time.Now(),
		Namespace:     namespace,
		Objects:       objects,
	}, nil
}

// ImportStore persists a snapshot's objects into a KnowledgeStore via the
// store's existing Save path. It stamps each object with the current schema
// version (MigrateObject upgrades legacy records on read) and deduplicates
// by object ID — re-importing the same snapshot is idempotent. The import
// does NOT bypass write-side guards: namespace isolation, schema stamping,
// and the quality gate all apply through Save.
func ImportStore(ctx context.Context, store KnowledgeStore, snapshot *StoreSnapshot) error {
	if snapshot == nil {
		return errors.New("import store: snapshot is nil")
	}
	// Fast path: store implements StoreExporter.
	if se, ok := store.(StoreExporter); ok {
		return se.Import(ctx, snapshot)
	}
	// Fallback: use Save.
	objects := snapshot.Objects
	// Migrate each object to the current schema so Save receives
	// well-formed records.
	for _, obj := range objects {
		if err := MigrateObject(obj); err != nil {
			return fmt.Errorf("import store: migrate object %q: %w", obj.ID, err)
		}
	}
	StampWriteSchema(objects...)
	if err := store.Save(ctx, objects...); err != nil {
		return fmt.Errorf("import store: save: %w", err)
	}
	return nil
}
