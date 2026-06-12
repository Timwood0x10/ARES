// package integration provides end-to-end integration tests for event-driven
// distillation: emitting events, subscribing, filtering, and ordering.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/events"
)

// TestEventDrivenDistillation_EmitAndSubscribe verifies that a subscriber
// receives events emitted after subscribing.
func TestEventDrivenDistillation_EmitAndSubscribe(t *testing.T) {
	store := events.NewMemoryEventStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Subscribe before emitting.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	ch, err := store.Subscribe(subCtx, events.EventFilter{})
	require.NoError(t, err)

	// Emit an event.
	err = store.Append(ctx, "stream-1", []*events.Event{
		{
			Type:    events.EventTaskCreated,
			Payload: map[string]any{"task_id": "task-001"},
		},
	}, 0)
	require.NoError(t, err)

	// Verify the subscriber receives the event.
	select {
	case evt := <-ch:
		require.NotNil(t, evt)
		assert.Equal(t, events.EventTaskCreated, evt.Type)
		assert.Equal(t, "stream-1", evt.StreamID)
		assert.Equal(t, "task-001", evt.Payload["task_id"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event from subscriber")
	}
}

// TestEventDrivenDistillation_MultipleSubscribers verifies that multiple
// subscribers all receive the same emitted events.
func TestEventDrivenDistillation_MultipleSubscribers(t *testing.T) {
	store := events.NewMemoryEventStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const numSubscribers = 3
	channels := make([]<-chan *events.Event, numSubscribers)

	// Create multiple subscribers.
	for i := 0; i < numSubscribers; i++ {
		subCtx, subCancel := context.WithCancel(ctx)
		defer subCancel()
		ch, err := store.Subscribe(subCtx, events.EventFilter{})
		require.NoError(t, err)
		channels[i] = ch
	}

	// Emit an event.
	err := store.Append(ctx, "stream-multi", []*events.Event{
		{
			Type:    events.EventAgentStarted,
			Payload: map[string]any{"agent_id": "agent-1"},
		},
	}, 0)
	require.NoError(t, err)

	// Verify each subscriber receives the event.
	for i := 0; i < numSubscribers; i++ {
		select {
		case evt := <-channels[i]:
			require.NotNil(t, evt, "subscriber %d received nil event", i)
			assert.Equal(t, events.EventAgentStarted, evt.Type)
			assert.Equal(t, "stream-multi", evt.StreamID)
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d timed out waiting for event", i)
		}
	}
}

// TestEventDrivenDistillation_EventOrdering verifies that events are stored
// and returned in the order they were appended. Uses Read (deterministic)
// rather than the lossy subscriber channel to verify ordering.
func TestEventDrivenDistillation_EventOrdering(t *testing.T) {
	store := events.NewMemoryEventStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Emit 5 events in sequence.
	const eventCount = 5
	for i := 0; i < eventCount; i++ {
		err := store.Append(ctx, "order-stream", []*events.Event{
			{
				Type:    events.EventTaskCompleted,
				Payload: map[string]any{"index": i},
			},
		}, 0)
		require.NoError(t, err)
	}

	// Read events back in ascending order.
	stored, err := store.Read(ctx, "order-stream", events.ReadOptions{
		Direction: events.ReadAscending,
	})
	require.NoError(t, err)
	require.Len(t, stored, eventCount, "all 5 events should be stored")

	// Verify ordering: each event's index should match its position.
	for i, evt := range stored {
		assert.Equal(t, events.EventTaskCompleted, evt.Type)
		index, ok := evt.Payload["index"].(int)
		require.True(t, ok, "payload index should be int")
		assert.Equal(t, i, index, "event at position %d should have index %d", i, i)
	}

	// Also verify the subscriber receives at least the first event
	// (subscribes before read, so it gets the in-order delivery).
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	ch, err := store.Subscribe(subCtx, events.EventFilter{})
	require.NoError(t, err)

	// Emit one more event; subscriber should receive it.
	err = store.Append(ctx, "order-stream", []*events.Event{
		{Type: events.EventTaskCompleted, Payload: map[string]any{"index": eventCount}},
	}, 0)
	require.NoError(t, err)

	select {
	case evt := <-ch:
		assert.Equal(t, events.EventTaskCompleted, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber timed out waiting for event")
	}
}

// TestEventDrivenDistillation_FilterByType verifies that events of different
// types are stored correctly and that ReadAll returns all events while
// subscriber filtering only delivers matching types.
func TestEventDrivenDistillation_FilterByType(t *testing.T) {
	store := events.NewMemoryEventStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Emit events of different types into separate streams so ReadAll returns all.
	eventTypes := []struct {
		stream string
		typ    events.EventType
		index  int
	}{
		{"s1", events.EventAgentStarted, 0},
		{"s2", events.EventTaskCompleted, 1},
		{"s3", events.EventTaskFailed, 2},
		{"s4", events.EventTaskCompleted, 3},
	}
	for _, et := range eventTypes {
		err := store.Append(ctx, et.stream, []*events.Event{
			{Type: et.typ, Payload: map[string]any{"index": et.index}},
		}, 0)
		require.NoError(t, err)
	}

	// ReadAll should return all 4 events regardless of type.
	allEvents, err := store.ReadAll(ctx, events.ReadOptions{Direction: events.ReadAscending})
	require.NoError(t, err)
	require.Len(t, allEvents, 4, "ReadAll should return all 4 events")

	// Verify subscriber with type filter receives only matching events.
	// Use a single event at a time to avoid channel buffer drops.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	ch, err := store.Subscribe(subCtx, events.EventFilter{
		Types: []events.EventType{events.EventTaskCompleted},
	})
	require.NoError(t, err)

	// Emit a non-matching event — subscriber should NOT receive it.
	err = store.Append(ctx, "filter-test", []*events.Event{
		{Type: events.EventAgentStarted, Payload: map[string]any{"k": "no"}},
	}, 0)
	require.NoError(t, err)

	// Emit a matching event — subscriber SHOULD receive it.
	err = store.Append(ctx, "filter-test", []*events.Event{
		{Type: events.EventTaskCompleted, Payload: map[string]any{"k": "yes"}},
	}, 0)
	require.NoError(t, err)

	select {
	case evt := <-ch:
		assert.Equal(t, events.EventTaskCompleted, evt.Type)
		assert.Equal(t, "yes", evt.Payload["k"])
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber timed out waiting for matching event")
	}
}
