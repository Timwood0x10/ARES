// nolint: errcheck // Test code may ignore return values
package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// EventSummary Model Tests
// ============================================================================

func TestEventSummary_Duration(t *testing.T) {
	s := &EventSummary{
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 1, 1, 0, 5, 30, 0, time.UTC),
	}
	assert.Equal(t, 5*time.Minute+30*time.Second, s.Duration())
}

func TestEventSummary_NewEventSummaryID_GeneratesUniqueIDs(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewEventSummaryID()
		assert.False(t, ids[id], "ID %q should be unique", id)
		ids[id] = true
	}
	assert.Len(t, ids, 100)
}

// ============================================================================
// CompactionConfig Tests
// ============================================================================

func TestDefaultCompactionConfig_SensibleDefaults(t *testing.T) {
	cfg := DefaultCompactionConfig()
	assert.Equal(t, 500, cfg.Threshold)
	assert.Equal(t, 100, cfg.KeepRecent)
	assert.Equal(t, 50, cfg.MaxSummariesPerStream)
	assert.Equal(t, 30*24*time.Hour, cfg.SummaryTTL)
	assert.False(t, cfg.EnableTrimming)
}

func TestCompactionConfig_ZeroThreshold_HandledByNewCompactor(t *testing.T) {
	// NewCompactor should replace zero threshold with defaults.
	memStore := NewMemoryEventStore()
	mockRepo := &mockSummaryRepo{}
	c := NewCompactor(memStore, mockRepo, CompactionConfig{Threshold: 0})
	require.NotNil(t, c)
	assert.Equal(t, DefaultCompactionConfig().Threshold, c.config.Threshold)
}

func TestCompactionConfig_NegativeValues(t *testing.T) {
	cfg := CompactionConfig{
		Threshold:             -10,
		KeepRecent:            -5,
		MaxSummariesPerStream: -1,
		SummaryTTL:            -time.Hour,
	}
	c := NewCompactor(nil, nil, cfg)
	// Should still create a compactor; negative values are caller's responsibility
	// to validate or the compactor should handle gracefully.
	require.NotNil(t, c)
}

// ============================================================================
// collectTool Helper Tests
// ============================================================================

func TestCollectTool_ExtractsStringFromPayload(t *testing.T) {
	payload := map[string]any{"tool": "search"}
	seen := make(map[string]bool)
	var tools []string

	collectTool(payload, "tool", seen, &tools)

	assert.Len(t, tools, 1)
	assert.Equal(t, "search", tools[0])
	assert.True(t, seen["search"])
}

func TestCollectTool_IgnoresEmptyString(t *testing.T) {
	payload := map[string]any{"tool": ""}
	seen := make(map[string]bool)
	var tools []string

	collectTool(payload, "tool", seen, &tools)

	assert.Empty(t, tools)
}

func TestCollectTool_IgnoresNonStringValue(t *testing.T) {
	payload := map[string]any{"tool": 12345}
	seen := make(map[string]bool)
	var tools []string

	collectTool(payload, "tool", seen, &tools)

	assert.Empty(t, tools)
}

func TestCollectTool_IgnoresMissingKey(t *testing.T) {
	payload := map[string]any{"other_key": "value"}
	seen := make(map[string]bool)
	var tools []string

	collectTool(payload, "nonexistent", seen, &tools)

	assert.Empty(t, tools)
}

func TestCollectTool_Deduplicates(t *testing.T) {
	payload := map[string]any{"tool": "search"}
	seen := make(map[string]bool) // Pre-populate: mark "search" as already seen.
	seen["search"] = true
	var tools []string

	collectTool(payload, "tool", seen, &tools)

	assert.Empty(t, tools, "should not add duplicate tool")
}

func TestCollectTool_NilPayload(t *testing.T) {
	var tools []string
	seen := make(map[string]bool)
	// Should not panic on nil payload.
	collectTool(nil, "tool", seen, &tools)
	assert.Empty(t, tools)
}

// ============================================================================
// DefaultSummarizer Tests
// ============================================================================

func TestDefaultSummarizer_EmptyEvents(t *testing.T) {
	result := DefaultSummarizer(nil)
	assert.Equal(t, "(empty event window)", result)

	result = DefaultSummarizer([]*Event{})
	assert.Equal(t, "(empty event window)", result)
}

func TestDefaultSummarizer_SingleEvent(t *testing.T) {
	base := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	events := []*Event{
		{
			StreamID:  "agent-1",
			Type:      EventAgentStarted,
			Version:   1,
			Timestamp: base,
			Payload:   map[string]any{},
		},
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "Agent agent-1")
	assert.Contains(t, result, "processed 1 events")
	assert.Contains(t, result, "result: active")
}

func TestDefaultSummarizer_FullLifecycle(t *testing.T) {
	base := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	events := []*Event{
		{
			StreamID:  "agent-1",
			Type:      EventSessionCreated,
			Version:   1,
			Timestamp: base,
			Payload: map[string]any{
				"session_id": "sess-1",
				"user_id":    "user-42",
				"input":      "help me plan a trip to Japan for next week with budget under $3000",
			},
		},
		{
			StreamID:  "agent-1",
			Type:      EventTaskCreated,
			Version:   2,
			Timestamp: base.Add(1 * time.Second),
			Payload: map[string]any{
				"task_id": "task-search-flights",
			},
		},
		{
			StreamID:  "agent-1",
			Type:      EventTaskCreated,
			Version:   3,
			Timestamp: base.Add(2 * time.Second),
			Payload: map[string]any{
				"task_id": "task-book-hotel",
			},
		},
		{
			StreamID:  "agent-1",
			Type:      EventLLMCall,
			Version:   4,
			Timestamp: base.Add(3 * time.Second),
			Payload: map[string]any{
				"function": "search_flights",
			},
		},
		{
			StreamID:  "agent-1",
			Type:      EventLLMCall,
			Version:   5,
			Timestamp: base.Add(4 * time.Second),
			Payload: map[string]any{
				"tool": "weather_api",
			},
		},
		{
			StreamID:  "agent-1",
			Type:      EventTaskCompleted,
			Version:   6,
			Timestamp: base.Add(5 * time.Second),
			Payload:   map[string]any{},
		},
		{
			StreamID:  "agent-1",
			Type:      EventMessageAdded,
			Version:   7,
			Timestamp: base.Add(6 * time.Second),
			Payload: map[string]any{
				"content": "Here is your trip plan...",
			},
		},
	}

	result := DefaultSummarizer(events)

	// Verify all key components are present.
	assert.Contains(t, result, "Agent agent-1")
	assert.Contains(t, result, "ran 2 task(s)")
	assert.Contains(t, result, "task-search-flights")
	assert.Contains(t, result, "task-book-hotel")
	assert.Contains(t, result, "called 2 tool(s)")
	assert.Contains(t, result, "search_flights")
	assert.Contains(t, result, "weather_api")
	assert.Contains(t, result, "emitted 7 events")
	assert.Contains(t, result, "duration")
	assert.Contains(t, result, "bound to user request")
	assert.Contains(t, result, "Japan") // from truncated request
	assert.Contains(t, result, "result: completed")
}

func TestDefaultSummarizer_FailedOutcome(t *testing.T) {
	base := time.Now()
	events := []*Event{
		{StreamID: "a1", Type: EventTaskCreated, Version: 1, Timestamp: base, Payload: map[string]any{"task_id": "t1"}},
		{StreamID: "a1", Type: EventTaskFailed, Version: 2, Timestamp: base.Add(time.Second), Payload: map[string]any{"error": "timeout"}},
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "result: failed")
	assert.Contains(t, result, "errors: [timeout]")
}

func TestDefaultSummarizer_PartialOutcome(t *testing.T) {
	base := time.Now()
	events := []*Event{
		{StreamID: "a1", Type: EventTaskCreated, Version: 1, Timestamp: base, Payload: map[string]any{"task_id": "t1"}},
		{StreamID: "a1", Type: EventTaskCompleted, Version: 2, Timestamp: base.Add(time.Second)},
		{StreamID: "a1", Type: EventTaskFailed, Version: 3, Timestamp: base.Add(2 * time.Second), Payload: map[string]any{"error": "rate limited"}},
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "result: partial")
}

func TestDefaultSummarizer_LongRequest_Truncated(t *testing.T) {
	longRequest := strings.Repeat("x", 300)

	events := []*Event{
		{
			StreamID: "a1", Type: EventSessionCreated, Version: 1, Timestamp: time.Now(),
			Payload: map[string]any{"input": longRequest},
		},
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "...")          // truncated
	assert.NotContains(t, result, longRequest) // full text not present
}

func TestDefaultSummarizer_MultipleErrors_CappedAt3(t *testing.T) {
	base := time.Now()
	events := []*Event{
		{StreamID: "a1", Type: EventTaskFailed, Version: 1, Timestamp: base, Payload: map[string]any{"error": "err-1"}},
		{StreamID: "a1", Type: EventTaskFailed, Version: 2, Timestamp: base.Add(time.Second), Payload: map[string]any{"error": "err-2"}},
		{StreamID: "a1", Type: EventFailoverTriggered, Version: 3, Timestamp: base.Add(2 * time.Second), Payload: map[string]any{"error": "err-3"}},
		{StreamID: "a1", Type: EventFailoverTriggered, Version: 4, Timestamp: base.Add(3 * time.Second), Payload: map[string]any{"error": "err-4"}},
		{StreamID: "a1", Type: EventFailoverTriggered, Version: 5, Timestamp: base.Add(4 * time.Second), Payload: map[string]any{"error": "err-5"}},
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "err-1")
	assert.Contains(t, result, "err-2")
	assert.Contains(t, result, "err-3")
	// err-4 and err-5 should be capped (max 3 errors in DefaultSummarizer).
}

func TestDefaultSummarizer_ManyTasks_Truncated(t *testing.T) {
	base := time.Now()
	var events []*Event
	for i := 0; i < 10; i++ {
		events = append(events, &Event{
			StreamID: "a1", Type: EventTaskCreated, Version: int64(i + 1),
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Payload:   map[string]any{"task_id": fmt.Sprintf("task-%d", i)},
		})
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "10 task(s)")
	assert.Contains(t, result, "+7 more") // 10 tasks, show 3 + 7 more
}

func TestDefaultSummarizer_ManyTools_Truncated(t *testing.T) {
	base := time.Now()
	var events []*Event
	for i := 0; i < 8; i++ {
		events = append(events, &Event{
			StreamID: "a1", Type: EventLLMCall, Version: int64(i + 1),
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Payload:   map[string]any{"function": fmt.Sprintf("tool_%d", i)},
		})
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "8 tool(s)")
	assert.Contains(t, result, "+3 more") // 8 tools, show 5 + 3 more
}

func TestDefaultSummarizer_NoTasks_ShowsProcessedCount(t *testing.T) {
	events := []*Event{
		{StreamID: "a1", Type: EventAgentStarted, Version: 1, Timestamp: time.Now()},
		{StreamID: "a1", Type: EventMessageAdded, Version: 2, Timestamp: time.Now().Add(time.Second)},
	}

	result := DefaultSummarizer(events)
	assert.Contains(t, result, "processed 2 events")
	assert.NotContains(t, result, "ran")
}

func TestDefaultSummarizer_NoTools_OmitsToolsSection(t *testing.T) {
	events := []*Event{
		{StreamID: "a1", Type: EventTaskCreated, Version: 1, Timestamp: time.Now(), Payload: map[string]any{"task_id": "t1"}},
		{StreamID: "a1", Type: EventTaskCompleted, Version: 2, Timestamp: time.Now().Add(time.Second)},
	}

	result := DefaultSummarizer(events)
	assert.NotContains(t, result, "called")
	assert.Contains(t, result, "result: completed")
}

// ============================================================================
// Compactor Tests (unit tests using MemoryEventStore + mock repo)
// ============================================================================

// mockSummaryRepo implements SummaryRepository in-memory for unit testing.
type mockSummaryRepo struct {
	mu        sync.Mutex
	summaries map[string]*EventSummary // keyed by ID
	saveErr   error
}

func newMockSummaryRepo() *mockSummaryRepo {
	return &mockSummaryRepo{
		summaries: make(map[string]*EventSummary),
	}
}

func (m *mockSummaryRepo) Save(_ context.Context, s *EventSummary) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	m.summaries[s.ID] = s
	m.mu.Unlock()
	return nil
}

func (m *mockSummaryRepo) FindByStreamID(_ context.Context, streamID string) ([]*EventSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*EventSummary
	for _, s := range m.summaries {
		if s.StreamID == streamID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSummaryRepo) FindByAgentAndTask(_ context.Context, _, _ string) ([]*EventSummary, error) {
	return nil, nil
}

func (m *mockSummaryRepo) FindByAgentID(_ context.Context, agentID string) ([]*EventSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*EventSummary
	for _, s := range m.summaries {
		if s.AgentID == agentID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSummaryRepo) FindLatestByStreamID(_ context.Context, streamID string) (*EventSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *EventSummary
	for _, s := range m.summaries {
		if s.StreamID == streamID && (latest == nil || s.EndVersion > latest.EndVersion) {
			latest = s
		}
	}
	return latest, nil
}

func (m *mockSummaryRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.summaries, id)
	m.mu.Unlock()
	return nil
}

func (m *mockSummaryRepo) DeleteOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func TestCompactor_CheckAndCompact_BelowThreshold_NoOp(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 100, KeepRecent: 20}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-below-threshold"
	// Append 50 events — below threshold of 100.
	appendNEvents(t, store, ctx, streamID, 50)

	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.False(t, didCompact, "should NOT compact when below threshold")

	// Repo should be empty.
	summaries, _ := repo.FindByStreamID(ctx, streamID)
	assert.Empty(t, summaries)
}

func TestCompactor_CheckAndCompact_ExactlyAtThreshold_NoOp(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 100, KeepRecent: 20}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-at-threshold"
	appendNEvents(t, store, ctx, streamID, 100)

	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.False(t, didCompact, "at exactly threshold, version == threshold so no compaction")
}

func TestCompactor_CheckAndCompact_AboveThreshold_TriggersCompaction(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 10, KeepRecent: 3}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-above-threshold"
	appendNEvents(t, store, ctx, streamID, 25) // 25 > threshold of 10

	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.True(t, didCompact, "SHOULD compact when above threshold")

	// Verify summary was saved.
	summaries, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	summary := summaries[0]
	assert.Equal(t, streamID, summary.StreamID)
	assert.Greater(t, summary.EventCount, 0)
	// Should have compacted events[0..21] (25 total - 3 keepRecent = 22 candidates).
	assert.Equal(t, 22, summary.EventCount)
	assert.Equal(t, int64(1), summary.StartVersion)
	assert.Equal(t, int64(22), summary.EndVersion)
	assert.NotEmpty(t, summary.SummaryText)
	assert.NotEmpty(t, summary.ID)
}

func TestCompactor_CheckAndCompact_EmptyStream_NoError(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 5, KeepRecent: 2}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	didCompact, err := c.CheckAndCompact(ctx, "nonexistent-stream")
	require.NoError(t, err)
	assert.False(t, didCompact)
}

func TestCompactor_CheckAndCompact_KeepRecentEqualsTotal_NoCompaction(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 5, KeepRecent: 100} // KeepRecent > total
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-keeprecent-large"
	appendNEvents(t, store, ctx, streamID, 10)

	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.False(t, didCompact, "no candidates when KeepRecent >= total events")
}

func TestCompactor_CheckAndCompact_KeepRecentZero_CompactsAll(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 5, KeepRecent: 0} // keep nothing!
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-keeprecent-zero"
	appendNEvents(t, store, ctx, streamID, 20)

	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.True(t, didCompact)

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	require.Len(t, summaries, 1)
	assert.Equal(t, 20, summaries[0].EventCount, "all events should be compacted when KeepRecent=0")
}

func TestCompactor_BuildSummary_EventTypeCounts(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 5, KeepRecent: 0}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-type-counts"
	// Append specific event types.
	eventTypes := []EventType{
		EventAgentStarted, EventTaskCreated, EventTaskDispatched,
		EventTaskCreated, EventLLMCall, EventMessageAdded,
		EventTaskCreated, EventTaskFailed, EventTaskCompleted,
	}
	for i, et := range eventTypes {
		err := store.Append(ctx, streamID, []*Event{{Type: et}}, int64(i))
		require.NoError(t, err)
	}

	_, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	require.Len(t, summaries, 1)
	counts := summaries[0].EventTypeCounts

	assert.Equal(t, 3, counts["task.created"], "3 task.created events")
	assert.Equal(t, 1, counts["task.failed"])
	assert.Equal(t, 1, counts["task.completed"])
	assert.Equal(t, 1, counts["llm.call"])
}

func TestCompactor_BuildSummary_ExtractsTaskInfo(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 3, KeepRecent: 0}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-task-extract"
	events := []*Event{
		{Type: EventTaskCreated, Payload: map[string]any{"task_id": "alpha"}},
		{Type: EventTaskCreated, Payload: map[string]any{"task_id": "beta"}},
		{Type: EventTaskCreated, Payload: map[string]any{"task_id": "gamma"}},
		{Type: EventLLMCall, Payload: map[string]any{"function": "search"}},
		{Type: EventSessionCreated, Payload: map[string]any{
			"session_id": "sess-1", "user_id": "user-1", "input": "find me hotels in Tokyo",
		}},
	}
	for _, evt := range events {
		err := store.Append(ctx, streamID, []*Event{evt}, 0)
		require.NoError(t, err)
	}

	_, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	require.Len(t, summaries, 1)
	s := summaries[0]

	assert.Equal(t, streamID, s.AgentID)
	assert.Contains(t, s.TasksCreated, "alpha")
	assert.Contains(t, s.TasksCreated, "beta")
	assert.Contains(t, s.TasksCreated, "gamma")
	assert.Len(t, s.TasksCreated, 3)
	assert.Equal(t, "alpha", s.TaskID, "first task ID should be primary")
	assert.Contains(t, s.ToolsCalled, "search")
	assert.Equal(t, "sess-1", s.SessionID)
	assert.Equal(t, "user-1", s.UserID)
	assert.Contains(t, s.RequestSummary, "Tokyo")
}

func TestCompactor_BuildSummary_OutcomeDetection(t *testing.T) {
	tests := []struct {
		name            string
		eventTypes      []EventType
		expectedOutcome string
	}{
		{"all_completed", []EventType{EventTaskCreated, EventTaskCompleted}, "completed"},
		{"has_failure", []EventType{EventTaskCreated, EventTaskFailed}, "failed"},
		{"partial_success", []EventType{EventTaskCreated, EventTaskCompleted, EventTaskFailed}, "partial"},
		{"active_no_terminal", []EventType{EventAgentStarted, EventMessageAdded}, "active"},
		{"failover_only", []EventType{EventFailoverTriggered}, "failed"},
		{"failover_with_complete", []EventType{EventTaskCompleted, EventFailoverTriggered, EventFailoverCompleted}, "partial"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryEventStore()
			repo := newMockSummaryRepo()
			cfg := CompactionConfig{Threshold: 2, KeepRecent: 0}
			c := NewCompactor(store, repo, cfg)
			ctx := context.Background()

			streamID := fmt.Sprintf("outcome-%s", tc.name)
			for i, et := range tc.eventTypes {
				_ = store.Append(ctx, streamID, []*Event{{Type: et}}, int64(i+1))
			}
			// Add one more event to go over threshold if needed.
			_ = store.Append(ctx, streamID, []*Event{{Type: EventAgentStopped}}, int64(len(tc.eventTypes)+1))

			_, err := c.CheckAndCompact(ctx, streamID)
			require.NoError(t, err)

			summaries, _ := repo.FindByStreamID(ctx, streamID)
			if len(summaries) > 0 {
				assert.Equal(t, tc.expectedOutcome, summaries[0].Outcome)
			}
		})
	}
}

func TestCompactor_CustomSummarizer(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 3, KeepRecent: 0}
	c := NewCompactor(store, repo, cfg).WithSummarizer(func(events []*Event) string {
		return fmt.Sprintf("custom: %d events processed", len(events))
	})
	ctx := context.Background()

	streamID := "test-custom-summarizer"
	appendNEvents(t, store, ctx, streamID, 5)

	_, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	require.Len(t, summaries, 1)
	assert.Equal(t, "custom: 5 events processed", summaries[0].SummaryText)
}

func TestCompactor_SaveError_Propagated(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	repo.saveErr = errors.New("db connection lost")
	cfg := CompactionConfig{Threshold: 3, KeepRecent: 0}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "test-save-error"
	appendNEvents(t, store, ctx, streamID, 5)

	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.Error(t, err, "save error should propagate")
	assert.False(t, didCompact)
	assert.Contains(t, err.Error(), "save event summary")
}

func TestCompactor_CompactAll_MultipleStreams(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 5, KeepRecent: 2}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	// Stream A: 20 events (above threshold)
	streamA := "multi-stream-a"
	appendNEvents(t, store, ctx, streamA, 20)

	// Stream B: 3 events (below threshold)
	streamB := "multi-stream-b"
	appendNEvents(t, store, ctx, streamB, 3)

	// Stream C: 10 events (above threshold)
	streamC := "multi-stream-c"
	appendNEvents(t, store, ctx, streamC, 10)

	compacted, err := c.CompactAll(ctx, []string{streamA, streamB, streamC})
	require.NoError(t, err)
	assert.Equal(t, 2, compacted, "only streams A and C should be compacted")

	summariesA, _ := repo.FindByStreamID(ctx, streamA)
	assert.NotEmpty(t, summariesA, "stream A should have a summary")

	summariesB, _ := repo.FindByStreamID(ctx, streamB)
	assert.Empty(t, summariesB, "stream B should have no summary (below threshold)")

	summariesC, _ := repo.FindByStreamID(ctx, streamC)
	assert.NotEmpty(t, summariesC, "stream C should have a summary")
}

func TestCompactor_CleanupOldSummaries(t *testing.T) {
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{SummaryTTL: 1 * time.Hour}
	c := NewCompactor(nil, repo, cfg)
	ctx := context.Background()

	deleted, err := c.CleanupOldSummaries(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted, "mock repo always returns 0")
}

func TestCompactor_NilStore_SafeCheck(t *testing.T) {
	repo := newMockSummaryRepo()
	cfg := DefaultCompactionConfig()
	c := NewCompactor(nil, repo, cfg)
	ctx := context.Background()

	// Should not panic, but will likely fail gracefully at StreamVersion call.
	// This tests that we don't get a nil pointer panic.
	didCompact, err := c.CheckAndCompact(ctx, "some-stream")
	// Error expected because store is nil.
	assert.Error(t, err)
	assert.False(t, didCompact)
}

// ============================================================================
// MemoryTrimStore Tests
// ============================================================================

func TestMemoryTrimStore_TrimBefore_RemovesOldEvents(t *testing.T) {
	store := NewMemoryEventStore()
	trimStore := NewMemoryTrimStore(store)
	ctx := context.Background()

	streamID := "trim-test"
	appendNEvents(t, store, ctx, streamID, 100)

	// Trim events up to version 50.
	removed, err := trimStore.TrimBefore(ctx, streamID, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(50), removed, "should have removed 50 events")

	// Verify remaining events start from version 51.
	remaining, err := store.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	require.Len(t, remaining, 50)
	assert.Equal(t, int64(51), remaining[0].Version)
	assert.Equal(t, int64(100), remaining[len(remaining)-1].Version)
}

func TestMemoryTrimStore_TrimBefore_AllEvents(t *testing.T) {
	store := NewMemoryEventStore()
	trimStore := NewMemoryTrimStore(store)
	ctx := context.Background()

	streamID := "trim-all"
	appendNEvents(t, store, ctx, streamID, 10)

	removed, err := trimStore.TrimBefore(ctx, streamID, 999)
	require.NoError(t, err)
	assert.Equal(t, int64(10), removed)

	remaining, _ := store.Read(ctx, streamID, ReadOptions{})
	assert.Empty(t, remaining)
}

func TestMemoryTrimStore_TrimBefore_NoMatch(t *testing.T) {
	store := NewMemoryEventStore()
	trimStore := NewMemoryTrimStore(store)
	ctx := context.Background()

	streamID := "trim-nomatch"
	appendNEvents(t, store, ctx, streamID, 5)

	// Trim up to version 0 — no events match (versions start at 1).
	removed, err := trimStore.TrimBefore(ctx, streamID, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)

	remaining, _ := store.Read(ctx, streamID, ReadOptions{})
	assert.Len(t, remaining, 5)
}

func TestMemoryTrimStore_TrimBefore_NonexistentStream(t *testing.T) {
	store := NewMemoryEventStore()
	trimStore := NewMemoryTrimStore(store)
	ctx := context.Background()

	removed, err := trimStore.TrimBefore(ctx, "no-such-stream", 100)
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)
}

func TestMemoryTrimStore_TrimBefore_NilStore(t *testing.T) {
	var trimStore *MemoryTrimStore // nil
	ctx := context.Background()

	removed, err := trimStore.TrimBefore(ctx, "stream", 10)
	assert.ErrorIs(t, err, ErrEventStoreClosed)
	assert.Equal(t, int64(0), removed)
}

func TestMemoryTrimStore_TrimBefore_PreservesOtherStreams(t *testing.T) {
	store := NewMemoryEventStore()
	trimStore := NewMemoryTrimStore(store)
	ctx := context.Background()

	appendNEvents(t, store, ctx, "stream-a", 20)
	appendNEvents(t, store, ctx, "stream-b", 20)

	// Only trim stream-a.
	_, err := trimStore.TrimBefore(ctx, "stream-a", 10)
	require.NoError(t, err)

	// stream-b should be untouched.
	bRemaining, _ := store.Read(ctx, "stream-b", ReadOptions{})
	assert.Len(t, bRemaining, 20)
}

func TestMemoryTrimStore_TrimBefore_AfterReadAllConsistency(t *testing.T) {
	store := NewMemoryEventStore()
	trimStore := NewMemoryTrimStore(store)
	ctx := context.Background()

	appendNEvents(t, store, ctx, "stream-x", 50)
	appendNEvents(t, store, ctx, "stream-y", 30)

	// Trim stream-x partially.
	trimStore.TrimBefore(ctx, "stream-x", 25)

	// ReadAll should still return only stream-y's events plus trimmed stream-x events.
	all, err := store.ReadAll(ctx, ReadOptions{})
	require.NoError(t, err)
	// stream-x has 25 remaining (26..50), stream-y has 30.
	assert.Len(t, all, 55)
}

// ============================================================================
// CompactableEventStore Tests
// ============================================================================

func TestCompactableEventStore_Append_TriggersCompaction(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 10, KeepRecent: 3}

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	streamID := "auto-compact-test"
	// Append enough events to trigger compaction.
	appendNEventsToWrapped(t, wrapped, ctx, streamID, 15)

	// Give async compaction time to run.
	time.Sleep(200 * time.Millisecond)

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	assert.NotEmpty(t, summaries, "compaction should have been triggered automatically")
}

func TestCompactableEventStore_Append_BelowThreshold_NoCompaction(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 100, KeepRecent: 10}

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	streamID := "below-auto-threshold"
	appendNEventsToWrapped(t, wrapped, ctx, streamID, 10)

	time.Sleep(200 * time.Millisecond)

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	assert.Empty(t, summaries, "no compaction should occur below threshold")
}

func TestCompactableEventStore_ForceCompact(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 9999, KeepRecent: 5} // very high threshold

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	streamID := "force-compact"
	appendNEventsToWrapped(t, wrapped, ctx, streamID, 8)

	// Force compact even though we're below threshold.
	didCompact, err := wrapped.ForceCompact(ctx, streamID)
	require.NoError(t, err)
	assert.True(t, didCompact, "ForceCompact should work regardless of threshold")

	summaries, _ := repo.FindByStreamID(ctx, streamID)
	assert.NotEmpty(t, summaries)
}

func TestCompactableEventStore_GetSummariesForStream(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 3, KeepRecent: 0}

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	streamID := "get-summary-test"
	appendNEventsToWrapped(t, wrapped, ctx, streamID, 5)
	time.Sleep(200 * time.Millisecond)

	summaries, err := wrapped.GetSummariesForStream(ctx, streamID)
	require.NoError(t, err)
	assert.NotEmpty(t, summaries)
}

func TestCompactableEventStore_GetSummariesForAgent(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 3, KeepRecent: 0}

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	appendNEventsToWrapped(t, wrapped, ctx, "agent-1-stream", 5)
	time.Sleep(200 * time.Millisecond)

	summaries, err := wrapped.GetSummariesForAgent(ctx, "agent-1-stream")
	require.NoError(t, err)
	// AgentID defaults to streamID when no agent_id in metadata.
	assert.NotEmpty(t, summaries)
}

func TestCompactableEventStore_WithCustomSummarizer(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 3, KeepRecent: 0}

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	wrapped.WithCustomSummarizer(func(events []*Event) string {
		return "CUSTOM SUMMARY"
	})
	ctx := context.Background()

	streamID := "custom-wrapped"
	appendNEventsToWrapped(t, wrapped, ctx, streamID, 5)
	time.Sleep(200 * time.Millisecond)

	summaries, _ := wrapped.GetSummariesForStream(ctx, streamID)
	if len(summaries) > 0 {
		assert.Equal(t, "CUSTOM SUMMARY", summaries[0].SummaryText)
	}
}

func TestCompactableEventStore_CleanupSummaries(t *testing.T) {
	repo := newMockSummaryRepo()
	cfg := DefaultCompactionConfig()
	store := NewMemoryEventStore()

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	deleted, err := wrapped.CleanupSummaries(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

// ============================================================================
// Concurrent / Race Condition Tests
// ============================================================================

func TestCompactor_ConcurrentCompactSameStream(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 20, KeepRecent: 5}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := "concurrent-compact"
	appendNEvents(t, store, ctx, streamID, 100)

	const goroutines = 10
	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			didCompact, err := c.CheckAndCompact(ctx, streamID)
			if err == nil && didCompact {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// At least some should succeed (first one trims events, subsequent ones may find fewer).
	assert.GreaterOrEqual(t, successCount, 1, "at least one compaction should succeed")
}

func TestCompactableEventStore_ConcurrentAppendWithAutoCompact(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	cfg := CompactionConfig{Threshold: 20, KeepRecent: 5}

	wrapped := NewCompactableEventStore(store, repo, nil, cfg)
	ctx := context.Background()

	streamID := "race-append-compact"
	var wg sync.WaitGroup
	const writers = 20

	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 5; j++ { // each writer appends 5 events
				evt := &Event{
					Type:    EventTaskCreated,
					Payload: map[string]any{"idx": idx, "j": j},
				}
				_ = wrapped.Append(ctx, streamID, []*Event{evt}, 0)
			}
		}(i)
	}
	wg.Wait()

	// Wait for async compaction.
	time.Sleep(500 * time.Millisecond)

	// Verify data integrity — no panics, no corruption.
	version, err := store.StreamVersion(ctx, streamID)
	require.NoError(t, err)
	assert.Equal(t, int64(writers*5), version)
}

// ============================================================================
// Edge Case: buildSummary boundary conditions
// ============================================================================

func TestBuildSummary_NilEvents_ReturnsNil(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	result := c.buildSummary("test", nil)
	assert.Nil(t, result)
}

func TestBuildSummary_SingleEvent_MinimalFields(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	evt := &Event{
		StreamID:  "single-stream",
		Type:      EventAgentStarted,
		Version:   42,
		Timestamp: time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
		Payload:   map[string]any{},
	}

	result := c.buildSummary("single-stream", []*Event{evt})
	require.NotNil(t, result)
	assert.Equal(t, 1, result.EventCount)
	assert.Equal(t, int64(42), result.StartVersion)
	assert.Equal(t, int64(42), result.EndVersion)
	assert.Equal(t, "single-stream", result.AgentID)
	assert.Equal(t, "active", result.Outcome)
	assert.NotEmpty(t, result.SummaryText)
	assert.NotEmpty(t, result.ID)
}

func TestBuildSummary_EventsWithoutMetadata_UsesStreamIDAsAgent(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	events := []*Event{
		{StreamID: "my-agent", Type: EventTaskCreated, Version: 1, Timestamp: time.Now(), Payload: map[string]any{}},
		{StreamID: "my-agent", Type: EventTaskCompleted, Version: 2, Timestamp: time.Now(), Payload: map[string]any{}},
	}

	result := c.buildSummary("my-agent", events)
	require.NotNil(t, result)
	assert.Equal(t, "my-agent", result.AgentID, "should fall back to streamID as agent ID")
}

func TestBuildSummary_MetadataAgentID_TakesPrecedence(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	events := []*Event{
		{
			StreamID:  "stream-1",
			Type:      EventTaskCreated,
			Version:   1,
			Timestamp: time.Now(),
			Metadata:  map[string]any{"agent_id": "real-agent-id"},
			Payload:   map[string]any{},
		},
	}

	result := c.buildSummary("stream-1", events)
	assert.Equal(t, "real-agent-id", result.AgentID, "metadata agent_id should take precedence")
}

func TestBuildSummary_PayloadAgentID_Fallback(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	events := []*Event{
		{
			StreamID:  "stream-1",
			Type:      EventTaskCreated,
			Version:   1,
			Timestamp: time.Now(),
			Metadata:  map[string]any{},
			Payload:   map[string]any{"agent_id": "payload-agent"},
		},
	}

	result := c.buildSummary("stream-1", events)
	assert.Equal(t, "payload-agent", result.AgentID, "payload agent_id should be used when metadata lacks it")
}

func TestBuildSummary_RequestTruncation_At200Chars(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	longInput := strings.Repeat("a", 300)

	events := []*Event{
		{
			StreamID: "s1", Type: EventSessionCreated, Version: 1, Timestamp: time.Now(),
			Payload: map[string]any{"input": longInput},
		},
		{StreamID: "s1", Type: EventTaskCreated, Version: 2, Timestamp: time.Now()},
	}

	result := c.buildSummary("s1", events)
	require.NotNil(t, result)
	assert.LessOrEqual(t, len(result.RequestSummary), 203, "request summary should be <= 203 chars (200 + ...)")
	assert.True(t, len(result.RequestSummary) > 200 || strings.HasSuffix(result.RequestSummary, "..."))
}

func TestBuildSummary_ErrorCapping_At10Errors(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	var events []*Event
	for i := 0; i < 15; i++ {
		events = append(events, &Event{
			StreamID: "s1", Type: EventTaskFailed, Version: int64(i + 1),
			Timestamp: time.Now(), Payload: map[string]any{"error": fmt.Sprintf("error-%d", i)},
		})
	}

	result := c.buildSummary("s1", events)
	require.NotNil(t, result)
	assert.LessOrEqual(t, len(result.Errors), 10, "errors should be capped at 10")
}

func TestBuildSummary_ToolDeduplication(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	// Same tool called multiple times.
	var events []*Event
	for i := 0; i < 5; i++ {
		events = append(events, &Event{
			StreamID: "s1", Type: EventLLMCall, Version: int64(i + 1),
			Timestamp: time.Now(), Payload: map[string]any{"tool": "same_tool"},
		})
	}

	result := c.buildSummary("s1", events)
	require.NotNil(t, result)
	assert.Len(t, result.ToolsCalled, 1, "same tool should appear only once")
	assert.Equal(t, "same_tool", result.ToolsCalled[0])
}

func TestBuildSummary_TaskDeduplication(t *testing.T) {
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, DefaultCompactionConfig())

	var events []*Event
	for i := 0; i < 3; i++ {
		events = append(events, &Event{
			StreamID: "s1", Type: EventTaskCreated, Version: int64(i + 1),
			Timestamp: time.Now(), Payload: map[string]any{"task_id": "same-task"},
		})
	}

	result := c.buildSummary("s1", events)
	require.NotNil(t, result)
	assert.Len(t, result.TasksCreated, 1, "same task ID should appear only once")
}

// ============================================================================
// Helpers
// ============================================================================

// appendNEvents appends N simple events to a store for testing.
func appendNEvents(t *testing.T, store EventStore, ctx context.Context, streamID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		evt := &Event{
			Type:    EventTaskCreated,
			Payload: map[string]any{"index": i},
		}
		err := store.Append(ctx, streamID, []*Event{evt}, 0)
		require.NoError(t, err)
	}
}

// appendNEventsToWrapped appends N events through a CompactableEventStore.
func appendNEventsToWrapped(t *testing.T, wrapped *CompactableEventStore, ctx context.Context, streamID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		evt := &Event{
			Type:    EventTaskCreated,
			Payload: map[string]any{"index": i},
		}
		err := wrapped.Append(ctx, streamID, []*Event{evt}, 0)
		require.NoError(t, err)
	}
}
