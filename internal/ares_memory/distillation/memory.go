// Package distillation provides memory distillation functionality for agent experience extraction.
package distillation

import (
	"context"
	"time"
)

// MemoryType defines the four types of memory.
type MemoryType string

const (
	MemoryKnowledge   MemoryType = "knowledge"
	MemoryPreference  MemoryType = "preference"
	MemoryInteraction MemoryType = "interaction"
	MemoryProfile     MemoryType = "profile"
)

// Memory represents a distilled memory from agent experience.
type Memory struct {
	ID         string
	Type       MemoryType
	Content    string
	Importance float64
	Source     string
	Vector     []float64
	TTL        time.Duration
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Metadata   map[string]interface{}
}

// ExtractionMethod defines how an experience was extracted.
type ExtractionMethod string

const (
	ExtractionDirect    ExtractionMethod = "direct"     // Direct user-assistant pair
	ExtractionCrossTurn ExtractionMethod = "cross-turn" // Multi-turn conversation
)

// Experience represents a problem-solution pair extracted from conversation.
type Experience struct {
	Problem          string
	Solution         string
	Confidence       float64
	ExtractionMethod ExtractionMethod
	Vector           []float64
}

// ResolutionStrategy defines how to resolve memory conflicts.
type ResolutionStrategy string

const (
	ReplaceOld ResolutionStrategy = "replace" // Replace old memory with new
	KeepBoth   ResolutionStrategy = "version" // Keep both versions (for solutions)
	Merge      ResolutionStrategy = "merge"   // Merge memories (future)
)

// ExperienceRepository defines the interface for experience storage and retrieval.
type ExperienceRepository interface {
	// SearchByVector searches for similar experiences by vector.
	SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]Experience, error)

	// GetByMemoryType retrieves experiences by memory type.
	GetByMemoryType(ctx context.Context, tenantID string, memoryType MemoryType) ([]Experience, error)

	// Update updates an existing experience.
	Update(ctx context.Context, experience *Experience) error

	// Delete deletes an experience by ID.
	Delete(ctx context.Context, id string) error

	// Create creates a new experience.
	Create(ctx context.Context, experience *Experience) error
}

// ExperienceStore defines the interface for writing experiences to the experience store.
// This is used by the distiller to sync distilled memories to the experience system.
type ExperienceStore interface {
	// Create persists a new experience entry.
	Create(ctx context.Context, exp *StoredExperience) error
}

// StoredExperience represents an experience entry to be stored in the experience store.
type StoredExperience struct {
	// TenantID is the tenant identifier for multi-tenancy isolation.
	TenantID string
	// Type is the experience type (e.g., "solution", "heuristic", "strategy", "failure", "general").
	Type string
	// Problem is the abstract problem statement.
	Problem string
	// Solution is the concise solution approach.
	Solution string
	// Score is the importance score (0-1).
	Score float64
	// Source indicates where this experience originated from.
	Source string
	// Metadata holds additional structured data.
	Metadata map[string]interface{}
}
