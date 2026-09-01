// Package models defines data structures for the storage system.
package models

import "time"

// Experience represents a distilled task experience.
// This is the core data structure for experience storage and retrieval.
// The structure follows the principle: Experience = Distilled Knowledge.
type Experience struct {
	// ID is the unique identifier of the experience.
	ID string `json:"id"`

	// TenantID is the tenant identifier for multi-tenancy isolation.
	TenantID string `json:"tenant_id"`

	// Type is the type of experience.
	// Valid values: "success" or "failure".
	Type string `json:"type"`

	// Problem is the abstract problem statement.
	// This is the target for embedding generation.
	// NOTE: This field is stored in the 'input' column for backward compatibility.
	Problem string `json:"problem"`

	// Solution is the concise solution approach.
	// NOTE: This field is stored in the 'output' column for backward compatibility.
	Solution string `json:"solution"`

	// Input is the raw input text (stored in database 'input' column).
	// This is for backward compatibility with the database schema.
	Input string `json:"-"`

	// Output is the raw output text (stored in database 'output' column).
	// This is for backward compatibility with the database schema.
	Output string `json:"-"`

	// Constraints are important constraints or context for the solution.
	// NOTE: This field is stored in metadata['constraints'] for backward compatibility.
	Constraints string `json:"constraints"`

	// Embedding is the vector embedding of the problem.
	// This is used for semantic similarity search.
	Embedding []float64 `json:"embedding"`

	// EmbeddingModel is the name of the embedding model used.
	EmbeddingModel string `json:"embedding_model"`

	// EmbeddingVersion is the version of the embedding model.
	EmbeddingVersion int `json:"embedding_version"`

	// Score is the overall score of the experience.
	Score float64 `json:"score"`

	// Success indicates whether the original task was successful.
	Success bool `json:"success"`

	// AgentID is the identifier of the agent that generated this experience.
	AgentID string `json:"agent_id"`

	// UsageCount is the number of times this experience was successfully used.
	// This is used as a reinforcement signal.
	// NOTE: This field is stored in metadata['usage_count'] for backward compatibility.
	UsageCount int `json:"usage_count"`

	// Metadata is additional metadata for the experience.
	// NOTE: For backward compatibility, constraints and usage_count are also stored here.
	Metadata map[string]interface{} `json:"metadata"`

	// DecayAt is the time when this experience should be considered expired.
	// If zero, the experience never expires.
	DecayAt time.Time `json:"decay_at"`

	// CreatedAt is the time when this experience was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the time when this experience was last updated.
	UpdatedAt time.Time `json:"updated_at"`
}

// ExperiencesTable is the physical table distilled experiences live in. The
// `_1024` suffix encodes the embedding dimension of its `embedding VECTOR(1024)`
// column, so a dimension change means a new table plus a migration. Kept here,
// next to the model, so the storage layer is the single source of truth and
// domain packages do not have to re-declare the literal.
const ExperiencesTable = "experiences_1024"

// TableName returns the table name for this model.
func (e *Experience) TableName() string {
	return ExperiencesTable
}

// ExperienceType constants.
const (
	// ExperienceTypeSuccess represents a successful experience.
	ExperienceTypeSuccess = "success"

	// ExperienceTypeFailure represents a failed experience.
	ExperienceTypeFailure = "failure"

	// ExperienceTypeQuery represents a query-based experience (for backward compatibility).
	ExperienceTypeQuery = "query"

	// ExperienceTypeSolution represents a solution-based experience (for backward compatibility).
	ExperienceTypeSolution = "solution"

	// ExperienceTypePattern represents a pattern-based experience (for backward compatibility).
	ExperienceTypePattern = "pattern"

	// ExperienceTypeDistilled represents a distilled experience (for backward compatibility).
	ExperienceTypeDistilled = "distilled"
)

// IsExpired checks if the experience has decayed and should be excluded from search.
func (e *Experience) IsExpired() bool {
	return !e.DecayAt.IsZero() && time.Now().After(e.DecayAt)
}

// GetConstraints retrieves constraints from metadata.
// This provides backward compatibility for the new structure.
func (e *Experience) GetConstraints() string {
	// Check metadata first for backward compatibility
	if e.Metadata != nil {
		if c, ok := e.Metadata["constraints"].(string); ok && c != "" {
			return c
		}
	}
	// Fall back to Constraints field
	return e.Constraints
}

// GetUsageCount retrieves usage count from metadata.
// This provides backward compatibility for the new structure.
func (e *Experience) GetUsageCount() int {
	// Check metadata first for backward compatibility
	if e.Metadata != nil {
		if c, ok := e.Metadata["usage_count"].(float64); ok {
			return int(c)
		}
	}
	// Fall back to UsageCount field
	return e.UsageCount
}
