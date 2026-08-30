// nolint: errcheck // Test code may ignore return values
package ares_events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSummary builds an EventSummary with the fields the memory repository
// indexes on, so ordering/matching assertions are deterministic.
func newTestSummary(id, streamID, agentID, taskID string, startVersion int64, createdAt time.Time) *EventSummary {
	return &EventSummary{
		ID:           id,
		StreamID:     streamID,
		AgentID:      agentID,
		TaskID:       taskID,
		SummaryText:  "summary " + id,
		EventCount:   3,
		StartVersion: startVersion,
		EndVersion:   startVersion + 9,
		CreatedAt:    createdAt,
	}
}

func TestMemorySummaryRepositorySaveAndFindByStreamID(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()
	base := time.Now()

	// Save in non-chronological order; index must return them start_version asc.
	require.NoError(t, r.Save(ctx, newTestSummary("s2", "stream-a", "agent-a", "task-1", 20, base)))
	require.NoError(t, r.Save(ctx, newTestSummary("s1", "stream-a", "agent-a", "task-1", 10, base.Add(-time.Minute))))
	require.NoError(t, r.Save(ctx, newTestSummary("s0", "stream-a", "agent-a", "task-1", 0, base.Add(-2*time.Minute))))

	got, err := r.FindByStreamID(ctx, "stream-a")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []int64{0, 10, 20}, []int64{got[0].StartVersion, got[1].StartVersion, got[2].StartVersion})

	// Empty stream returns an empty slice, not nil/error.
	empty, err := r.FindByStreamID(ctx, "missing")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestMemorySummaryRepositorySaveUpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()

	orig := newTestSummary("s1", "stream-a", "agent-a", "task-1", 10, time.Now())
	require.NoError(t, r.Save(ctx, orig))

	// Same ID, same stream: update fields, count unchanged.
	updated := newTestSummary("s1", "stream-a", "agent-a", "task-1", 10, time.Now())
	updated.SummaryText = "updated text"
	require.NoError(t, r.Save(ctx, updated))

	got, err := r.FindByStreamID(ctx, "stream-a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "updated text", got[0].SummaryText)

	// Empty ID is a no-op (returns nil, not an error).
	assert.NoError(t, r.Save(ctx, &EventSummary{ID: ""}))
}

func TestMemorySummaryRepositorySaveMovesStreamOnIDCollision(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()

	base := time.Now()
	require.NoError(t, r.Save(ctx, newTestSummary("s1", "stream-a", "agent-a", "task-1", 10, base)))
	// Reuse s1's ID but under a different stream: old index entry removed.
	require.NoError(t, r.Save(ctx, newTestSummary("s1", "stream-b", "agent-b", "task-2", 5, base)))

	a, err := r.FindByStreamID(ctx, "stream-a")
	require.NoError(t, err)
	assert.Empty(t, a, "old stream must drop the moved summary")

	b, err := r.FindByStreamID(ctx, "stream-b")
	require.NoError(t, err)
	require.Len(t, b, 1)
	assert.Equal(t, "s1", b[0].ID)
}

func TestMemorySummaryRepositoryFindByAgentAndTask(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()
	base := time.Now()

	require.NoError(t, r.Save(ctx, newTestSummary("a1", "stream-x", "agent-1", "task-9", 30, base)))
	require.NoError(t, r.Save(ctx, newTestSummary("a2", "stream-y", "agent-1", "task-9", 10, base)))
	require.NoError(t, r.Save(ctx, newTestSummary("a3", "stream-z", "agent-2", "task-9", 20, base)))

	got, err := r.FindByAgentAndTask(ctx, "agent-1", "task-9")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, len([]string{got[0].ID, got[1].ID})) // both agent-1 matched
	assert.Equal(t, []int64{10, 30}, []int64{got[0].StartVersion, got[1].StartVersion})

	none, err := r.FindByAgentAndTask(ctx, "agent-9", "task-9")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestMemorySummaryRepositoryFindByAgentID(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()
	base := time.Now()

	// Same agent, three summaries; most recent CreatedAt first in the result.
	require.NoError(t, r.Save(ctx, newTestSummary("f1", "s-a", "agent-1", "t1", 1, base)))
	require.NoError(t, r.Save(ctx, newTestSummary("f2", "s-b", "agent-1", "t2", 2, base.Add(time.Hour))))
	require.NoError(t, r.Save(ctx, newTestSummary("f3", "s-c", "agent-2", "t3", 3, base.Add(2*time.Hour))))

	got, err := r.FindByAgentID(ctx, "agent-1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "f2", got[0].ID) // newest first
	assert.Equal(t, "f1", got[1].ID)
}

func TestMemorySummaryRepositoryFindLatestByStreamID(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()
	base := time.Now()

	require.NoError(t, r.Save(ctx, newTestSummary("l1", "s-a", "agent-1", "t1", 10, base)))
	require.NoError(t, r.Save(ctx, newTestSummary("l2", "s-a", "agent-1", "t1", 20, base)))

	latest, err := r.FindLatestByStreamID(ctx, "s-a")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, "l2", latest.ID)

	// Unknown stream must return ErrSummaryNotFound.
	_, err = r.FindLatestByStreamID(ctx, "missing")
	assert.ErrorIs(t, err, ErrSummaryNotFound)
}

func TestMemorySummaryRepositoryDelete(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()
	base := time.Now()

	require.NoError(t, r.Save(ctx, newTestSummary("d1", "s-a", "agent-1", "t1", 10, base)))
	require.NoError(t, r.Save(ctx, newTestSummary("d2", "s-a", "agent-1", "t1", 20, base)))

	// Delete d1 (middle) and the index + map both shrink.
	require.NoError(t, r.Delete(ctx, "d1"))
	got, err := r.FindByStreamID(ctx, "s-a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "d2", got[0].ID)

	// Deleting an unknown ID is a no-op, not an error.
	require.NoError(t, r.Delete(ctx, "does-not-exist"))
}

func TestMemorySummaryRepositoryDeleteOlderThan(t *testing.T) {
	ctx := context.Background()
	r := NewMemorySummaryRepository()
	now := time.Now()
	old := now.Add(-2 * time.Hour)

	require.NoError(t, r.Save(ctx, newTestSummary("old1", "s-a", "agent-1", "t1", 10, old)))
	require.NoError(t, r.Save(ctx, newTestSummary("old2", "s-a", "agent-1", "t2", 20, old)))
	require.NoError(t, r.Save(ctx, newTestSummary("new1", "s-a", "agent-1", "t3", 30, now)))

	deleted, err := r.DeleteOlderThan(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	got, err := r.FindByStreamID(ctx, "s-a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "new1", got[0].ID)

	// Second pass: nothing left to delete.
	deleted, err = r.DeleteOlderThan(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}
