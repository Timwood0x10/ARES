// Package memory provides API abstractions for memory distillation operations.
package memory

import (
	"context"
	"time"
)

// MemoryType represents the type of distilled memory.
type MemoryType string

const (
	// MemoryKnowledge represents factual information and knowledge.
	MemoryKnowledge MemoryType = "knowledge"
	// MemoryPreference represents user preferences.
	MemoryPreference MemoryType = "preference"
	// MemoryInteraction represents interaction patterns and solutions.
	MemoryInteraction MemoryType = "interaction"
	// MemoryProfile represents user profile and behavioral patterns.
	MemoryProfile MemoryType = "profile"
)

// ExtractionMethod represents how an experience was extracted.
type ExtractionMethod string

const (
	// ExtractionDirect represents direct user-assistant pair extraction.
	ExtractionDirect ExtractionMethod = "direct"
	// ExtractionCrossTurn represents multi-turn conversation extraction.
	ExtractionCrossTurn ExtractionMethod = "cross-turn"
)

// ResolutionStrategy represents how to resolve memory conflicts.
type ResolutionStrategy string

const (
	// ReplaceOld replaces old memory with new.
	ReplaceOld ResolutionStrategy = "replace"
	// KeepBoth keeps both versions.
	KeepBoth ResolutionStrategy = "version"
	// Merge merges memories (future implementation).
	Merge ResolutionStrategy = "merge"
)

// Experience represents a problem-solution pair extracted from conversation.
type Experience struct {
	// Problem is the problem description.
	Problem string `json:"problem"`
	// Solution is the solution description.
	Solution string `json:"solution"`
	// Confidence is the confidence score.
	Confidence float64 `json:"confidence"`
	// ExtractionMethod is how the experience was extracted.
	ExtractionMethod ExtractionMethod `json:"extraction_method"`
}

// DistilledMemory represents a distilled memory from agent experience.
type DistilledMemory struct {
	// ID is the unique identifier.
	ID string `json:"id"`
	// Type is the memory type.
	Type MemoryType `json:"type"`
	// Content is the memory content.
	Content string `json:"content"`
	// Importance is the importance score.
	Importance float64 `json:"importance"`
	// Source is the source conversation ID.
	Source string `json:"source"`
	// TenantID is the tenant identifier.
	TenantID string `json:"tenant_id"`
	// UserID is the user identifier.
	UserID string `json:"user_id"`
	// CreatedAt is the creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is the expiration timestamp.
	ExpiresAt *time.Time `json:"expires_at"`
	// Metadata is additional metadata.
	Metadata map[string]interface{} `json:"metadata"`
}

// DistillationConfig holds configuration for the distillation process.
type DistillationConfig struct {
	// MinImportance is the minimum importance score.
	MinImportance float64 `json:"min_importance"`
	// ConflictThreshold is the similarity threshold for conflict detection.
	ConflictThreshold float64 `json:"conflict_threshold"`
	// MaxMemoriesPerDistillation is the maximum memories per distillation.
	MaxMemoriesPerDistillation int `json:"max_memories_per_distillation"`
	// MaxSolutionsPerTenant is the global cap on solution memories.
	MaxSolutionsPerTenant int `json:"max_solutions_per_tenant"`
	// EnableCodeFilter enables code block filtering.
	EnableCodeFilter bool `json:"enable_code_filter"`
	// EnableStacktraceFilter enables stacktrace filtering.
	EnableStacktraceFilter bool `json:"enable_stacktrace_filter"`
	// EnableLogFilter enables log filtering.
	EnableLogFilter bool `json:"enable_log_filter"`
	// EnableMarkdownTableFilter enables markdown table filtering.
	EnableMarkdownTableFilter bool `json:"enable_markdown_table_filter"`
	// EnableCrossTurnExtraction enables cross-turn conversation extraction.
	EnableCrossTurnExtraction bool `json:"enable_cross_turn_extraction"`
	// EnableLengthBonus enables length bonus in importance scoring.
	EnableLengthBonus bool `json:"enable_length_bonus"`
	// LengthThreshold is the threshold for length bonus.
	LengthThreshold int `json:"length_threshold"`
	// LengthBonus is the bonus value for length threshold.
	LengthBonus float64 `json:"length_bonus"`
	// TopNBeforeConflict enables top-N filtering before conflict detection.
	TopNBeforeConflict bool `json:"top_n_before_conflict"`
	// ConflictSearchLimit is the limit for vector search in conflict detection.
	ConflictSearchLimit int `json:"conflict_search_limit"`
	// PrecisionOverRecall prioritizes precision over recall.
	PrecisionOverRecall bool `json:"precision_over_recall"`
}

// DistillationMetrics holds metrics for the distillation process.
type DistillationMetrics struct {
	// AttemptTotal is the total number of distillation attempts.
	AttemptTotal int64 `json:"attempt_total"`
	// SuccessTotal is the total number of successful distillations.
	SuccessTotal int64 `json:"success_total"`
	// FilteredNoise is the number of memories filtered as noise.
	FilteredNoise int64 `json:"filtered_noise"`
	// FilteredSecurity is the number of memories filtered for security.
	FilteredSecurity int64 `json:"filtered_security"`
	// ConflictResolved is the number of conflicts resolved.
	ConflictResolved int64 `json:"conflict_resolved"`
	// MemoriesCreated is the total number of memories created.
	MemoriesCreated int64 `json:"memories_created"`
}

// ConversationMessage represents a message in a conversation.
type ConversationMessage struct {
	// Role is the message role (user/assistant/system).
	Role string `json:"role"`
	// Content is the message content.
	Content string `json:"content"`
}

// DistillationService provides memory distillation operations.
type DistillationService interface {
	// DistillConversation distills memories from a conversation.
	// Args:
	// ctx - operation context.
	// conversationID - unique identifier for the conversation.
	// messages - conversation messages.
	// tenantID - tenant ID for multi-tenancy.
	// userID - user ID for the conversation.
	// Returns distilled memories or error.
	DistillConversation(ctx context.Context, conversationID string, messages []ConversationMessage, tenantID, userID string) ([]*DistilledMemory, error)

	// GetMetrics returns the current distillation metrics.
	// Returns the metrics.
	GetMetrics() *DistillationMetrics

	// ResetMetrics resets the distillation metrics.
	ResetMetrics()

	// GetConfig returns the current distillation configuration.
	// Returns the configuration.
	GetConfig() *DistillationConfig

	// UpdateConfig updates the distillation configuration.
	// Args:
	// config - new configuration.
	// Returns error if update fails.
	UpdateConfig(config *DistillationConfig) error

	// UpdateMemory updates an existing distilled memory.
	// Args:
	// ctx - operation context.
	// memoryID - the memory ID to update.
	// updates - map of fields to update (content, importance, metadata, etc.).
	// Returns error if update fails.
	UpdateMemory(ctx context.Context, memoryID string, updates map[string]interface{}) error

	// DeleteMemory deletes a distilled memory.
	// Args:
	// ctx - operation context.
	// memoryID - the memory ID to delete.
	// Returns error if deletion fails.
	DeleteMemory(ctx context.Context, memoryID string) error

	// SearchMemories searches for memories by query text (using vector search).
	// Args:
	// ctx - operation context.
	// query - the search query.
	// tenantID - tenant ID for multi-tenancy.
	// limit - maximum number of results.
	// Returns matching memories or error.
	SearchMemories(ctx context.Context, query string, tenantID string, limit int) ([]*DistilledMemory, error)
}

// ExperienceRepository defines the interface for experience storage and retrieval.
type ExperienceRepository interface {
	// SearchByVector searches for similar experiences by vector.
	// Args:
	// ctx - operation context.
	// vector - the vector to search.
	// tenantID - tenant ID for multi-tenancy.
	// limit - maximum number of results.
	// Returns similar experiences or error.
	SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]*Experience, error)

	// GetByMemoryType retrieves experiences by memory type.
	// Args:
	// ctx - operation context.
	// tenantID - tenant ID for multi-tenancy.
	// memoryType - the memory type to filter by.
	// Returns experiences or error.
	GetByMemoryType(ctx context.Context, tenantID string, memoryType MemoryType) ([]*Experience, error)

	// Update updates an existing experience.
	// Args:
	// ctx - operation context.
	// experience - the experience to update.
	// Returns error if update fails.
	Update(ctx context.Context, experience *Experience) error

	// Delete deletes an experience by ID.
	// Args:
	// ctx - operation context.
	// id - the experience ID.
	// Returns error if deletion fails.
	Delete(ctx context.Context, id string) error

	// Create creates a new experience.
	// Args:
	// ctx - operation context.
	// experience - the experience to create.
	// Returns error if creation fails.
	Create(ctx context.Context, experience *Experience) error

	// GetInternalRepository returns the internal repository for advanced usage.
	// This is used to bridge the API and internal interfaces.
	GetInternalRepository() interface{}
}
