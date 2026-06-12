package events

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppend_LargePayload verifies that a 1 MB payload is stored and retrieved correctly.
func TestAppend_LargePayload(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Build a 1 MB string payload.
	payload := map[string]any{
		"data": strings.Repeat("x", 1024*1024),
	}

	err := store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted, Payload: payload},
	}, 0)
	require.NoError(t, err)

	events, err := store.Read(ctx, "s1", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, events, 1)

	data, ok := events[0].Payload["data"].(string)
	require.True(t, ok, "payload data should be a string")
	assert.Len(t, data, 1024*1024, "payload should be 1 MB")
}

// TestAppend_VersionNearMax verifies behavior when the stream version is near int64 max.
func TestAppend_VersionNearMax(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Pre-set the stream version to a very large value.
	store.mu.Lock()
	store.versions["near-max-stream"] = math.MaxInt64 - 10
	store.mu.Unlock()

	// Append a single event to bring the version closer to max.
	err := store.Append(ctx, "near-max-stream", []*Event{
		{Type: EventAgentStarted},
	}, math.MaxInt64-10)
	require.NoError(t, err)

	version, err := store.StreamVersion(ctx, "near-max-stream")
	require.NoError(t, err)
	assert.Equal(t, int64(math.MaxInt64-9), version)

	// Verify reading still works at high version numbers.
	events, err := store.Read(ctx, "near-max-stream", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(math.MaxInt64-9), events[0].Version)
}

// TestSubscribe_1000Subscribers verifies the store handles many subscribers.
func TestSubscribe_1000Subscribers(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create 1000 subscribers.
	channels := make([]<-chan *Event, 1000)
	for i := 0; i < 1000; i++ {
		ch, err := store.Subscribe(ctx, EventFilter{Types: []EventType{EventTaskCreated}})
		require.NoError(t, err)
		channels[i] = ch
	}

	// Publish one event.
	err := store.Append(ctx, "s1", []*Event{
		{Type: EventTaskCreated, Payload: map[string]any{"id": "broadcast"}},
	}, 0)
	require.NoError(t, err)

	// Verify all subscribers received the event (with timeout).
	for i, ch := range channels {
		select {
		case event := <-ch:
			assert.Equal(t, EventTaskCreated, event.Type, "subscriber %d", i)
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d timed out", i)
		}
	}
}

// TestSubscribe_CloseAfterSubscribe verifies that closing the store closes all subscriber channels.
func TestSubscribe_CloseAfterSubscribe(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Create multiple subscribers.
	channels := make([]<-chan *Event, 10)
	for i := 0; i < 10; i++ {
		ch, err := store.Subscribe(ctx, EventFilter{})
		require.NoError(t, err)
		channels[i] = ch
	}

	// Close the store.
	err := store.Close()
	require.NoError(t, err)

	// All channels must be closed.
	for i, ch := range channels {
		_, ok := <-ch
		assert.False(t, ok, "subscriber channel %d should be closed after store close", i)
	}
}

// TestRead_LargeLimit verifies that a limit larger than total events returns all events.
func TestRead_LargeLimit(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Append 5 events.
	events := make([]*Event, 5)
	for i := range events {
		events[i] = &Event{Type: EventTaskCreated}
	}
	err := store.Append(ctx, "s1", events, 0)
	require.NoError(t, err)

	// Read with a limit much larger than the stream size.
	result, err := store.Read(ctx, "s1", ReadOptions{Limit: 9999})
	require.NoError(t, err)
	assert.Len(t, result, 5, "should return all events when limit exceeds stream size")
}

// TestConcurrentAppend_SameStream verifies that only one goroutine wins when appending
// concurrently to the same stream with the same expected version.
func TestConcurrentAppend_SameStream(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Pre-create the stream at version 1.
	err := store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
	}, 0)
	require.NoError(t, err)

	// 100 goroutines race to append with expectedVersion=1.
	// Only one can succeed because the mutex serializes and the first
	// winner increments the version, causing ErrVersionConflict for the rest.
	const goroutines = 100
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			appendErr := store.Append(ctx, "s1", []*Event{
				{Type: EventTaskCreated, Payload: map[string]any{"goroutine": idx}},
			}, 1)
			if appendErr == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, successCount, "exactly one goroutine should succeed")

	// The stream should now be at version 2.
	version, err := store.StreamVersion(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), version)
}

// TestReadAll_EmptyStore verifies that ReadAll on an empty store returns an empty result.
func TestReadAll_EmptyStore(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	events, err := store.ReadAll(ctx, ReadOptions{})
	require.NoError(t, err)
	assert.Empty(t, events, "empty store should return empty result")
}

// TestAppend_DuplicateID verifies that appending events with duplicate IDs does not
// cause an error; the store does not enforce ID uniqueness.
func TestAppend_DuplicateID(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	err := store.Append(ctx, "s1", []*Event{
		{ID: "dup-id", Type: EventAgentStarted},
	}, 0)
	require.NoError(t, err)

	// Append a second event with the same ID to a different version.
	err = store.Append(ctx, "s1", []*Event{
		{ID: "dup-id", Type: EventTaskCreated},
	}, 1)
	require.NoError(t, err)

	events, err := store.Read(ctx, "s1", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "dup-id", events[0].ID)
	assert.Equal(t, "dup-id", events[1].ID)
}

// TestSubscribe_FilterByMultipleTypes verifies subscription filtering with 3 event types.
// Events are appended and drained one at a time because the subscriber channel buffer is 1.
func TestSubscribe_FilterByMultipleTypes(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe to 3 specific types.
	filter := EventFilter{
		Types: []EventType{EventAgentStarted, EventTaskCreated, EventTaskCompleted},
	}
	ch, err := store.Subscribe(ctx, filter)
	require.NoError(t, err)

	// Event sequence: 3 matching, 2 non-matching.
	type eventEntry struct {
		eventType EventType
		matches   bool
	}
	entries := []eventEntry{
		{EventAgentStarted, true},
		{EventAgentFailed, false},
		{EventTaskCreated, true},
		{EventSessionCreated, false},
		{EventTaskCompleted, true},
	}

	// Append one event at a time and drain matching events between appends.
	// The channel buffer is 1, so we must receive before the next matching append.
	var received []EventType
	version := int64(0)
	for _, entry := range entries {
		appendErr := store.Append(ctx, "s1", []*Event{{Type: entry.eventType}}, version)
		require.NoError(t, appendErr)
		version++

		if entry.matches {
			select {
			case event := <-ch:
				received = append(received, event.Type)
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for event %s", entry.eventType)
			}
		}
	}

	assert.Equal(t, []EventType{
		EventAgentStarted,
		EventTaskCreated,
		EventTaskCompleted,
	}, received)
}

// TestRead_AfterClose verifies that reading from a closed store returns ErrEventStoreClosed.
func TestRead_AfterClose(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Append some data before closing.
	err := store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
	}, 0)
	require.NoError(t, err)

	// Close the store.
	err = store.Close()
	require.NoError(t, err)

	// Read should fail.
	_, err = store.Read(ctx, "s1", ReadOptions{})
	assert.ErrorIs(t, err, ErrEventStoreClosed)

	// ReadAll should fail.
	_, err = store.ReadAll(ctx, ReadOptions{})
	assert.ErrorIs(t, err, ErrEventStoreClosed)

	// StreamVersion should fail.
	_, err = store.StreamVersion(ctx, "s1")
	assert.ErrorIs(t, err, ErrEventStoreClosed)

	// Subscribe should fail.
	_, err = store.Subscribe(ctx, EventFilter{})
	assert.ErrorIs(t, err, ErrEventStoreClosed)
}

// TestAppend_ThenRead_PayloadIntegrity verifies that complex payloads survive a round-trip
// through append and read without data loss or corruption.
func TestAppend_ThenRead_PayloadIntegrity(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	original := map[string]any{
		"string_field": "hello world",
		"int_field":    42,
		"float_field":  3.14159,
		"bool_field":   true,
		"nil_field":    nil,
		"nested": map[string]any{
			"inner_string": "nested value",
			"inner_int":    99,
		},
		"array_field": []any{"a", "b", "c"},
	}

	err := store.Append(ctx, "s1", []*Event{
		{Type: EventMessageAdded, Payload: original},
	}, 0)
	require.NoError(t, err)

	events, err := store.Read(ctx, "s1", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, events, 1)

	result := events[0].Payload
	assert.Equal(t, "hello world", result["string_field"])
	assert.Equal(t, 42, result["int_field"])
	assert.InDelta(t, 3.14159, result["float_field"], 0.00001)
	assert.Equal(t, true, result["bool_field"])
	assert.Nil(t, result["nil_field"])

	nested, ok := result["nested"].(map[string]any)
	require.True(t, ok, "nested should be a map")
	assert.Equal(t, "nested value", nested["inner_string"])
	assert.Equal(t, 99, nested["inner_int"])

	arr, ok := result["array_field"].([]any)
	require.True(t, ok, "array_field should be a slice")
	assert.Equal(t, []any{"a", "b", "c"}, arr)
}

// TestStreamVersion_ConcurrentRead verifies that concurrent reads of stream version
// do not cause data races and always return a consistent value.
func TestStreamVersion_ConcurrentRead(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Pre-populate the stream with a known version.
	err := store.Append(ctx, "s1", []*Event{
		{Type: EventAgentStarted},
		{Type: EventTaskCreated},
	}, 0)
	require.NoError(t, err)

	const goroutines = 50
	var wg sync.WaitGroup
	versions := make([]int64, goroutines)

	// All goroutines read the version concurrently.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			v, vErr := store.StreamVersion(ctx, "s1")
			if vErr != nil {
				t.Errorf("goroutine %d: unexpected error: %v", idx, vErr)
				return
			}
			versions[idx] = v
		}(i)
	}
	wg.Wait()

	// All reads should return the same version (2).
	for i, v := range versions {
		assert.Equal(t, int64(2), v, "goroutine %d got unexpected version", i)
	}
}
