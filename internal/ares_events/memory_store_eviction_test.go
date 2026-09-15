package ares_events

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F-04 regression: the memory EventStore must not grow without bound. A
// serve process misconfigured onto memory mode (no PG retention cleaner is
// wired) previously appended forever until OOM; the store now sheds its
// oldest events once it exceeds maxEvents.

// TestEvictOverCapShedsOldestEvents pins the cap contract: appending past
// maxEvents evicts the OLDEST events (newest always survive), per-stream
// slices shed exactly their own evicted prefix, and version counters are NOT
// rewound — a read over the evicted range returns nothing rather than a
// replayed-from-zero history.
func TestEvictOverCapShedsOldestEvents(t *testing.T) {
	store := NewMemoryEventStore()
	store.maxEvents = 10
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		require.NoError(t, store.Append(ctx, "s1", []*Event{{StreamID: "s1", Type: "t"}}, 0))
	}

	assert.Len(t, store.events, 10, "the global log must stay at the cap")

	// Newest events survive: the retained tail is versions 16..25.
	got, err := store.Read(ctx, "s1", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, got, 10)
	assert.Equal(t, int64(16), got[0].Version, "oldest retained event must be the one just past the shed boundary")
	assert.Equal(t, int64(25), got[len(got)-1].Version, "the newest event must always survive eviction")

	// Version counters are not rewound: a read over the evicted range is
	// empty, not a renumbered replay.
	evictedRead, err := store.Read(ctx, "s1", ReadOptions{ToVersion: 15})
	require.NoError(t, err)
	assert.Empty(t, evictedRead, "reads over the evicted range must return nothing, not renumbered events")

	version, err := store.StreamVersion(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(25), version, "eviction must not rewind the stream version counter")

	assert.EqualValues(t, 15, store.Stats()["evicted_events"], "shed events must be counted for monitoring")
}

// TestEvictOverCapShedsAcrossStreams pins the multi-stream case: the cap is
// global, and each stream loses only its own prefix — one stream's eviction
// must never touch another stream's retained events.
func TestEvictOverCapShedsAcrossStreams(t *testing.T) {
	store := NewMemoryEventStore()
	store.maxEvents = 10
	ctx := context.Background()

	for i := 0; i < 8; i++ {
		require.NoError(t, store.Append(ctx, "s1", []*Event{{StreamID: "s1", Type: "t"}}, 0))
	}
	// s2's appends push the global log past the cap; eviction is global FIFO,
	// so the OLDEST events — all from s1 — shed first, and s2 (newest) is
	// untouched until s1 runs out.
	for i := 0; i < 8; i++ {
		require.NoError(t, store.Append(ctx, "s2", []*Event{{StreamID: "s2", Type: "t"}}, 0))
	}

	assert.Len(t, store.events, 10, "the cap is global across streams")

	s1, err := store.Read(ctx, "s1", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, s1, 2, "s1, written first, sheds down first under global FIFO")
	assert.Equal(t, int64(7), s1[0].Version, "s1 sheds exactly its own oldest prefix")

	s2, err := store.Read(ctx, "s2", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, s2, 8, "s2, written last, is untouched while older streams still overflow")
	assert.Equal(t, int64(1), s2[0].Version, "s2 retains its full version range")

	// Appends continue on top of unrewound versions after eviction.
	require.NoError(t, store.Append(ctx, "s1", []*Event{{StreamID: "s1", Type: "t"}}, 0))
	version, err := store.StreamVersion(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(9), version, "post-eviction appends continue the original version sequence")
}

// TestEvictOverCapDropsFullyShedStream pins the map-cleanup edge: a stream
// whose events are all evicted is removed from the streams map entirely, not
// left as an empty slice that would read as "stream exists but is empty"
// forever.
func TestEvictOverCapDropsFullyShedStream(t *testing.T) {
	store := NewMemoryEventStore()
	store.maxEvents = 4
	ctx := context.Background()

	require.NoError(t, store.Append(ctx, "old", []*Event{{StreamID: "old", Type: "t"}}, 0))
	for i := 0; i < 5; i++ {
		require.NoError(t, store.Append(ctx, "new", []*Event{{StreamID: "new", Type: "t"}}, 0))
	}

	_, exists := store.streams["old"]
	assert.False(t, exists, "a fully evicted stream must be removed from the streams map")

	_, err := store.Read(ctx, "old", ReadOptions{})
	require.NoError(t, err, "reading a fully evicted stream is empty, not an error")
}
