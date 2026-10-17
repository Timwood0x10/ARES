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

	// Weights overrides the hybrid scoring weights; nil means DefaultHybridWeights.
	// It is a request field rather than package state so the choice is the caller's
	// per-query decision (no global singleton), and because enabling the recency
	// term is a retrieval-quality question that has to be measured per corpus.
	Weights *HybridWeights
}

// ScoredObject is a HybridSearch result with its component scores.
//
// The components are reported so a caller can tell WHY a result ranked where it did
// (and so a rerank upstream does not have to recompute them). FinalScore is the blend
// of the terms that were available for this candidate, under the weights the request
// asked for.
type ScoredObject struct {
	Object       *KnowledgeObject
	VectorScore  float64
	LexicalScore float64
	// RecencyScore is the object's freshness (see FreshnessScore). It is reported
	// even when its weight is zero: a reader comparing two results should be able to
	// see the tie-breaker a different configuration would have applied.
	RecencyScore float64
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
	// Representations are the embedding vectors belonging to the exported
	// objects. They are carried because a snapshot without them is a LOSSY
	// backup: the facts return but their vectors do not, so recall silently
	// degrades to lexical-only and nothing says so. Populated when the store
	// implements RepresentationLister; empty means the store could not
	// enumerate them or holds none (ExportStore documents which).
	Representations []*Representation `json:"representations,omitempty"`
}

// TODO(tech-debt): ExportStore / ImportStore / StoreExporter now have an
// operator caller (`ares knowledge export|import`), and the snapshot carries
// Representations whenever the store can enumerate them — closing the silent
// recall degradation a round trip used to cause. One gap remains:
// StoreSnapshot.SchemaVersion is written but never validated on import (the
// real guard is the per-object MigrateObject).
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

// NamespaceLister is an optional capability of a KnowledgeStore: the ability to
// enumerate the namespaces it holds. Stores that do not implement it are still
// valid KnowledgeStores — one that cannot (or deliberately will not) enumerate
// stays inside the frozen contract.
//
// It exists because every namespace-scoped sweep needs the same answer first. A
// namespace-scoped store deliberately refuses an unscoped operation (an empty
// namespace would drop the predicate and cross tenants — and, under M5, users),
// so a pass over "everything" must learn which namespaces exist instead of
// scanning blind. The maintenance worker already documents this shape: "When
// maintenance goes multi-tenant, iterate tenants here instead of binding one."
type NamespaceLister interface {
	// ListNamespaces returns the distinct non-empty namespaces held by the
	// store. Implementations must return a deterministic order so a sweep is
	// reproducible.
	ListNamespaces(ctx context.Context) ([]string, error)
}

// ErrNamespaceListingNotSupported is returned by ListNamespaces when the store
// does not implement NamespaceLister. Callers can use errors.Is to tell "this
// backend cannot enumerate" apart from "this backend holds no namespaces".
var ErrNamespaceListingNotSupported = errors.New(
	"namespace listing not supported by this knowledge store")

// ListNamespaces enumerates the namespaces held by a KnowledgeStore. Callers
// that must not act when enumeration is unavailable should check
// errors.Is(err, ErrNamespaceListingNotSupported) rather than treating the
// absence of the capability as a fatal error.
func ListNamespaces(ctx context.Context, store KnowledgeStore) ([]string, error) {
	if lister, ok := store.(NamespaceLister); ok {
		return lister.ListNamespaces(ctx)
	}
	return nil, ErrNamespaceListingNotSupported
}

// RepresentationLister is an optional capability of a KnowledgeStore: the
// ability to enumerate the embedding vectors it holds for one namespace. Like
// NamespaceLister it is deliberately outside the frozen KnowledgeStore contract.
//
// It exists because a snapshot without vectors is a lossy backup: export/import
// would return the facts and silently drop their embeddings, degrading recall to
// lexical-only with nothing to observe. The namespace argument is required — a
// representation is reachable only through its owning object, so enumeration is
// scoped through that object rather than scanned across tenants.
type RepresentationLister interface {
	// ListRepresentations returns every representation whose owning object
	// lives in namespace, in a deterministic order so a snapshot is
	// reproducible. An empty namespace is a caller error (fail closed, matching
	// ApplyDecay and ListByStatus).
	ListRepresentations(ctx context.Context, namespace string) ([]*Representation, error)
}

// ErrRepresentationListingNotSupported is returned when a store does not
// implement RepresentationLister. Callers can use errors.Is to tell "this
// backend cannot enumerate vectors" apart from "this namespace has none" — the
// difference between a degraded backup and an empty corpus.
var ErrRepresentationListingNotSupported = errors.New(
	"representation listing not supported by this knowledge store")

// ListRepresentations enumerates the vectors a store holds for one namespace.
func ListRepresentations(ctx context.Context, store KnowledgeStore, namespace string) ([]*Representation, error) {
	if lister, ok := store.(RepresentationLister); ok {
		return lister.ListRepresentations(ctx, namespace)
	}
	return nil, ErrRepresentationListingNotSupported
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
	representations, err := exportRepresentations(ctx, store, namespace)
	if err != nil {
		return nil, err
	}
	return &StoreSnapshot{
		SchemaVersion:   CurrentSchemaVersion,
		ExportedAt:      time.Now(),
		Namespace:       namespace,
		Objects:         objects,
		Representations: representations,
	}, nil
}

// exportRepresentations collects the vectors belonging to the exported objects.
//
// It returns (nil, nil) when the store cannot enumerate them: an object-only
// snapshot is still a valid artefact, but the operator must be told the round
// trip will be lossy — which is why the capability is probed rather than
// assumed, and why the absence is not an error.
func exportRepresentations(ctx context.Context, store KnowledgeStore, namespace string) ([]*Representation, error) {
	lister, ok := store.(RepresentationLister)
	if !ok {
		return nil, nil
	}
	if namespace != "" {
		reps, err := lister.ListRepresentations(ctx, namespace)
		if err != nil {
			return nil, fmt.Errorf("export store: list representations: %w", err)
		}
		return reps, nil
	}
	// All namespaces: representation enumeration is namespace-scoped (a vector is
	// reachable only through its owning object), so the namespaces come first —
	// a second optional capability.
	nl, ok := store.(NamespaceLister)
	if !ok {
		return nil, nil
	}
	namespaces, err := nl.ListNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("export store: list namespaces: %w", err)
	}
	var all []*Representation
	for _, ns := range namespaces {
		reps, rErr := lister.ListRepresentations(ctx, ns)
		if rErr != nil {
			return nil, fmt.Errorf("export store: list representations for %q: %w", ns, rErr)
		}
		all = append(all, reps...)
	}
	return all, nil
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
	// A snapshot from a NEWER build is refused. Its objects may carry fields or shapes this
	// build would silently drop, and the fallback path would then re-stamp them with an
	// OLDER schema version — a downgrade that looks like a successful import and is only
	// noticed when the missing data matters.
	//
	// Older snapshots (including the legacy zero version) are ACCEPTED: per-object
	// MigrateObject upgrades them on the way in, which is the supported direction.
	//
	// This check sits BEFORE the StoreExporter fast path on purpose: a store that implements
	// its own Import must not become the one route that skips the guard.
	if snapshot.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("import store: snapshot schema version %d is newer than this build's %d: %w",
			snapshot.SchemaVersion, CurrentSchemaVersion, ErrSchemaVersionTooNew)
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
	// Restore the vectors AFTER the objects: a representation references its
	// owning object (the Postgres FK enforces it), so saving them first would
	// fail or orphan them. Skipping this step is what made a round trip return
	// the facts without their embeddings and silently degrade recall to lexical.
	for _, rep := range snapshot.Representations {
		if rep == nil {
			continue
		}
		if err := store.SaveRepresentation(ctx, rep); err != nil {
			return fmt.Errorf("import store: save representation %q: %w", rep.ID, err)
		}
	}
	return nil
}
