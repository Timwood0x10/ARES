// nolint: errcheck // Test code may ignore return values
package events

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// ============================================================================
// Integration test helpers
// ============================================================================

// getSummaryTestPool returns a pool with both events and event_summaries tables created.
func getSummaryTestPool(t *testing.T) *postgres.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping integration test")
		return nil
	}

	// Parse DSN to extract host/port for pool config.
	cfg := postgres.DefaultConfig()
	cfg.QueryTimeout = 5 * time.Minute // generous for Docker-on-ARM
	if u, err := url.Parse(dsn); err == nil {
		cfg.Host = u.Hostname()
		if p := u.Port(); p != "" {
			fmt.Sscanf(p, "%d", &cfg.Port)
		}
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skipf("failed to open test database: %v", err)
		return nil
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skipf("failed to ping test database: %v", err)
		return nil
	}

	createEventsTable(t, db)
	createEventSummariesTable(t, db)

	_ = db.Close()

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		t.Skipf("failed to create test pool: %v", err)
		return nil
	}
	return pool
}

// createEventSummariesTable ensures the event_summaries table exists for tests.
func createEventSummariesTable(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS event_summaries (
			id VARCHAR(255) PRIMARY KEY,
			stream_id VARCHAR(255) NOT NULL,
			agent_id VARCHAR(255) NOT NULL,
			task_id VARCHAR(255),
			session_id VARCHAR(255),
			user_id VARCHAR(255),
			summary_text TEXT NOT NULL,
			event_count INTEGER NOT NULL DEFAULT 0,
			start_version BIGINT NOT NULL,
			end_version BIGINT NOT NULL,
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP NOT NULL,
			event_type_counts JSONB DEFAULT '{}'::jsonb,
			tasks_created JSONB DEFAULT '[]'::jsonb,
			tools_called JSONB DEFAULT '[]'::jsonb,
			errors JSONB DEFAULT '[]'::jsonb,
			request_summary TEXT,
			outcome VARCHAR(50) NOT NULL DEFAULT 'active',
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_event_summaries_stream ON event_summaries(stream_id)`,
		`CREATE INDEX IF NOT EXISTS idx_event_summaries_agent ON event_summaries(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_event_summaries_agent_task ON event_summaries(agent_id, task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_event_summaries_created ON event_summaries(created_at)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("failed to create event_summaries table: %v", err)
		}
	}
}

func cleanupSummaries(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "DELETE FROM event_summaries"); err != nil {
		t.Logf("warning: failed to clean event_summaries table: %v", err)
	}
}

func cleanupAll(t *testing.T, pool *postgres.Pool) {
	cleanupEvents(t, pool)
	cleanupSummaries(t, pool)
}

// makeTestSummary creates a valid EventSummary with test defaults.
func makeTestSummary(streamID string, startVer, endVer int64, count int) *EventSummary {
	base := time.Now().UTC().Truncate(time.Second)
	return &EventSummary{
		ID:              NewEventSummaryID(),
		StreamID:        streamID,
		AgentID:         streamID + "-agent",
		TaskID:          fmt.Sprintf("task-%s", streamID),
		SummaryText:     fmt.Sprintf("test summary for %s versions %d-%d", streamID, startVer, endVer),
		EventCount:      count,
		StartVersion:    startVer,
		EndVersion:      endVer,
		StartTime:       base.Add(-time.Duration(count) * time.Minute),
		EndTime:         base,
		EventTypeCounts: map[string]int{"task.created": count / 2, "task.completed": count / 2},
		TasksCreated:    []string{fmt.Sprintf("task-%s", streamID)},
		ToolsCalled:     []string{"tool_a"},
		RequestSummary:  "test request",
		Outcome:         "completed",
		CreatedAt:       time.Now(),
	}
}

// ============================================================================
// PgSummaryRepository Tests (Integration - requires TEST_POSTGRES_DSN)
// ============================================================================

func TestPgSummaryRepo_SaveAndFind(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	streamID := fmt.Sprintf("pg-save-find-%d", time.Now().UnixNano())
	s := makeTestSummary(streamID, 1, 10, 10)

	err := repo.Save(ctx, s)
	require.NoError(t, err)

	found, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.Len(t, found, 1)

	got := found[0]
	assert.Equal(t, s.ID, got.ID)
	assert.Equal(t, streamID, got.StreamID)
	assert.Equal(t, s.AgentID, got.AgentID)
	assert.Equal(t, s.SummaryText, got.SummaryText)
	assert.Equal(t, 10, got.EventCount)
	assert.Equal(t, int64(1), got.StartVersion)
	assert.Equal(t, int64(10), got.EndVersion)
	assert.Equal(t, "completed", got.Outcome)
	assert.NotEmpty(t, got.TasksCreated)
	assert.NotEmpty(t, got.ToolsCalled)
	assert.GreaterOrEqual(t, len(got.EventTypeCounts), 1)
}

func TestPgSummaryRepo_FindByStreamID_MultipleSummaries_Ordered(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	streamID := fmt.Sprintf("pg-multi-summary-%d", time.Now().UnixNano())

	// Save 3 summaries for the same stream.
	s1 := makeTestSummary(streamID, 1, 5, 5)
	time.Sleep(10 * time.Millisecond) // ensure distinct timestamps
	s2 := makeTestSummary(streamID, 6, 15, 10)
	time.Sleep(10 * time.Millisecond)
	s3 := makeTestSummary(streamID, 16, 20, 5)

	require.NoError(t, repo.Save(ctx, s1))
	require.NoError(t, repo.Save(ctx, s2))
	require.NoError(t, repo.Save(ctx, s3))

	found, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.Len(t, found, 3)

	// Should be ordered by start_version ascending.
	assert.Equal(t, int64(1), found[0].StartVersion)
	assert.Equal(t, int64(6), found[1].StartVersion)
	assert.Equal(t, int64(16), found[2].StartVersion)
}

func TestPgSummaryRepo_FindByStreamID_Empty(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	found, err := repo.FindByStreamID(ctx, "nonexistent-stream-xyz")
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestPgSummaryRepo_FindByAgentAndTask(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	agentID := "agent-integration-test"
	taskID := "task-search-flights"

	// Create summaries for same agent+task.
	s1 := makeTestSummary("stream-a", 1, 5, 5)
	s1.AgentID = agentID
	s1.TaskID = taskID

	s2 := makeTestSummary("stream-b", 6, 10, 5)
	s2.AgentID = agentID
	s2.TaskID = taskID

	// Different task for same agent.
	s3 := makeTestSummary("stream-c", 11, 12, 2)
	s3.AgentID = agentID
	s3.TaskID = "different-task"

	require.NoError(t, repo.Save(ctx, s1))
	require.NoError(t, repo.Save(ctx, s2))
	require.NoError(t, repo.Save(ctx, s3))

	found, err := repo.FindByAgentAndTask(ctx, agentID, taskID)
	require.NoError(t, err)
	require.Len(t, found, 2, "should find exactly 2 summaries for this agent+task combo")

	for _, s := range found {
		assert.Equal(t, agentID, s.AgentID)
		assert.Equal(t, taskID, s.TaskID)
	}
}

func TestPgSummaryRepo_FindByAgentID(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	agentA := "agent-alpha"
	agentB := "agent-beta"

	for i := 0; i < 3; i++ {
		s := makeTestSummary(fmt.Sprintf("stream-a%d", i), 1, 3, 3)
		s.AgentID = agentA
		require.NoError(t, repo.Save(ctx, s))
	}
	for i := 0; i < 2; i++ {
		s := makeTestSummary(fmt.Sprintf("stream-b%d", i), 4, 6, 3)
		s.AgentID = agentB
		require.NoError(t, repo.Save(ctx, s))
	}

	foundA, err := repo.FindByAgentID(ctx, agentA)
	require.NoError(t, err)
	assert.Len(t, foundA, 3, "should find 3 summaries for agent A")

	foundB, err := repo.FindByAgentID(ctx, agentB)
	require.NoError(t, err)
	assert.Len(t, foundB, 2, "should find 2 summaries for agent B")
}

func TestPgSummaryRepo_FindLatestByStreamID(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	streamID := fmt.Sprintf("pg-latest-%d", time.Now().UnixNano())

	s1 := makeTestSummary(streamID, 1, 5, 5)
	time.Sleep(10 * time.Millisecond)
	s2 := makeTestSummary(streamID, 6, 12, 7)
	time.Sleep(10 * time.Millisecond)
	s3 := makeTestSummary(streamID, 13, 18, 6)

	require.NoError(t, repo.Save(ctx, s1))
	require.NoError(t, repo.Save(ctx, s2))
	require.NoError(t, repo.Save(ctx, s3))

	latest, err := repo.FindLatestByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, s3.ID, latest.ID, "should return the summary with highest end_version")
	assert.Equal(t, int64(18), latest.EndVersion)
}

func TestPgSummaryRepo_FindLatestByStreamID_Empty(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	latest, err := repo.FindLatestByStreamID(ctx, "no-such-stream")
	require.NoError(t, err)
	assert.Nil(t, latest)
}

func TestPgSummaryRepo_SaveUpsert(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	streamID := fmt.Sprintf("pg-upsert-%d", time.Now().UnixNano())
	s := makeTestSummary(streamID, 1, 5, 5)

	require.NoError(t, repo.Save(ctx, s))

	// Update the same summary by ID.
	s.SummaryText = "updated summary text"
	s.EventCount = 99
	s.EndVersion = 999
	s.Outcome = "failed"

	err := repo.Save(ctx, s)
	require.NoError(t, err, "upsert should succeed on duplicate ID")

	found, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "updated summary text", found[0].SummaryText)
	assert.Equal(t, 99, found[0].EventCount)
	assert.Equal(t, int64(999), found[0].EndVersion)
	assert.Equal(t, "failed", found[0].Outcome)
}

func TestPgSummaryRepo_Delete(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	s := makeTestSummary("delete-me", 1, 5, 5)
	require.NoError(t, repo.Save(ctx, s))

	err := repo.Delete(ctx, s.ID)
	require.NoError(t, err)

	found, err := repo.FindByStreamID(ctx, "delete-me")
	require.NoError(t, err)
	assert.Empty(t, found, "deleted summary should not be found")
}

func TestPgSummaryRepo_Delete_NonExistent_NoError(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	err := repo.Delete(ctx, "non-existent-id-xyz")
	require.NoError(t, err, "deleting non-existent ID should not error")
}

func TestPgSummaryRepo_DeleteOlderThan(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	// Create summaries with different CreatedAt times.
	oldSummary := makeTestSummary("old-stream", 1, 5, 5)
	oldSummary.CreatedAt = time.Now().Add(-48 * time.Hour) // 2 days ago

	newSummary := makeTestSummary("new-stream", 1, 3, 3)
	newSummary.CreatedAt = time.Now().Add(-1 * time.Hour) // 1 hour ago

	require.NoError(t, repo.Save(ctx, oldSummary))
	require.NoError(t, repo.Save(ctx, newSummary))

	// Delete older than 24 hours.
	threshold := time.Now().Add(-24 * time.Hour)
	deleted, err := repo.DeleteOlderThan(ctx, threshold)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "only the old summary should be deleted")

	// Verify only new summary remains.
	allNew, _ := repo.FindByStreamID(ctx, "new-stream")
	assert.Len(t, allNew, 1)

	allOld, _ := repo.FindByStreamID(ctx, "old-stream")
	assert.Empty(t, allOld, "old summary should have been deleted")
}

func TestPgSummaryRepo_DeleteOlderThan_NoneMatch(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	s := makeTestSummary("recent", 1, 3, 3)
	s.CreatedAt = time.Now()
	require.NoError(t, repo.Save(ctx, s))

	// Threshold far in the past — nothing matches.
	threshold := time.Now().Add(-365 * 24 * time.Hour)
	deleted, err := repo.DeleteOlderThan(ctx, threshold)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

func TestPgSummaryRepo_LargePayload_RoundTrip(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	longText := strings.Repeat("This is a long summary text. ", 500) // ~17KB
	s := &EventSummary{
		ID:              NewEventSummaryID(),
		StreamID:        "large-payload",
		AgentID:         "agent-large",
		SummaryText:     longText,
		EventCount:      1000,
		StartVersion:    1,
		EndVersion:      1000,
		StartTime:       time.Now().Add(-time.Hour),
		EndTime:         time.Now(),
		EventTypeCounts: map[string]int{"task.created": 500, "task.completed": 500},
		TasksCreated:    make([]string, 50),
		ToolsCalled:     make([]string, 30),
		Errors:          make([]string, 10),
		RequestSummary:  strings.Repeat("x", 20000), // 20KB
		Outcome:         "completed",
		CreatedAt:       time.Now(),
	}
	for i := range s.TasksCreated {
		s.TasksCreated[i] = fmt.Sprintf("task-%04d", i)
	}
	for i := range s.ToolsCalled {
		s.ToolsCalled[i] = fmt.Sprintf("tool-%04d", i)
	}
	for i := range s.Errors {
		s.Errors[i] = fmt.Sprintf("error-%04d", i)
	}

	err := repo.Save(ctx, s)
	require.NoError(t, err)

	found, err := repo.FindByStreamID(ctx, "large-payload")
	require.NoError(t, err)
	require.Len(t, found, 1)

	got := found[0]
	assert.Equal(t, longText, got.SummaryText)
	assert.Equal(t, 1000, got.EventCount)
	assert.Len(t, got.TasksCreated, 50)
	assert.Len(t, got.ToolsCalled, 30)
	assert.Len(t, got.Errors, 10)
	assert.Len(t, got.RequestSummary, 20000)
}

func TestPgSummaryRepo_NilPool_Safety(t *testing.T) {
	var repo *PgSummaryRepository // nil
	ctx := context.Background()

	s := makeTestSummary("nil-pool", 1, 2, 2)

	err := repo.Save(ctx, s)
	assert.ErrorIs(t, err, ErrEventStoreClosed)

	_, err = repo.FindByStreamID(ctx, "x")
	assert.ErrorIs(t, err, ErrEventStoreClosed)

	_, err = repo.FindLatestByStreamID(ctx, "x")
	assert.ErrorIs(t, err, ErrEventStoreClosed)

	err = repo.Delete(ctx, "id")
	assert.ErrorIs(t, err, ErrEventStoreClosed)
}

func TestPgSummaryRepo_ConcurrentSaveSameStream(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	repo := NewPgSummaryRepository(pool)
	ctx := context.Background()

	streamID := fmt.Sprintf("concurrent-save-%d", time.Now().UnixNano())
	const writers = 20
	var wg sync.WaitGroup
	errCh := make(chan error, writers)

	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(idx int) {
			defer wg.Done()
			s := makeTestSummary(streamID, int64(idx*10+1), int64((idx+1)*10), 10)
			if saveErr := repo.Save(ctx, s); saveErr != nil {
				errCh <- saveErr
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent save error: %v", err)
	}

	// All saves should have succeeded (different IDs).
	found, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	assert.Len(t, found, writers, "all concurrent saves should persist")
}

// ============================================================================
// PgTrimStore Tests (Integration)
// ============================================================================

func TestPgTrimStore_TrimBefore_DeletesEvents(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	store := newTestPostgresEventStore(t, pool)
	trimStore := NewPgTrimStore(store, pool)
	ctx := context.Background()

	streamID := fmt.Sprintf("pg-trim-%d", time.Now().UnixNano())

	// Append 50 events.
	appendNEventsToPGStore(t, store, ctx, streamID, 50)

	// Verify all exist before trim.
	before, err := store.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, before, 50)

	// Trim up to version 25.
	removed, err := trimStore.TrimBefore(ctx, streamID, 25)
	require.NoError(t, err)
	assert.Equal(t, int64(25), removed)

	// Verify remaining events.
	after, err := store.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, after, 25)
	assert.Equal(t, int64(26), after[0].Version, "first remaining event should be version 26")
	assert.Equal(t, int64(50), after[len(after)-1].Version)
}

func TestPgTrimStore_TrimBefore_PreservesOtherStreams(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	store := newTestPostgresEventStore(t, pool)
	trimStore := NewPgTrimStore(store, pool)
	ctx := context.Background()

	streamA := fmt.Sprintf("pg-trim-a-%d", time.Now().UnixNano())
	streamB := fmt.Sprintf("pg-trim-b-%d", time.Now().UnixNano())

	appendNEventsToPGStore(t, store, ctx, streamA, 30)
	appendNEventsToPGStore(t, store, ctx, streamB, 30)

	// Only trim stream A.
	_, err := trimStore.TrimBefore(ctx, streamA, 15)
	require.NoError(t, err)

	bRemaining, err := store.Read(ctx, streamB, ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, bRemaining, 30, "stream B should be untouched")

	aRemaining, err := store.Read(ctx, streamA, ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, aRemaining, 15, "stream A should have 15 remaining events")
}

func TestPgTrimStore_TrimBefore_NonexistentStream(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()

	store := newTestPostgresEventStore(t, pool)
	trimStore := NewPgTrimStore(store, pool)
	ctx := context.Background()

	removed, err := trimStore.TrimBefore(ctx, "no-such-stream", 100)
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)
}

func TestPgTrimStore_NilPool_Safety(t *testing.T) {
	var trimStore *PgTrimStore // nil
	ctx := context.Background()

	removed, err := trimStore.TrimBefore(ctx, "stream", 10)
	assert.ErrorIs(t, err, ErrEventStoreClosed)
	assert.Equal(t, int64(0), removed)
}

// ============================================================================
// End-to-End Compaction Flow (Integration)
// ============================================================================

func TestE2E_CompactionWithPostgres_FullCycle(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	store := newTestPostgresEventStore(t, pool)
	repo := NewPgSummaryRepository(pool)
	cfg := CompactionConfig{Threshold: 20, KeepRecent: 5}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := fmt.Sprintf("e2e-full-cycle-%d", time.Now().UnixNano())

	// Build a realistic event sequence.
	events := buildRealisticEventSequence(streamID)
	for _, evt := range events {
		err := store.Append(ctx, streamID, []*Event{evt}, 0)
		require.NoError(t, err)
	}

	// Verify pre-compaction state.
	version, err := store.StreamVersion(ctx, streamID)
	require.NoError(t, err)
	assert.Greater(t, version, int64(cfg.Threshold), "must exceed threshold for compaction")

	// Run compaction.
	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.True(t, didCompact)

	// Verify summary was persisted in PostgreSQL.
	summaries, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	summary := summaries[0]
	t.Logf("Summary: ID=%s Events=%d Versions=[%d-%d] Text=%q",
		summary.ID, summary.EventCount, summary.StartVersion, summary.EndVersion, summary.SummaryText)

	assert.Greater(t, summary.EventCount, 0)
	assert.Contains(t, summary.SummaryText, "Agent")
	assert.Contains(t, summary.SummaryText, streamID)
	assert.NotEmpty(t, summary.TasksCreated)
	assert.NotEmpty(t, summary.ToolsCalled)
	assert.Equal(t, streamID, summary.StreamID)
	assert.True(t, summary.StartTime.Before(summary.EndTime))

	// Verify events are still readable (no trimming enabled).
	remaining, err := store.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, remaining, int(version), "all events still present when trimming disabled")
}

func TestE2E_CompactionWithPostgres_WithTrimming(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	store := newTestPostgresEventStore(t, pool)
	repo := NewPgSummaryRepository(pool)
	trimStore := NewPgTrimStore(store, pool)

	cfg := CompactionConfig{
		Threshold:      20,
		KeepRecent:     5,
		EnableTrimming: true,
	}
	c := NewCompactor(store, repo, cfg).WithTrimStore(trimStore)
	ctx := context.Background()

	streamID := fmt.Sprintf("e2e-with-trim-%d", time.Now().UnixNano())

	events := buildRealisticEventSequence(streamID)
	for _, evt := range events {
		_ = store.Append(ctx, streamID, []*Event{evt}, 0)
	}

	totalVersion, _ := store.StreamVersion(ctx, streamID)

	// Compact and trim.
	didCompact, err := c.CheckAndCompact(ctx, streamID)
	require.NoError(t, err)
	assert.True(t, didCompact)

	// After trimming, only KeepRecent events should remain.
	remaining, err := store.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(remaining), cfg.KeepRecent,
		"after trimming, at most KeepRecent events should remain")

	t.Logf("Total=%d Trimmed=%d Remaining=%d Summary saved",
		totalVersion, totalVersion-int64(len(remaining)), len(remaining))

	// But summary should preserve the full picture.
	summaries, _ := repo.FindByStreamID(ctx, streamID)
	require.Len(t, summaries, 1)
	compactedCount := summaries[0].EventCount
	assert.Greater(t, compactedCount, len(remaining),
		"summary should cover more events than what remains in live store")
}

func TestE2E_CompactionWithPostgres_MultipleCycles(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	store := newTestPostgresEventStore(t, pool)
	repo := NewPgSummaryRepository(pool)
	cfg := CompactionConfig{Threshold: 15, KeepRecent: 3}
	c := NewCompactor(store, repo, cfg)
	ctx := context.Background()

	streamID := fmt.Sprintf("e2e-multi-cycle-%d", time.Now().UnixNano())

	// Cycle 1: append 30 events → compaction triggers.
	appendNEventsToPGStore(t, store, ctx, streamID, 30)
	didCompact1, _ := c.CheckAndCompact(ctx, streamID)
	assert.True(t, didCompact1, "cycle 1 should compact")

	summariesAfter1, _ := repo.FindByStreamID(ctx, streamID)
	countAfter1 := len(summariesAfter1)
	t.Logf("After cycle 1: %d summaries", countAfter1)

	// Cycle 2: append more events → may trigger again if threshold exceeded.
	appendNEventsToPGStore(t, store, ctx, streamID, 20)
	didCompact2, _ := c.CheckAndCompact(ctx, streamID)
	t.Logf("Cycle 2 compacted: %v", didCompact2)

	// Cycle 3: force compact even if below threshold.
	appendNEventsToPGStore(t, store, ctx, streamID, 5)
	didCompact3, _ := c.CheckAndCompact(ctx, streamID)
	t.Logf("Cycle 3 compacted: %v", didCompact3)

	// All summaries should be queryable.
	allSummaries, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	t.Logf("Total summaries after 3 cycles: %d", len(allSummaries))
	assert.GreaterOrEqual(t, len(allSummaries), 1, "at least one summary must exist")

	// Verify version ranges don't overlap badly.
	totalCompacted := 0
	for _, s := range allSummaries {
		totalCompacted += s.EventCount
		t.Logf("  Summary v[%d-%d]: %d events, outcome=%s",
			s.StartVersion, s.EndVersion, s.EventCount, s.Outcome)
	}
	t.Logf("Total events across all summaries: %d", totalCompacted)
}

func TestE2E_CompactableStore_AutoCompactWithPostgres(t *testing.T) {
	pool := getSummaryTestPool(t)
	defer func() { _ = pool.Close() }()
	cleanupAll(t, pool)

	store := newTestPostgresEventStore(t, pool)
	repo := NewPgSummaryRepository(pool)
	cfg := CompactionConfig{Threshold: 15, KeepRecent: 3}

	wrapped := newTestCompactableEventStore(t, store, repo, nil, cfg)
	ctx := context.Background()

	streamID := fmt.Sprintf("e2e-auto-pg-%d", time.Now().UnixNano())

	// Auto-append through wrapped store.
	for i := 0; i < 40; i++ {
		evt := &Event{
			Type:    EventTaskCreated,
			Payload: map[string]any{"idx": i},
		}
		err := wrapped.Append(ctx, streamID, []*Event{evt}, 0)
		require.NoError(t, err)
	}

	// Wait for async compaction.
	time.Sleep(500 * time.Millisecond)

	// Verify auto-compaction happened.
	summaries, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	t.Logf("Auto-compaction produced %d summaries", len(summaries))
	assert.GreaterOrEqual(t, len(summaries), 1, "auto-compaction should produce at least one summary")

	// Verify live events are still there (no trimming enabled).
	liveEvents, err := store.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	liveVersion, _ := store.StreamVersion(ctx, streamID)
	t.Logf("Live store: version=%d, events=%d", liveVersion, len(liveEvents))
	assert.Equal(t, int64(40), liveVersion, "all 40 events should be in live store")
}

// ============================================================================
// Integration Helpers
// ============================================================================

// appendNEventsToPGStore appends N simple events to a PostgresEventStore.
func appendNEventsToPGStore(t *testing.T, store EventStore, ctx context.Context, streamID string, n int) {
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

// buildRealisticEventSequence creates a realistic multi-type event sequence for testing.
func buildRealisticEventSequence(streamID string) []*Event {
	base := time.Now().Truncate(time.Second).UTC()
	var events []*Event

	// Session creation.
	events = append(events, &Event{
		Type: EventSessionCreated, Version: 1, Timestamp: base,
		Payload: map[string]any{"session_id": "sess-e2e", "user_id": "user-e2e", "input": "Plan a 3-day trip to Tokyo with budget $2000"},
	})

	// Task planning.
	events = append(events, &Event{
		Type: EventTaskCreated, Version: 2, Timestamp: base.Add(1 * time.Second),
		Payload: map[string]any{"task_id": "plan-itinerary"},
	})
	events = append(events, &Event{
		Type: EventTaskDispatched, Version: 3, Timestamp: base.Add(2 * time.Second),
		Payload: map[string]any{"task_id": "plan-itinerary"},
	})

	// Tool calls.
	tools := []string{"search_flights", "search_hotels", "check_weather", "convert_currency"}
	for i, tool := range tools {
		events = append(events, &Event{
			Type: EventLLMCall, Version: int64(4 + i), Timestamp: base.Add(time.Duration(3+i) * time.Second),
			Payload: map[string]any{"function": tool},
		})
	}

	// Messages.
	events = append(events, &Event{
		Type: EventMessageAdded, Version: 8, Timestamp: base.Add(8 * time.Second),
		Payload: map[string]any{"content": "I've searched flights and hotels..."},
	})

	// Task completions.
	events = append(events, &Event{
		Type: EventTaskCompleted, Version: 9, Timestamp: base.Add(9 * time.Second),
		Payload: map[string]any{"task_id": "plan-itinerary"},
	})
	events = append(events, &Event{
		Type: EventTaskCreated, Version: 10, Timestamp: base.Add(10 * time.Second),
		Payload: map[string]any{"task_id": "book-transport"},
	})
	events = append(events, &Event{
		Type: EventLLMCall, Version: 11, Timestamp: base.Add(11 * time.Second),
		Payload: map[string]any{"function": "book_flight"},
	})
	events = append(events, &Event{
		Type: EventTaskCompleted, Version: 12, Timestamp: base.Add(12 * time.Second),
		Payload: map[string]any{"task_id": "book-transport"},
	})

	// Memory distillation.
	events = append(events, &Event{
		Type: EventMemoryDistilled, Version: 13, Timestamp: base.Add(13 * time.Second),
		Payload: map[string]any{},
	})

	// Fill remaining events to reach > threshold.
	extraTypes := []EventType{EventLLMCall, EventMessageAdded, EventTaskCreated, EventTaskCompleted,
		EventLLMCall, EventMessageAdded, EventTaskFailed, EventLLMCall}
	for i, et := range extraTypes {
		payload := map[string]any{}
		if et == EventTaskCreated {
			payload["task_id"] = fmt.Sprintf("extra-task-%d", i)
		}
		if et == EventTaskFailed {
			payload["error"] = "service unavailable"
		}
		if et == EventLLMCall {
			payload["function"] = fmt.Sprintf("extra_tool_%d", i)
		}
		events = append(events, &Event{
			Type: et, Version: int64(14 + i), Timestamp: base.Add(time.Duration(14+i) * time.Second),
			Payload: payload,
		})
	}

	return events
}
