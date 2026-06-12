package events

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryEventStore_AppendToNewStream(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	events := []*Event{
		{Type: EventAgentStarted, Payload: map[string]any{"id": "a1"}},
	}
	err := store.Append(ctx, "stream-1", events, 0)
	require.NoError(t, err)

	version, err := store.StreamVersion(ctx, "stream-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), version)
}

func TestMemoryEventStore_AppendIncrementsVersion(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	err := store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)
	require.NoError(t, err)

	err = store.Append(ctx, "s1", []*Event{{Type: EventAgentStopped}}, 1)
	require.NoError(t, err)

	version, _ := store.StreamVersion(ctx, "s1")
	assert.Equal(t, int64(2), version)
}

func TestMemoryEventStore_VersionConflict(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	err := store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)
	require.NoError(t, err)

	err = store.Append(ctx, "s1", []*Event{{Type: EventAgentStopped}}, 99)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestMemoryEventStore_AutoDetectVersion(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)
	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStopped}}, 0)

	version, _ := store.StreamVersion(ctx, "s1")
	assert.Equal(t, int64(2), version)
}

func TestMemoryEventStore_ReadAscending(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	_ = store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
		{Type: EventTaskCreated},
		{Type: EventTaskCompleted},
	}, 0)

	events, err := store.Read(ctx, "s1", ReadOptions{Direction: ReadAscending})
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, EventType("agent.started"), events[0].Type)
	assert.Equal(t, EventType("task.completed"), events[2].Type)
}

func TestMemoryEventStore_ReadDescending(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	_ = store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
		{Type: EventTaskCreated},
	}, 0)

	events, err := store.Read(ctx, "s1", ReadOptions{Direction: ReadDescending})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, int64(2), events[0].Version)
	assert.Equal(t, int64(1), events[1].Version)
}

func TestMemoryEventStore_ReadWithFromVersion(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	_ = store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
		{Type: EventTaskCreated},
		{Type: EventTaskCompleted},
	}, 0)

	events, err := store.Read(ctx, "s1", ReadOptions{FromVersion: 1})
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, int64(1), events[0].Version)
}

func TestMemoryEventStore_ReadWithLimit(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	_ = store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
		{Type: EventTaskCreated},
		{Type: EventTaskCompleted},
	}, 0)

	events, err := store.Read(ctx, "s1", ReadOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestMemoryEventStore_ReadWithSince(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Override timeNow for deterministic timestamps.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return base }
	defer func() { timeNow = func() time.Time { return time.Now() } }()

	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)

	timeNow = func() time.Time { return base.Add(time.Hour) }
	_ = store.Append(ctx, "s1", []*Event{{Type: EventTaskCreated}}, 1)

	events, err := store.Read(ctx, "s1", ReadOptions{Since: base.Add(30 * time.Minute)})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, EventType("task.created"), events[0].Type)
}

func TestMemoryEventStore_ReadAll(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)
	_ = store.Append(ctx, "s2", []*Event{{Type: EventTaskCreated}}, 0)

	events, err := store.ReadAll(ctx, ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestMemoryEventStore_SubscribeReceivesEvents(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := store.Subscribe(ctx, EventFilter{})
	require.NoError(t, err)

	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)

	select {
	case event := <-ch:
		assert.Equal(t, EventType("agent.started"), event.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestMemoryEventStore_SubscribeWithFilter(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := store.Subscribe(ctx, EventFilter{Types: []EventType{EventTaskCompleted}})
	require.NoError(t, err)

	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)
	_ = store.Append(ctx, "s1", []*Event{{Type: EventTaskCompleted}}, 1)

	select {
	case event := <-ch:
		assert.Equal(t, EventType("task.completed"), event.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for filtered event")
	}
}

func TestMemoryEventStore_SubscribeByStream(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := store.Subscribe(ctx, EventFilter{StreamIDs: []string{"s1"}})
	require.NoError(t, err)

	_ = store.Append(ctx, "s2", []*Event{{Type: EventAgentStarted}}, 0)
	_ = store.Append(ctx, "s1", []*Event{{Type: EventTaskCreated}}, 0)

	select {
	case event := <-ch:
		assert.Equal(t, "s1", event.StreamID)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for stream-filtered event")
	}
}

func TestMemoryEventStore_UnsubscribeOnContextCancel(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := store.Subscribe(ctx, EventFilter{})
	require.NoError(t, err)

	cancel()
	time.Sleep(50 * time.Millisecond) // Allow goroutine to run.

	_, ok := <-ch
	assert.False(t, ok, "channel should be closed after context cancel")
}

func TestMemoryEventStore_StreamVersionUnknown(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	version, err := store.StreamVersion(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Equal(t, int64(0), version)
}

func TestMemoryEventStore_Close(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	ch, _ := store.Subscribe(ctx, EventFilter{})

	err := store.Close()
	require.NoError(t, err)

	_, ok := <-ch
	assert.False(t, ok, "channel should be closed")

	err = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)
	assert.ErrorIs(t, err, ErrEventStoreClosed)
}

func TestMemoryEventStore_AppendEmptyStreamID(t *testing.T) {
	store := NewMemoryEventStore()
	err := store.Append(context.Background(), "", []*Event{{Type: EventAgentStarted}}, 0)
	assert.ErrorIs(t, err, ErrStreamNotFound)
}

func TestMemoryEventStore_AppendEmptyEvents(t *testing.T) {
	store := NewMemoryEventStore()
	err := store.Append(context.Background(), "s1", []*Event{}, 0)
	assert.NoError(t, err)
}

func TestMemoryEventStore_AppendNilEvent(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	err := store.Append(ctx, "s1", []*Event{nil, {Type: EventAgentStarted}}, 0)
	require.NoError(t, err)

	events, _ := store.Read(ctx, "s1", ReadOptions{})
	assert.Len(t, events, 1)
}

func TestMemoryEventStore_ConcurrentAppend(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			streamID := "stream-" + string(rune('A'+id))
			_ = store.Append(ctx, streamID, []*Event{{Type: EventAgentStarted}}, 0)
		}(i)
	}
	wg.Wait()

	version, _ := store.StreamVersion(ctx, "stream-A")
	assert.Equal(t, int64(1), version)
}

func TestMemoryEventStore_ConcurrentAppendSameStream(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// First event to establish the stream.
	_ = store.Append(ctx, "s1", []*Event{{Type: EventAgentStarted}}, 0)

	// Concurrent appends to same stream with expectedVersion=1.
	// Only one should succeed due to version conflict.
	var wg sync.WaitGroup
	successes := 0
	var mu sync.Mutex
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.Append(ctx, "s1", []*Event{{Type: EventTaskCreated}}, 1)
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Exactly one should have succeeded (the rest get ErrVersionConflict).
	assert.Equal(t, 1, successes)
}

func TestMemoryEventStore_ReadEmptyStream(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	events, err := store.Read(ctx, "nonexistent", ReadOptions{})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestMemoryEventStore_ReadAllEmpty(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	events, err := store.ReadAll(ctx, ReadOptions{})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestAppend_VersionOverflow(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Set the stream version to max int64 - 1 so the second event overflows.
	store.mu.Lock()
	store.versions["overflow-stream"] = math.MaxInt64 - 1
	store.mu.Unlock()

	// Appending 3 events: version 1 = MaxInt64, version 2 = MaxInt64+1 overflows.
	events := []*Event{
		{Type: EventAgentStarted},
		{Type: EventTaskCreated},
		{Type: EventTaskCompleted},
	}
	err := store.Append(ctx, "overflow-stream", events, math.MaxInt64-1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version overflow")
}
