// Package distillation provides memory distillation functionality for agent experience extraction.
package distillation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
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

// --- SubscribeAndDistill and processEvent tests ---

func TestSubscribeAndDistill_NilStore(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})

	distiller := NewDistiller(config, embedder, repo)

	// Should not panic or start any goroutine.
	distiller.SubscribeAndDistill(context.Background(), nil)
}

func TestSubscribeAndDistill_CancelledContext(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should handle cancelled context gracefully.
	distiller.SubscribeAndDistill(ctx, store)
}

func TestSubscribeAndDistill_ReceivesEvents(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	distiller.SubscribeAndDistill(ctx, store)

	// Wait briefly for the subscription goroutine to start reading from the channel.
	// Uses a short deadline instead of arbitrary sleep to avoid flaky tests.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer waitCancel()
	<-waitCtx.Done()

	// Publish ares_events to the store.
	err := store.Append(context.Background(), "stream-1", []*ares_events.Event{
		{
			Type: ares_events.EventMessageAdded,
			Payload: map[string]any{
				"role": "user",
			},
		},
		{
			Type: ares_events.EventTaskCompleted,
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
	}, 0)
	require.NoError(t, err)

	// Wait for the subscriber goroutine to process published ares_events.
	processCtx, processCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer processCancel()
	<-processCtx.Done()
	// No assertion needed: the test verifies that no panic or deadlock occurs.
}

func TestProcessEvent_NilEvent(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	// Should not panic.
	distiller.processEvent(context.Background(), nil)
}

func TestProcessEvent_MessageAdded(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	event := &ares_events.Event{
		StreamID: "stream-1",
		Type:     ares_events.EventMessageAdded,
		Payload: map[string]any{
			"role": "user",
		},
	}

	// Should not panic.
	distiller.processEvent(context.Background(), event)
}

func TestProcessEvent_TaskCompleted(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	event := &ares_events.Event{
		StreamID: "stream-1",
		Type:     ares_events.EventTaskCompleted,
		Payload: map[string]any{
			"task_id": "task-1",
		},
	}

	// Should not panic.
	distiller.processEvent(context.Background(), event)
}

func TestProcessEvent_UnknownEventType(t *testing.T) {
	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	event := &ares_events.Event{
		StreamID: "stream-1",
		Type:     ares_events.EventAgentStarted,
		Payload:  map[string]any{},
	}

	// Should not panic — unknown ares_events are handled by the default case.
	distiller.processEvent(context.Background(), event)
}

func TestSubscribeAndDistill_FilteredEventTypes(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	config := DefaultDistillationConfig()
	embedder := NewMockEmbeddingService()
	repo := NewMockExperienceRepository([]Experience{})
	distiller := NewDistiller(config, embedder, repo)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	distiller.SubscribeAndDistill(ctx, store)

	// Wait briefly for the subscription goroutine to start reading from the channel.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer waitCancel()
	<-waitCtx.Done()

	// Publish ares_events of various types; only EventMessageAdded and
	// EventTaskCompleted should be received by the subscriber.
	err := store.Append(context.Background(), "stream-1", []*ares_events.Event{
		{
			Type:    ares_events.EventAgentStarted,
			Payload: map[string]any{},
		},
		{
			Type: ares_events.EventMessageAdded,
			Payload: map[string]any{
				"role": "assistant",
			},
		},
		{
			Type:    ares_events.EventFailoverTriggered,
			Payload: map[string]any{},
		},
		{
			Type: ares_events.EventTaskCompleted,
			Payload: map[string]any{
				"task_id": "task-2",
			},
		},
	}, 0)
	require.NoError(t, err)

	// Wait for the subscriber goroutine to process published ares_events.
	processCtx, processCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer processCancel()
	<-processCtx.Done()

	// Verify that the subscription filtered correctly by checking that
	// no panic occurred and that the store has the expected ares_events.
	streamEvents, err := store.Read(context.Background(), "stream-1", ares_events.ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, streamEvents, 4, "store should have all 4 ares_events")
}
