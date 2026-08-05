package ares_events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryEventStore_ConcurrentAppend_DifferentStreams verifies that
// concurrent appends to DIFFERENT streams do not race and each stream ends up
// at version 1. Uses expectedVersion=0 (auto) so no OCC conflicts occur.
func TestMemoryEventStore_ConcurrentAppend_DifferentStreams(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()
	t.Cleanup(func() { _ = store.Close() })

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			streamID := fmt.Sprintf("stream-%d", idx)
			err := store.Append(ctx, streamID, []*Event{
				{Type: EventAgentStarted, Payload: map[string]any{"idx": idx}},
			}, 0)
			if err != nil {
				t.Errorf("goroutine %d: Append failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Every stream should have exactly one event at version 1.
	for i := 0; i < goroutines; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		version, err := store.StreamVersion(ctx, streamID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), version, "stream %s", streamID)
	}
}

// TestMemoryEventStore_ConcurrentAppend_SameStreamAuto verifies that
// concurrent appends to the SAME stream with expectedVersion=0 (auto-detect,
// no OCC check) do not race and produce a contiguous version sequence 1..N.
func TestMemoryEventStore_ConcurrentAppend_SameStreamAuto(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()
	t.Cleanup(func() { _ = store.Close() })

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			err := store.Append(ctx, "shared-stream", []*Event{
				{Type: EventTaskCreated, Payload: map[string]any{"idx": idx}},
			}, 0)
			if err != nil {
				t.Errorf("goroutine %d: Append failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	version, err := store.StreamVersion(ctx, "shared-stream")
	require.NoError(t, err)
	assert.Equal(t, int64(goroutines), version, "all appends should succeed with auto-version")

	events, err := store.Read(ctx, "shared-stream", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, events, goroutines, "all events should be stored")

	// Verify versions are contiguous 1..N with no gaps or duplicates.
	seen := make(map[int64]bool, len(events))
	for _, evt := range events {
		assert.False(t, seen[evt.Version], "duplicate version %d", evt.Version)
		assert.Greater(t, evt.Version, int64(0), "version must be positive")
		seen[evt.Version] = true
	}
	assert.Len(t, seen, goroutines, "expected %d unique versions", goroutines)
}

// TestMemoryEventStore_VersionMismatch verifies that appending with a wrong
// expectedVersion returns ErrVersionConflict (checked via errors.Is), and that
// valid version values (0=auto, -1=no check, matching) succeed.
func TestMemoryEventStore_VersionMismatch(t *testing.T) {
	tests := []struct {
		name            string
		seedVersion     int64 // version to pre-set on the stream (0 = no seed)
		expectedVersion int64 // expectedVersion passed to Append
		wantConflict    bool
	}{
		{"new_stream_wrong_version_99", 0, 99, true},
		{"empty_stream_expected_1", 0, 1, true},
		{"stream_at_2_expected_1", 2, 1, true},
		{"stream_at_3_expected_5", 3, 5, true},
		{"auto_version_zero_no_conflict", 2, 0, false},
		{"negative_one_no_check", 2, -1, false},
		{"matching_version_no_conflict", 2, 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryEventStore()
			ctx := context.Background()
			t.Cleanup(func() { _ = store.Close() })

			streamID := "mismatch-stream"
			// Seed the stream to the desired version using auto-version appends.
			if tt.seedVersion > 0 {
				seedEvents := make([]*Event, tt.seedVersion)
				for i := range seedEvents {
					seedEvents[i] = &Event{Type: EventAgentStarted}
				}
				err := store.Append(ctx, streamID, seedEvents, 0)
				require.NoError(t, err)
			}

			err := store.Append(ctx, streamID, []*Event{{Type: EventTaskCreated}}, tt.expectedVersion)
			if tt.wantConflict {
				if !errors.Is(err, ErrVersionConflict) {
					t.Errorf("expected ErrVersionConflict, got %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// TestMemoryEventStore_ConcurrentSubscribe verifies that concurrent Subscribe
// and Append operations do not race and that subscribers receive events.
func TestMemoryEventStore_ConcurrentSubscribe(t *testing.T) {
	store := NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = store.Close()
	})

	const numSubscribers = 25
	const numAppenders = 25
	const eventsPerAppender = 4

	// Pre-allocate slice; each goroutine writes its own index (no race).
	channels := make([]<-chan *Event, numSubscribers)
	var subWG sync.WaitGroup
	subWG.Add(numSubscribers)
	for i := 0; i < numSubscribers; i++ {
		go func(idx int) {
			defer subWG.Done()
			ch, err := store.Subscribe(ctx, EventFilter{})
			if err == nil {
				channels[idx] = ch
			}
		}(i)
	}

	// Concurrently append events to a shared stream (auto-version, no OCC).
	var appWG sync.WaitGroup
	appWG.Add(numAppenders)
	for i := 0; i < numAppenders; i++ {
		go func(idx int) {
			defer appWG.Done()
			for j := 0; j < eventsPerAppender; j++ {
				_ = store.Append(ctx, "sub-stream", []*Event{
					{Type: EventTaskCreated, Payload: map[string]any{"app": idx, "evt": j}},
				}, 0)
			}
		}(i)
	}

	appWG.Wait()
	subWG.Wait()

	// Drain buffered events non-blockingly; at least some should be delivered.
	received := drainReceived(channels)
	assert.Greater(t, received, 0, "expected at least one event delivered to subscribers")

	version, err := store.StreamVersion(ctx, "sub-stream")
	require.NoError(t, err)
	assert.Equal(t, int64(numAppenders*eventsPerAppender), version)
}

// drainReceived counts events currently buffered in the given channels
// without blocking. Returns the total count of successfully received events.
func drainReceived(channels []<-chan *Event) int {
	total := 0
	for _, ch := range channels {
		if ch != nil {
			total += drainChannel(ch)
		}
	}
	return total
}

// drainChannel reads all buffered events from a single channel without
// blocking. Stops when the channel is empty or closed.
func drainChannel(ch <-chan *Event) int {
	total := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return total
			}
			total++
		default:
			return total
		}
	}
}
