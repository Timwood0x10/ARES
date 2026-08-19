package knowledge

import "time"

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
