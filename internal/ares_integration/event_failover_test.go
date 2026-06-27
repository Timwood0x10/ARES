// package integration provides end-to-end integration tests for Event Sourcing
// combined with failover recovery patterns using the MemoryEventStore.
package ares_integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// emitSessionEvents emits a sequence of session lifecycle ares_events to the given stream.
// Returns the ares_events that were appended for later verification.
func emitSessionEvents(
	ctx context.Context,
	store ares_events.EventStore,
	streamID string,
	fromVersion int64,
	eventTypes []ares_events.EventType,
	payloads []map[string]any,
) ([]*ares_events.Event, error) {
	if len(eventTypes) != len(payloads) {
		return nil, fmt.Errorf("eventTypes and payloads must have equal length")
	}

	eventsList := make([]*ares_events.Event, len(eventTypes))
	for i, eventType := range eventTypes {
		eventsList[i] = &ares_events.Event{
			Type:    eventType,
			Payload: payloads[i],
		}
	}

	if err := store.Append(ctx, streamID, eventsList, fromVersion); err != nil {
		return nil, fmt.Errorf("append ares_events: %w", err)
	}

	return eventsList, nil
}

// TestEventSourcing_FailoverRecovery verifies the full failover recovery cycle:
// 1. Leader A emits session ares_events to the event store.
// 2. Leader A "crashes" (we just stop emitting).
// 3. Leader B reads the event store and replays ares_events.
// 4. Leader B verifies the session state is recovered.
func TestEventSourcing_FailoverRecovery(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	streamID := fmt.Sprintf("session-failover-%d", time.Now().UnixNano())

	// Leader A emits session lifecycle ares_events.
	eventTypes := []ares_events.EventType{
		ares_events.EventSessionCreated,
		ares_events.EventMessageAdded,
		ares_events.EventMessageAdded,
		ares_events.EventTaskCreated,
		ares_events.EventTaskCompleted,
	}
	payloads := []map[string]any{
		{"session_id": streamID, "user_id": "user-1"},
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi there"},
		{"task_id": "task-1", "type": "code_review"},
		{"task_id": "task-1", "output": "approved"},
	}

	_, err := emitSessionEvents(ctx, store, streamID, 0, eventTypes, payloads)
	require.NoError(t, err, "leader A should emit ares_events successfully")

	// Leader A "crashes". The ares_events remain in the store.

	// Leader B starts and replays all ares_events from the store.
	replayed, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, replayed, 5, "leader B should replay all 5 ares_events")

	// Verify event types match the original sequence.
	assert.Equal(t, ares_events.EventSessionCreated, replayed[0].Type)
	assert.Equal(t, ares_events.EventMessageAdded, replayed[1].Type)
	assert.Equal(t, ares_events.EventMessageAdded, replayed[2].Type)
	assert.Equal(t, ares_events.EventTaskCreated, replayed[3].Type)
	assert.Equal(t, ares_events.EventTaskCompleted, replayed[4].Type)

	// Verify payloads are preserved.
	assert.Equal(t, "user-1", replayed[0].Payload["user_id"])
	assert.Equal(t, "hello", replayed[1].Payload["content"])
	assert.Equal(t, "approved", replayed[4].Payload["output"])

	// Verify stream version is correct.
	version, err := store.StreamVersion(ctx, streamID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), version)

	// Leader B can continue emitting ares_events from where leader A left off.
	newEvents := []*ares_events.Event{
		{Type: ares_events.EventMessageAdded, Payload: map[string]any{"role": "user", "content": "follow-up"}},
	}
	err = store.Append(ctx, streamID, newEvents, 5)
	require.NoError(t, err, "leader B should append after leader A's last version")

	version, err = store.StreamVersion(ctx, streamID)
	require.NoError(t, err)
	assert.Equal(t, int64(6), version, "version should increment after leader B appends")
}

// TestEventSourcing_EventOrdering verifies that ares_events are replayed in the
// exact order they were appended, regardless of read direction options.
func TestEventSourcing_EventOrdering(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	streamID := fmt.Sprintf("session-order-%d", time.Now().UnixNano())

	const eventCount = 10
	eventsList := make([]*ares_events.Event, eventCount)
	for i := 0; i < eventCount; i++ {
		eventsList[i] = &ares_events.Event{
			Type:    ares_events.EventMessageAdded,
			Payload: map[string]any{"index": i, "content": fmt.Sprintf("message-%d", i)},
		}
	}

	// Append all ares_events in one batch.
	err := store.Append(ctx, streamID, eventsList, 0)
	require.NoError(t, err)

	// Read ascending (default): oldest to newest.
	ascending, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, ascending, eventCount)

	for i, event := range ascending {
		assert.Equal(t, int64(i+1), event.Version, "ascending event %d should have version %d", i, i+1)
		assert.Equal(t, i, event.Payload["index"], "ascending event %d should have index %d", i, i)
	}

	// Read descending: newest to oldest.
	descending, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadDescending,
	})
	require.NoError(t, err)
	require.Len(t, descending, eventCount)

	for i, event := range descending {
		expectedIndex := eventCount - 1 - i
		assert.Equal(t, expectedIndex, event.Payload["index"],
			"descending event %d should have index %d", i, expectedIndex)
	}

	// Read with limit.
	limited, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
		Limit:       3,
	})
	require.NoError(t, err)
	assert.Len(t, limited, 3, "limit should cap the number of returned ares_events")
	assert.Equal(t, 0, limited[0].Payload["index"])
	assert.Equal(t, 2, limited[2].Payload["index"])
}

// TestEventSourcing_ConcurrentAppend verifies that multiple goroutines can
// append to the same stream without data loss. Version conflicts are detected
// and the caller can retry.
func TestEventSourcing_ConcurrentAppend(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	streamID := fmt.Sprintf("session-concurrent-%d", time.Now().UnixNano())

	const numGoroutines = 5
	const eventsPerGoroutine = 10

	var wg sync.WaitGroup
	successCounts := make([]int, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			for i := 0; i < eventsPerGoroutine; i++ {
				event := &ares_events.Event{
					Type:    ares_events.EventMessageAdded,
					Payload: map[string]any{"goroutine": idx, "index": i},
				}

				// Use auto-detect version (0) to append after current version.
				// This avoids version conflicts since the store handles
				// auto-increment internally.
				err := store.Append(ctx, streamID, []*ares_events.Event{event}, 0)
				if err == nil {
					successCounts[idx]++
				}
				// Version conflicts are expected in concurrent scenarios.
				// The caller would normally retry, but for this test we
				// just count successes.
			}
		}(g)
	}

	wg.Wait()

	// Verify total ares_events written.
	totalExpected := numGoroutines * eventsPerGoroutine

	allEvents, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)

	totalSuccess := 0
	for _, count := range successCounts {
		totalSuccess += count
	}
	assert.Equal(t, totalExpected, totalSuccess, "all appends should succeed with auto-detect version")
	assert.Len(t, allEvents, totalExpected, "store should contain all appended ares_events")

	// Verify versions are sequential with no gaps.
	for i, event := range allEvents {
		assert.Equal(t, int64(i+1), event.Version,
			"event %d should have sequential version %d", i, i+1)
	}
}

// TestEventSourcing_SubscribeAndReplay verifies that subscribers receive ares_events
// in real-time and that replaying from the store produces the same sequence.
// Events are appended one at a time because the subscriber channel has buffer
// size 1, and notifySubscribers uses non-blocking sends that drop on full.
func TestEventSourcing_SubscribeAndReplay(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	streamID := fmt.Sprintf("session-subscribe-%d", time.Now().UnixNano())

	// Subscribe to ares_events on this stream before emitting.
	subCh, err := store.Subscribe(ctx, ares_events.EventFilter{
		StreamIDs: []string{streamID},
	})
	require.NoError(t, err)

	// Allow subscription goroutine to start.
	time.Sleep(50 * time.Millisecond)

	// Emit ares_events one at a time so the subscriber can consume each before
	// the next is appended (subscriber buffer is 1).
	type emittedEvent struct {
		eventType ares_events.EventType
		payload   map[string]any
	}
	emittedEvents := []emittedEvent{
		{ares_events.EventSessionCreated, map[string]any{"session_id": streamID}},
		{ares_events.EventMessageAdded, map[string]any{"content": "hello"}},
		{ares_events.EventTaskCreated, map[string]any{"task_id": "t1"}},
		{ares_events.EventTaskCompleted, map[string]any{"task_id": "t1", "result": "ok"}},
		{ares_events.EventTaskCompleted, map[string]any{"step": "final"}},
	}

	var received []*ares_events.Event
	for i, ee := range emittedEvents {
		appendErr := store.Append(ctx, streamID, []*ares_events.Event{
			{Type: ee.eventType, Payload: ee.payload},
		}, int64(i))
		require.NoError(t, appendErr)

		// Consume the event from the subscriber before appending the next.
		select {
		case event, ok := <-subCh:
			require.True(t, ok, "subscriber channel closed unexpectedly at event %d", i)
			received = append(received, event)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}

	// Verify subscriber received all ares_events.
	require.Len(t, received, len(emittedEvents))
	for i, event := range received {
		assert.Equal(t, emittedEvents[i].eventType, event.Type,
			"subscriber event %d type mismatch", i)
		assert.Equal(t, emittedEvents[i].payload, event.Payload,
			"subscriber event %d payload mismatch", i)
	}

	// Replay from store and verify it matches the subscription.
	replayed, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, replayed, len(emittedEvents))

	for i, event := range replayed {
		assert.Equal(t, received[i].Type, event.Type,
			"replayed event %d type should match subscription", i)
		assert.Equal(t, received[i].Payload, event.Payload,
			"replayed event %d payload should match subscription", i)
		assert.Equal(t, received[i].Version, event.Version,
			"replayed event %d version should match subscription", i)
	}
}

// TestEventSourcing_FailoverWithPartialProgress verifies that a new leader
// can resume from the exact point where the previous leader stopped, even
// when the previous leader only partially completed a batch of work.
func TestEventSourcing_FailoverWithPartialProgress(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	streamID := fmt.Sprintf("session-partial-%d", time.Now().UnixNano())

	// Leader A emits the first 3 ares_events of a planned 6-event sequence.
	firstBatch := []*ares_events.Event{
		{Type: ares_events.EventSessionCreated, Payload: map[string]any{"session": streamID}},
		{Type: ares_events.EventTaskCreated, Payload: map[string]any{"task_id": "t1", "step": 1}},
		{Type: ares_events.EventTaskCompleted, Payload: map[string]any{"task_id": "t1", "step": 1}},
	}
	err := store.Append(ctx, streamID, firstBatch, 0)
	require.NoError(t, err)

	// Leader A "crashes".

	// Leader B starts and replays to find the last known state.
	replayed, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, replayed, 3)

	// Leader B determines the last version and continues.
	lastVersion := replayed[len(replayed)-1].Version
	assert.Equal(t, int64(3), lastVersion)

	// Leader B emits the remaining ares_events.
	secondBatch := []*ares_events.Event{
		{Type: ares_events.EventTaskCreated, Payload: map[string]any{"task_id": "t1", "step": 2}},
		{Type: ares_events.EventTaskCompleted, Payload: map[string]any{"task_id": "t1", "step": 2}},
		{Type: ares_events.EventTaskCompleted, Payload: map[string]any{"task_id": "t1", "result": "done"}},
	}
	err = store.Append(ctx, streamID, secondBatch, lastVersion)
	require.NoError(t, err, "leader B should append from leader A's last version")

	// Verify the complete sequence.
	allEvents, err := store.Read(ctx, streamID, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, allEvents, 6)

	// Verify versions are sequential.
	for i, event := range allEvents {
		assert.Equal(t, int64(i+1), event.Version)
	}

	// Verify the final event has the expected result.
	assert.Equal(t, "done", allEvents[5].Payload["result"])
}

// TestEventSourcing_FailoverMultipleStreams verifies that event sourcing
// handles multiple independent streams correctly during failover.
func TestEventSourcing_FailoverMultipleStreams(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	streamA := fmt.Sprintf("session-a-%d", time.Now().UnixNano())
	streamB := fmt.Sprintf("session-b-%d", time.Now().UnixNano())

	// Leader A emits ares_events to two different streams.
	err := store.Append(ctx, streamA, []*ares_events.Event{
		{Type: ares_events.EventSessionCreated, Payload: map[string]any{"stream": "A"}},
		{Type: ares_events.EventMessageAdded, Payload: map[string]any{"content": "A1"}},
	}, 0)
	require.NoError(t, err)

	err = store.Append(ctx, streamB, []*ares_events.Event{
		{Type: ares_events.EventSessionCreated, Payload: map[string]any{"stream": "B"}},
		{Type: ares_events.EventMessageAdded, Payload: map[string]any{"content": "B1"}},
		{Type: ares_events.EventMessageAdded, Payload: map[string]any{"content": "B2"}},
	}, 0)
	require.NoError(t, err)

	// Leader A "crashes".

	// Leader B replays each stream independently.
	eventsA, err := store.Read(ctx, streamA, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, eventsA, 2, "stream A should have 2 ares_events")
	assert.Equal(t, "A1", eventsA[1].Payload["content"])

	eventsB, err := store.Read(ctx, streamB, ares_events.ReadOptions{
		FromVersion: 1,
		Direction:   ares_events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, eventsB, 3, "stream B should have 3 ares_events")
	assert.Equal(t, "B1", eventsB[1].Payload["content"])
	assert.Equal(t, "B2", eventsB[2].Payload["content"])

	// Verify stream versions are independent.
	versionA, err := store.StreamVersion(ctx, streamA)
	require.NoError(t, err)
	assert.Equal(t, int64(2), versionA)

	versionB, err := store.StreamVersion(ctx, streamB)
	require.NoError(t, err)
	assert.Equal(t, int64(3), versionB)

	// ReadAll should return ares_events from both streams.
	allEvents, err := store.ReadAll(ctx, ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
	})
	require.NoError(t, err)
	assert.Len(t, allEvents, 5, "ReadAll should return ares_events from both streams")
}
