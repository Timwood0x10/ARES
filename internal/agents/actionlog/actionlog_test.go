package actionlog

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_AppendAndList(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	require.NoError(t, s.Append(ctx, Entry{ID: "e1", SessionID: "sess-1", AgentID: "a1", Action: "tool.call"}))
	require.NoError(t, s.Append(ctx, Entry{ID: "e2", SessionID: "sess-1", AgentID: "a1", Action: "handoff"}))
	require.NoError(t, s.Append(ctx, Entry{ID: "e3", SessionID: "sess-2", AgentID: "b1", Action: "task.result"}))

	assert.Equal(t, 3, s.Count())

	// List filters by session, preserves append order.
	entries := s.List("sess-1")
	require.Len(t, entries, 2)
	assert.Equal(t, "e1", entries[0].ID)
	assert.Equal(t, "e2", entries[1].ID)

	// Timestamp defaulted when zero.
	assert.False(t, entries[0].Timestamp.IsZero())
}

func TestStore_AppendDuplicateRejected(t *testing.T) {
	s := NewStore()
	ctx := context.Background()
	require.NoError(t, s.Append(ctx, Entry{ID: "e1", SessionID: "s", Action: "x"}))
	err := s.Append(ctx, Entry{ID: "e1", SessionID: "s", Action: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
	assert.Equal(t, 1, s.Count())
}

func TestStore_AppendEmptyIDRejected(t *testing.T) {
	s := NewStore()
	err := s.Append(context.Background(), Entry{ID: "", SessionID: "s", Action: "x"})
	require.Error(t, err)
}

func TestStore_ReplayFromStartID(t *testing.T) {
	s := NewStore()
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c", "d"} {
		require.NoError(t, s.Append(ctx, Entry{ID: id, SessionID: "sess-1", Action: "step"}))
	}

	replay, err := s.Replay("sess-1", "b")
	require.NoError(t, err)
	require.Len(t, replay, 2, "replay after 'b' returns the following entries c and d")
	assert.Equal(t, "c", replay[0].ID)
	assert.Equal(t, "d", replay[1].ID)

	// Full replay with empty start.
	all, err := s.Replay("sess-1", "")
	require.NoError(t, err)
	assert.Len(t, all, 4)
}

func TestStore_ReplayUnknownStartID(t *testing.T) {
	s := NewStore()
	ctx := context.Background()
	require.NoError(t, s.Append(ctx, Entry{ID: "a", SessionID: "sess-1", Action: "step"}))
	_, err := s.Replay("sess-1", "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStore_ExplicitTimestampPreserved(t *testing.T) {
	s := NewStore()
	ctx := context.Background()
	ts := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.Append(ctx, Entry{ID: "e1", SessionID: "s", Action: "x", Timestamp: ts}))
	entries := s.List("s")
	require.Len(t, entries, 1)
	assert.Equal(t, ts, entries[0].Timestamp)
}
