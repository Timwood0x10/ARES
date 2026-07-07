// Package knowledge provides core types for the ARES Knowledge Fabric (AKF).
//
// KnowledgeObject is the universal knowledge representation. Every external
// data source (PostgreSQL, MySQL, Git, Memory, Code, etc.) is converted into
// KnowledgeObject via GraphProvider. The three-layer structure (Raw → Normalized
// → Summary) preserves original data while optimizing for LLM consumption.
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
