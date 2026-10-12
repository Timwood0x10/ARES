package knowledge

import (
	"errors"
	"fmt"
	"time"
)

// ObjectType identifies the type of a knowledge object.
type ObjectType string

const (
	ObjectMemory       ObjectType = "memory"
	ObjectUser         ObjectType = "user"
	ObjectProject      ObjectType = "project"
	ObjectCode         ObjectType = "code"
	ObjectIssue        ObjectType = "issue"
	ObjectCommit       ObjectType = "commit"
	ObjectDecision     ObjectType = "decision"
	ObjectDocument     ObjectType = "document"
	ObjectToolResult   ObjectType = "tool_result"
	ObjectWorkflow     ObjectType = "workflow"
	ObjectRuntime      ObjectType = "runtime"
	ObjectArchitecture ObjectType = "architecture"
)

// Evidence records the provenance of a KnowledgeObject, ensuring every piece
// of knowledge is traceable back to its source.
type Evidence struct {
	Source    string    `json:"source"`    // Source identifier, e.g. "postgres://orders/2024-01"
	Ref       string    `json:"ref"`       // Reference ID, e.g. row ID, commit hash
	Weight    float64   `json:"weight"`    // Confidence weight [0, 1]
	Timestamp time.Time `json:"timestamp"` // When the evidence was collected
	// Span narrows the evidence to a specific location within the source
	// document or data set. It is a POINTER so an unspecified span is ABSENT
	// on the wire: encoding/json's omitempty does not omit a zero struct, so a
	// value field would serialise "span":{} on every evidence item. Nil means
	// "unspecified", keeping the field additive — pre-0.3.4 evidence
	// deserialises with a nil Span and behaves exactly as before.
	Span *EvidenceRef `json:"span,omitempty"`
}

// EvidenceRef pinpoints where in a source the evidence was extracted from.
// All fields are optional (zero = unspecified) to keep the type additive.
type EvidenceRef struct {
	// StartLine is the 1-based line number where the evidence starts (0 = unspecified).
	StartLine int `json:"start_line,omitempty"`
	// EndLine is the 1-based line number where the evidence ends (0 = unspecified).
	EndLine int `json:"end_line,omitempty"`
	// StartOffset is the 0-based byte offset within the source (0 = unspecified, -1 = N/A).
	StartOffset int `json:"start_offset,omitempty"`
	// EndOffset is the 0-based byte offset where the evidence ends (0 = unspecified, -1 = N/A).
	EndOffset int `json:"end_offset,omitempty"`
	// Text is a short verbatim excerpt of the evidence span for human review.
	Text string `json:"text,omitempty"`
}

// KnowledgeObject is the universal knowledge representation.
//
// Three-layer data structure:
//   - Raw:        Original bytes from the source, preserved for re-distillation.
//   - Normalized: Cleaned, standardized text for embedding and matching.
//   - Summary:    LLM-friendly summary for token-efficient retrieval.
//
// Embeddings are stored externally via Representation to support multiple
// embedding models (OpenAI, BGE, Jina, etc.) without data migration.
type KnowledgeObject struct {
	ID        string     `json:"id"`
	Type      ObjectType `json:"type"`
	Namespace string     `json:"namespace,omitempty"`

	// Three-layer data.
	Raw        []byte `json:"raw,omitempty"`
	Normalized string `json:"normalized,omitempty"`
	Summary    string `json:"summary"`

	Metadata   map[string]any `json:"metadata,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence"`
	Version    int64          `json:"version"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Evidence   []Evidence     `json:"evidence,omitempty"`

	// Representations maps model name → representation ID for external embeddings.
	// Example: {"openai-text-3-large": "rep_abc123", "bge-m3": "rep_def456"}
	Representations map[string]string `json:"representations,omitempty"`

	// ===== 0.2.9 fields =====
	// Status controls the fact lifecycle: candidate → active → superseded → rejected.
	// Empty status is treated as active for backward compatibility with pre-0.2.9 data.
	Status ObjectStatus `json:"status,omitempty"`
	// Quality holds the multi-dimensional quality scores used for recall ranking.
	Quality *Quality `json:"quality,omitempty"`
	// Relations are explicit outgoing relations extracted by rules (not LLM).
	Relations []Relation `json:"relations,omitempty"`
	// EmbeddingModel records the model used for the current vector, for migration.
	EmbeddingModel string `json:"embedding_model,omitempty"`
	// Relevance is the query-time relevance of this object to the retrieval
	// intent, in [0, 1]. It is set by providers at stream time and by the
	// store HybridSearch path from FinalScore. Relevance is distinct from
	// Confidence: Confidence is the stored reliability of a fact, Relevance
	// is how well it matches the current query. The retriever ranks and
	// filters on Relevance, NOT Confidence. Relevance is transient: it is
	// not a persisted property, is recomputed per query, and is excluded
	// from serialization (json:"-") so a streamed score can never leak into
	// stored state or API responses.
	Relevance float64 `json:"-"`

	// ===== 0.3.4 fields (additive; zero value == pre-0.3.4 behaviour) =====
	// SchemaVersion records the KnowledgeObject schema revision that produced
	// this record so future migrations can detect and upgrade older shapes.
	// Zero means "pre-0.3.4, unversioned" and is treated as legacy on read:
	// no temporal metadata, always valid, observed-at falls back to the
	// stored timestamps (see EffectiveObservedAt / IsValidAt).
	SchemaVersion int `json:"schema_version,omitempty"`
	// ValidFrom and ValidUntil bound the wall-clock interval during which this
	// fact is considered current. A nil ValidFrom means "valid since creation"
	// (no lower bound); a nil ValidUntil means "no expiry" (no upper bound).
	// They let recall weight by validity WITHOUT deleting superseded facts —
	// e.g. a user's changed preference: the superseded fact gets a ValidUntil,
	// the replacement a later ValidFrom, and both stay traceable.
	//
	// These are POINTERS, not time.Time: encoding/json's omitempty does not
	// omit a zero struct, so a value field would serialise an unset bound as
	// the 0001-01-01 sentinel on every object. A nil pointer is absent on the
	// wire, keeping "unset" distinguishable from "the epoch" for external
	// consumers (export/import, other services) — matching the SQL columns,
	// which store NULL (postgres) / '' (sqlite) for unset.
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
	// ObservedAt is when the underlying fact was actually observed at the
	// source, as distinct from CreatedAt (when AKG first stored it). Nil (or a
	// zero time) means "unknown"; callers fall back to UpdatedAt then
	// CreatedAt via EffectiveObservedAt. Used to resolve preference drift by
	// recency.
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	// SourceVersion is an opaque source-side revision marker (row version,
	// document etag, commit hash, ...) enabling staleness detection on
	// incremental re-ingest. Empty means "unversioned source".
	SourceVersion string `json:"source_version,omitempty"`
}

// CurrentSchemaVersion is the KnowledgeObject schema revision stamped on
// records written by this build. A zero SchemaVersion on a decoded object
// denotes a pre-0.3.4 record written before versioning existed; readers must
// treat 0 as legacy (no temporal metadata) and apply pre-0.3.4 behaviour.
const CurrentSchemaVersion = 1

// EffectiveObservedAt returns the best available observation time: ObservedAt
// when set, otherwise UpdatedAt, otherwise CreatedAt. For any stored object
// this is non-zero because the write path always stamps CreatedAt.
func (o *KnowledgeObject) EffectiveObservedAt() time.Time {
	if o.ObservedAt != nil && !o.ObservedAt.IsZero() {
		return *o.ObservedAt
	}
	if !o.UpdatedAt.IsZero() {
		return o.UpdatedAt
	}
	return o.CreatedAt
}

// IsValidAt reports whether the fact is temporally valid at instant t.
// ValidFrom is an inclusive lower bound and ValidUntil an exclusive upper
// bound. A nil (or zero) ValidFrom imposes no lower bound and a nil (or zero)
// ValidUntil imposes no upper bound, so a legacy object with neither set is
// always valid — preserving pre-0.3.4 behaviour.
func (o *KnowledgeObject) IsValidAt(t time.Time) bool {
	if o.ValidFrom != nil && !o.ValidFrom.IsZero() && t.Before(*o.ValidFrom) {
		return false
	}
	if o.ValidUntil != nil && !o.ValidUntil.IsZero() && !t.Before(*o.ValidUntil) {
		return false
	}
	return true
}

// StampWriteSchema marks each object as written by this build by setting
// SchemaVersion to CurrentSchemaVersion when it is still zero. Objects that
// already carry a version are left untouched so a replayed or imported record
// keeps its original provenance. Nil entries are skipped. Call it on the write
// path just before persisting so stored records are self-describing and future
// migrations can detect the shape they were written in.
func StampWriteSchema(objects ...*KnowledgeObject) {
	for _, o := range objects {
		if o != nil && o.SchemaVersion == 0 {
			o.SchemaVersion = CurrentSchemaVersion
		}
	}
}

// ErrSchemaVersionTooNew marks a record written by a build newer than this one.
// MigrateObject wraps it so callers can use errors.Is to tell a genuinely
// unreadable record apart from other migration failures: single reads (Get)
// should fail closed, while list/search paths may skip just that row and keep
// serving the rest of the corpus.
var ErrSchemaVersionTooNew = errors.New("knowledge: schema version newer than supported")

// MigrateObject upgrades a decoded KnowledgeObject in place to
// CurrentSchemaVersion, applying one migration step per schema revision in
// order. A zero SchemaVersion denotes a pre-0.3.4 (unversioned) record: its
// additive fields already default to pre-0.3.4 behaviour, so the only upgrade
// is to stamp the version. Objects already at CurrentSchemaVersion are left
// untouched. A version NEWER than this build understands is refused with an
// error wrapping ErrSchemaVersionTooNew so a forward-dated record from a future
// build is never silently misread. Call it on the read path after decoding a
// stored or imported record.
//
// It MUTATES o in place (the version stamp is written back onto the decoded
// object). That is deliberate — reads normalise the record they hand out — but
// callers should not treat decoding as a pure function.
func MigrateObject(o *KnowledgeObject) error {
	if o == nil {
		return nil
	}
	if o.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("knowledge: object %q schema version %d is newer than supported %d: %w",
			o.ID, o.SchemaVersion, CurrentSchemaVersion, ErrSchemaVersionTooNew)
	}
	for o.SchemaVersion < CurrentSchemaVersion {
		switch o.SchemaVersion {
		case 0:
			// 0 → 1: the 0.3.4 temporal fields are additive and their zero
			// values already encode pre-0.3.4 behaviour (always valid,
			// observed-at falls back to the stored timestamps), so this step
			// only records the version.
			o.SchemaVersion = 1
		default:
			// An unrecognised revision (e.g. a corrupted negative version) is
			// deliberately NOT wrapped in ErrSchemaVersionTooNew: list/search
			// callers skip "newer" records on purpose, but an unknown version
			// is a data-integrity error that should surface rather than be
			// silently dropped. Only a record from a future build gets the
			// skippable sentinel.
			return fmt.Errorf("knowledge: no migration step from object %q schema version %d",
				o.ID, o.SchemaVersion)
		}
	}
	return nil
}

// ObjectStatus is the lifecycle state of a KnowledgeObject.
type ObjectStatus string

const (
	StatusCandidate  ObjectStatus = "candidate"  // written, not yet verified
	StatusActive     ObjectStatus = "active"     // passed the quality gate
	StatusSuperseded ObjectStatus = "superseded" // replaced by a newer fact
	StatusRejected   ObjectStatus = "rejected"   // conflicting or low quality
)

// Quality holds multi-dimensional quality scores, each in [0, 1].
type Quality struct {
	ExtractionScore  float64 `json:"extraction_score"`
	ConsistencyScore float64 `json:"consistency_score"`
	FreshnessScore   float64 `json:"freshness_score"`
	UsageScore       float64 `json:"usage_score"`
	ManualVerified   bool    `json:"manual_verified"`
}

// AllowedPredicates restricts the relation predicate vocabulary so rule-based
// extraction (and any future LLM helper) cannot mint arbitrary relation types.
// Predicates that have a Rel* constant (see relation.go) use it; the remainder
// are string literals because no constant is defined for them yet.
var AllowedPredicates = map[string]bool{
	RelDependsOn: true, RelCalls: true, "produces": true, "consumes": true,
	RelFixes: true, RelCauses: true, RelBelongsTo: true, "derived_from": true,
	RelSimilarTo: true, "contradicts": true, RelSupersedes: true, "related_to": true,
}

// Representation stores an embedding vector for a KnowledgeObject.
// Stored separately from KnowledgeObject to support multiple embedding models
// (OpenAI 1536d, BGE 1024d, Jina 768d, etc.) without data migration.
type Representation struct {
	ID        string            `json:"id"`
	ObjectID  string            `json:"object_id"`
	Model     string            `json:"model"` // e.g. "openai-text-3-large", "bge-m3"
	Dimension int               `json:"dimension"`
	Vector    []float32         `json:"vector"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}
