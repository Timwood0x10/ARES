// Package models provides comprehensive tests for storage system data structures.
package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestKnowledgeChunk_EmbeddingStatusConstants tests embedding status constants.
func TestKnowledgeChunk_EmbeddingStatusConstants(t *testing.T) {
	assert.Equal(t, "pending", EmbeddingStatusPending)
	assert.Equal(t, "processing", EmbeddingStatusProcessing)
	assert.Equal(t, "completed", EmbeddingStatusCompleted)
	assert.Equal(t, "failed", EmbeddingStatusFailed)
}

// TestKnowledgeChunk_TableName tests table name returns correct value.
func TestKnowledgeChunk_TableName(t *testing.T) {
	chunk := &KnowledgeChunk{}
	assert.Equal(t, "knowledge_chunks_1024", chunk.TableName())
}

// TestKnowledgeChunk_ValidFields tests valid field assignment.
func TestKnowledgeChunk_ValidFields(t *testing.T) {
	chunk := &KnowledgeChunk{
		ID:               "test-id",
		TenantID:         "tenant-1",
		Content:          "test content",
		Embedding:        []float64{0.1, 0.2, 0.3},
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		EmbeddingStatus:  EmbeddingStatusCompleted,
		ChunkIndex:       1,
		DocumentID:       "doc-1",
		SourceType:       "document",
		Source:           "test-source",
		Metadata:         map[string]interface{}{"key": "value"},
		ContentHash:      "hash-123",
		AccessCount:      10,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	assert.Equal(t, "test-id", chunk.ID)
	assert.Equal(t, "tenant-1", chunk.TenantID)
	assert.Equal(t, "test content", chunk.Content)
	assert.Len(t, chunk.Embedding, 3)
	assert.Equal(t, "e5-large", chunk.EmbeddingModel)
	assert.Equal(t, 1, chunk.EmbeddingVersion)
	assert.Equal(t, EmbeddingStatusCompleted, chunk.EmbeddingStatus)
	assert.Equal(t, 1, chunk.ChunkIndex)
	assert.Equal(t, "doc-1", chunk.DocumentID)
	assert.Equal(t, "document", chunk.SourceType)
	assert.Equal(t, "test-source", chunk.Source)
	assert.Equal(t, "hash-123", chunk.ContentHash)
	assert.Equal(t, 10, chunk.AccessCount)
}

// TestKnowledgeChunk_EmptyFields tests handling of empty fields.
func TestKnowledgeChunk_EmptyFields(t *testing.T) {
	chunk := &KnowledgeChunk{}

	assert.Empty(t, chunk.ID)
	assert.Empty(t, chunk.TenantID)
	assert.Empty(t, chunk.Content)
	assert.Nil(t, chunk.Embedding)
	assert.Empty(t, chunk.EmbeddingModel)
	assert.Equal(t, 0, chunk.EmbeddingVersion)
	assert.Empty(t, chunk.EmbeddingStatus)
	assert.Equal(t, 0, chunk.ChunkIndex)
	assert.Empty(t, chunk.DocumentID)
	assert.Empty(t, chunk.SourceType)
	assert.Empty(t, chunk.Source)
	assert.Nil(t, chunk.Metadata)
	assert.Empty(t, chunk.ContentHash)
	assert.Equal(t, 0, chunk.AccessCount)
	assert.True(t, chunk.CreatedAt.IsZero())
	assert.True(t, chunk.UpdatedAt.IsZero())
}

// TestKnowledgeChunk_EmptyEmbedding tests handling of empty embedding.
func TestKnowledgeChunk_EmptyEmbedding(t *testing.T) {
	chunk := &KnowledgeChunk{
		ID:               "test-id",
		Embedding:        []float64{},
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		EmbeddingStatus:  EmbeddingStatusPending,
	}

	assert.Empty(t, chunk.Embedding)
	assert.Equal(t, EmbeddingStatusPending, chunk.EmbeddingStatus)
}

// TestKnowledgeChunk_NilEmbedding tests handling of nil embedding.
func TestKnowledgeChunk_NilEmbedding(t *testing.T) {
	chunk := &KnowledgeChunk{
		ID:               "test-id",
		Embedding:        nil,
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		EmbeddingStatus:  EmbeddingStatusPending,
	}

	assert.Nil(t, chunk.Embedding)
	assert.Equal(t, EmbeddingStatusPending, chunk.EmbeddingStatus)
}

// TestExperience_TypeConstants tests experience type constants.
func TestExperience_TypeConstants(t *testing.T) {
	assert.Equal(t, "query", ExperienceTypeQuery)
	assert.Equal(t, "solution", ExperienceTypeSolution)
	assert.Equal(t, "failure", ExperienceTypeFailure)
	assert.Equal(t, "pattern", ExperienceTypePattern)
	assert.Equal(t, "distilled", ExperienceTypeDistilled)
}

// TestExperience_TableName tests table name returns correct value.
func TestExperience_TableName(t *testing.T) {
	exp := &Experience{}
	assert.Equal(t, "experiences_1024", exp.TableName())
}

// TestExperience_ValidFields tests valid field assignment.
func TestExperience_ValidFields(t *testing.T) {
	exp := &Experience{
		ID:               "exp-id",
		TenantID:         "tenant-1",
		Type:             ExperienceTypeSolution,
		Input:            "test input",
		Output:           "test output",
		Embedding:        []float64{0.1, 0.2, 0.3},
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Score:            0.9,
		Success:          true,
		AgentID:          "agent-1",
		Metadata:         map[string]interface{}{"key": "value"},
		DecayAt:          time.Now().Add(24 * time.Hour),
		CreatedAt:        time.Now(),
	}

	assert.Equal(t, "exp-id", exp.ID)
	assert.Equal(t, "tenant-1", exp.TenantID)
	assert.Equal(t, ExperienceTypeSolution, exp.Type)
	assert.Equal(t, "test input", exp.Input)
	assert.Equal(t, "test output", exp.Output)
	assert.Len(t, exp.Embedding, 3)
	assert.Equal(t, "e5-large", exp.EmbeddingModel)
	assert.Equal(t, 1, exp.EmbeddingVersion)
	assert.Equal(t, 0.9, exp.Score)
	assert.True(t, exp.Success)
	assert.Equal(t, "agent-1", exp.AgentID)
	assert.False(t, exp.IsExpired())
}

// TestExperience_EmptyFields tests handling of empty fields.
func TestExperience_EmptyFields(t *testing.T) {
	exp := &Experience{}

	assert.Empty(t, exp.ID)
	assert.Empty(t, exp.TenantID)
	assert.Empty(t, exp.Type)
	assert.Empty(t, exp.Input)
	assert.Empty(t, exp.Output)
	assert.Nil(t, exp.Embedding)
	assert.Empty(t, exp.EmbeddingModel)
	assert.Equal(t, 0, exp.EmbeddingVersion)
	assert.Equal(t, 0.0, exp.Score)
	assert.False(t, exp.Success)
	assert.Empty(t, exp.AgentID)
	assert.Nil(t, exp.Metadata)
	assert.True(t, exp.DecayAt.IsZero())
	assert.True(t, exp.CreatedAt.IsZero())
}

// TestExperience_IsExpired tests expiration logic.
func TestExperience_IsExpired(t *testing.T) {
	tests := []struct {
		name     string
		decayAt  time.Time
		expected bool
	}{
		{
			name:     "expired experience",
			decayAt:  time.Now().Add(-1 * time.Hour),
			expected: true,
		},
		{
			name:     "not expired experience",
			decayAt:  time.Now().Add(1 * time.Hour),
			expected: false,
		},
		{
			name:     "zero decay time",
			decayAt:  time.Time{},
			expected: false,
		},
		{
			name:     "exactly expired",
			decayAt:  time.Now(),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := &Experience{
				ID:      "exp-id",
				DecayAt: tt.decayAt,
			}
			assert.Equal(t, tt.expected, exp.IsExpired())
		})
	}
}

// TestExperience_EmptyEmbedding tests handling of empty embedding.
func TestExperience_EmptyEmbedding(t *testing.T) {
	exp := &Experience{
		ID:        "exp-id",
		Type:      ExperienceTypeQuery,
		Embedding: []float64{},
		Score:     0.5,
		Success:   false,
		DecayAt:   time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	assert.Empty(t, exp.Embedding)
	assert.Equal(t, ExperienceTypeQuery, exp.Type)
	assert.Equal(t, 0.5, exp.Score)
	assert.False(t, exp.Success)
}

// TestExperience_NilEmbedding tests handling of nil embedding.
func TestExperience_NilEmbedding(t *testing.T) {
	exp := &Experience{
		ID:        "exp-id",
		Type:      ExperienceTypeFailure,
		Embedding: nil,
		Score:     0.3,
		Success:   false,
		DecayAt:   time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	assert.Nil(t, exp.Embedding)
	assert.Equal(t, ExperienceTypeFailure, exp.Type)
	assert.Equal(t, 0.3, exp.Score)
	assert.False(t, exp.Success)
}

// TestTool_TableName tests table name returns correct value.
func TestTool_TableName(t *testing.T) {
	tool := &Tool{}
	assert.Equal(t, "tools", tool.TableName())
}

// TestTool_ValidFields tests valid field assignment.
func TestTool_ValidFields(t *testing.T) {
	tool := &Tool{
		ID:               "tool-id",
		TenantID:         "tenant-1",
		Name:             "test-tool",
		Description:      "test description",
		Embedding:        []float64{0.1, 0.2, 0.3},
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		AgentType:        "general",
		Tags:             []string{"tag1", "tag2"},
		UsageCount:       10,
		SuccessRate:      0.8,
		LastUsedAt:       time.Now(),
		Metadata:         map[string]interface{}{"key": "value"},
		CreatedAt:        time.Now(),
	}

	assert.Equal(t, "tool-id", tool.ID)
	assert.Equal(t, "tenant-1", tool.TenantID)
	assert.Equal(t, "test-tool", tool.Name)
	assert.Equal(t, "test description", tool.Description)
	assert.Len(t, tool.Embedding, 3)
	assert.Equal(t, "e5-large", tool.EmbeddingModel)
	assert.Equal(t, 1, tool.EmbeddingVersion)
	assert.Equal(t, "general", tool.AgentType)
	assert.Equal(t, []string{"tag1", "tag2"}, tool.Tags)
	assert.Equal(t, 10, tool.UsageCount)
	assert.Equal(t, 0.8, tool.SuccessRate)
	assert.True(t, tool.IsAvailable())
}

// TestTool_EmptyFields tests handling of empty fields.
func TestTool_EmptyFields(t *testing.T) {
	tool := &Tool{}

	assert.Empty(t, tool.ID)
	assert.Empty(t, tool.TenantID)
	assert.Empty(t, tool.Name)
	assert.Empty(t, tool.Description)
	assert.Nil(t, tool.Embedding)
	assert.Empty(t, tool.EmbeddingModel)
	assert.Equal(t, 0, tool.EmbeddingVersion)
	assert.Empty(t, tool.AgentType)
	assert.Nil(t, tool.Tags)
	assert.Equal(t, 0, tool.UsageCount)
	assert.Equal(t, 0.0, tool.SuccessRate)
	assert.True(t, tool.LastUsedAt.IsZero())
	assert.Nil(t, tool.Metadata)
	assert.True(t, tool.CreatedAt.IsZero())
	assert.False(t, tool.IsAvailable())
}

// TestTool_UpdateUsage_Success tests update usage with success.
func TestTool_UpdateUsage_Success(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		UsageCount:  0,
		SuccessRate: 0.0,
		CreatedAt:   time.Now(),
	}

	// First successful usage
	tool.UpdateUsage(true)
	assert.Equal(t, 1, tool.UsageCount)
	assert.Equal(t, 1.0, tool.SuccessRate)
	assert.True(t, tool.IsAvailable())

	// Second successful usage
	tool.UpdateUsage(true)
	assert.Equal(t, 2, tool.UsageCount)
	assert.Equal(t, 1.0, tool.SuccessRate)
	assert.True(t, tool.IsAvailable())
}

// TestTool_UpdateUsage_Failure tests update usage with failure.
func TestTool_UpdateUsage_Failure(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		UsageCount:  0,
		SuccessRate: 0.0,
		CreatedAt:   time.Now(),
	}

	// First failed usage
	tool.UpdateUsage(false)
	assert.Equal(t, 1, tool.UsageCount)
	assert.Equal(t, 0.0, tool.SuccessRate)
	assert.False(t, tool.IsAvailable())

	// Second failed usage
	tool.UpdateUsage(false)
	assert.Equal(t, 2, tool.UsageCount)
	assert.Equal(t, 0.0, tool.SuccessRate)
	assert.False(t, tool.IsAvailable())
}

// TestTool_UpdateUsage_MixedSuccess tests update usage with mixed success and failure.
func TestTool_UpdateUsage_MixedSuccess(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		UsageCount:  0,
		SuccessRate: 0.0,
		CreatedAt:   time.Now(),
	}

	// Mixed usage pattern
	tool.UpdateUsage(true)
	assert.Equal(t, 1, tool.UsageCount)
	assert.Equal(t, 1.0, tool.SuccessRate)

	tool.UpdateUsage(false)
	assert.Equal(t, 2, tool.UsageCount)
	assert.InDelta(t, 0.9, tool.SuccessRate, 0.001) // 0.1 * 0.0 + 0.9 * 1.0 = 0.9

	tool.UpdateUsage(true)
	assert.Equal(t, 3, tool.UsageCount)
	assert.InDelta(t, 0.91, tool.SuccessRate, 0.001) // 0.1 * 1.0 + 0.9 * 0.9 = 0.91

	tool.UpdateUsage(false)
	assert.Equal(t, 4, tool.UsageCount)
	assert.InDelta(t, 0.819, tool.SuccessRate, 0.001) // 0.1 * 0.0 + 0.9 * 0.91 = 0.819
}

// TestTool_IsAvailable tests availability check based on success rate.
func TestTool_IsAvailable(t *testing.T) {
	tests := []struct {
		name        string
		successRate float64
		expected    bool
	}{
		{
			name:        "high success rate",
			successRate: 0.9,
			expected:    true,
		},
		{
			name:        "exact threshold",
			successRate: 0.5,
			expected:    true,
		},
		{
			name:        "below threshold",
			successRate: 0.4,
			expected:    false,
		},
		{
			name:        "zero success rate",
			successRate: 0.0,
			expected:    false,
		},
		{
			name:        "perfect success rate",
			successRate: 1.0,
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &Tool{
				ID:          "tool-id",
				Name:        "test-tool",
				SuccessRate: tt.successRate,
				CreatedAt:   time.Now(),
			}
			assert.Equal(t, tt.expected, tool.IsAvailable())
		})
	}
}

// TestTool_EmptyEmbedding tests handling of empty embedding.
func TestTool_EmptyEmbedding(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Embedding:   []float64{},
		UsageCount:  5,
		SuccessRate: 0.7,
		CreatedAt:   time.Now(),
	}

	assert.Empty(t, tool.Embedding)
	assert.True(t, tool.IsAvailable())
}

// TestTool_NilEmbedding tests handling of nil embedding.
func TestTool_NilEmbedding(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Embedding:   nil,
		UsageCount:  3,
		SuccessRate: 0.6,
		CreatedAt:   time.Now(),
	}

	assert.Nil(t, tool.Embedding)
	assert.True(t, tool.IsAvailable())
}

// TestTool_EmptyTags tests handling of empty tags.
func TestTool_EmptyTags(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Tags:        []string{},
		UsageCount:  5,
		SuccessRate: 0.7,
		CreatedAt:   time.Now(),
	}

	assert.Empty(t, tool.Tags)
	assert.True(t, tool.IsAvailable())
}

// TestTool_NilTags tests handling of nil tags.
func TestTool_NilTags(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Tags:        nil,
		UsageCount:  5,
		SuccessRate: 0.7,
		CreatedAt:   time.Now(),
	}

	assert.Nil(t, tool.Tags)
	assert.True(t, tool.IsAvailable())
}

// TestTool_MultipleTags tests handling of multiple tags.
func TestTool_MultipleTags(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Tags:        []string{"tag1", "tag2", "tag3", "tag4"},
		UsageCount:  5,
		SuccessRate: 0.7,
		CreatedAt:   time.Now(),
	}

	assert.Len(t, tool.Tags, 4)
	assert.Equal(t, "tag1", tool.Tags[0])
	assert.Equal(t, "tag4", tool.Tags[3])
	assert.True(t, tool.IsAvailable())
}

// TestTool_Metadata tests handling of metadata.
func TestTool_Metadata(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Metadata:    map[string]interface{}{"key1": "value1", "key2": 123},
		UsageCount:  5,
		SuccessRate: 0.7,
		CreatedAt:   time.Now(),
	}

	assert.NotNil(t, tool.Metadata)
	assert.Equal(t, "value1", tool.Metadata["key1"])
	assert.Equal(t, 123, tool.Metadata["key2"])
}

// TestTool_NilMetadata tests handling of nil metadata.
func TestTool_NilMetadata(t *testing.T) {
	tool := &Tool{
		ID:          "tool-id",
		Name:        "test-tool",
		Metadata:    nil,
		UsageCount:  5,
		SuccessRate: 0.7,
		CreatedAt:   time.Now(),
	}

	assert.Nil(t, tool.Metadata)
}
