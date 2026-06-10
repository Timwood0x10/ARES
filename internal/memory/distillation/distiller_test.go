// Package distillation provides memory distillation functionality for agent experience extraction.
package distillation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDistiller(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})

	distiller := NewDistiller(config, embedder, repo)

	require.NotNil(t, distiller)

	if distiller.config != config {
		t.Error("Distiller config not set correctly")
	}

	if distiller.embedder != embedder {
		t.Error("Distiller embedder not set correctly")
	}

	if distiller.repo != repo {
		t.Error("Distiller repo not set correctly")
	}
}

func TestDistiller_DistillConversation(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})

	distiller := NewDistiller(config, embedder, repo)

	messages := []Message{
		{Role: "user", Content: "I have an error in my code"},
		{Role: "assistant", Content: "Fix the syntax error on line 10"},
	}

	ctx := context.Background()
	memories, err := distiller.DistillConversation(ctx, "test-conv-1", messages, "default", "user1")

	if err != nil {
		t.Fatalf("DistillConversation() returned error: %v", err)
	}

	// Should extract at least one memory
	if len(memories) == 0 {
		t.Error("DistillConversation() extracted no memories, expected at least one")
	}

	// Validate memory structure
	for _, mem := range memories {
		if mem.Type == "" {
			t.Error("Memory has empty type")
		}
		if mem.Content == "" {
			t.Error("Memory has empty content")
		}
		if mem.Importance < 0 || mem.Importance > 1 {
			t.Errorf("Memory importance %v is out of range [0,1]", mem.Importance)
		}
	}
}

func TestDistiller_GetMetrics(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})

	distiller := NewDistiller(config, embedder, repo)

	metrics := distiller.GetMetrics()

	require.NotNil(t, metrics)

	// Verify metrics structure
	if metrics.AttemptTotal < 0 {
		t.Error("AttemptTotal should be non-negative")
	}
	if metrics.SuccessTotal < 0 {
		t.Error("SuccessTotal should be non-negative")
	}
	if metrics.MemoriesCreated < 0 {
		t.Error("MemoriesCreated should be non-negative")
	}
}

func TestDistiller_ResetMetrics(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})

	distiller := NewDistiller(config, embedder, repo)

	// Run some distillation to populate metrics
	messages := []Message{
		{Role: "user", Content: "test"},
		{Role: "assistant", Content: "response"},
	}
	ctx := context.Background()
	_, err := distiller.DistillConversation(ctx, "test", messages, "default", "user1")
	if err != nil {
		t.Error("DistillConversation() returned error:", err)
	}

	// Reset metrics
	distiller.ResetMetrics()

	metrics := distiller.GetMetrics()

	if metrics.AttemptTotal != 0 || metrics.SuccessTotal != 0 {
		t.Error("ResetMetrics() did not reset metrics")
	}
}

func TestDefaultDistillationConfig(t *testing.T) {
	config := DefaultDistillationConfig()

	require.NotNil(t, config)

	// Verify default values
	if config.MinImportance != 0.6 {
		t.Errorf("MinImportance = %v, want 0.6", config.MinImportance)
	}
	if config.ConflictThreshold != 0.85 {
		t.Errorf("ConflictThreshold = %v, want 0.85", config.ConflictThreshold)
	}
	if config.MaxMemoriesPerDistillation != 3 {
		t.Errorf("MaxMemoriesPerDistillation = %v, want 3", config.MaxMemoriesPerDistillation)
	}
	if config.MaxSolutionsPerTenant != 5000 {
		t.Errorf("MaxSolutionsPerTenant = %v, want 5000", config.MaxSolutionsPerTenant)
	}
	if !config.EnableCodeFilter {
		t.Error("EnableCodeFilter should be true by default")
	}
	if !config.PrecisionOverRecall {
		t.Error("PrecisionOverRecall should be true by default")
	}
}
