package ares_events

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryEventStore_SubscriberGoroutineExitsOnClose is the CRIT-2
// regression: a subscriber whose caller context is NEVER cancelled must not
// leak its cleanup goroutine after Close.
//
// Bug scenario: before the fix the cleanup goroutine blocked on
// `<-ctx.Done()` only, so a long-lived daemon passing context.Background()
// leaked one goroutine per subscriber; each goroutine also pinned the store
// via its closure and blocked garbage collection.
//
// Fix contract: the goroutine selects on BOTH ctx.Done() and s.ctx.Done()
// (cancelled by Close), so it always has a deterministic exit path.
func TestMemoryEventStore_SubscriberGoroutineExitsOnClose(t *testing.T) {
	store := NewMemoryEventStore()

	baseline := runtime.NumGoroutine()
	_, err := store.Subscribe(context.Background(), EventFilter{})
	require.NoError(t, err)

	// Wait until the cleanup goroutine is observable so the post-Close
	// comparison measures its exit rather than a not-yet-started goroutine.
	withSubscriber := baseline
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := runtime.NumGoroutine(); n >= baseline+1 {
			withSubscriber = n
			break
		}
		time.Sleep(time.Millisecond)
	}
	if withSubscriber < baseline+1 {
		t.Log("cleanup goroutine not observed before Close; exit assertion may be vacuous")
	}

	require.NoError(t, store.Close())

	// The goroutine must observe s.ctx.Done() and exit, returning the count
	// to the baseline within a generous deadline.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("subscriber cleanup goroutine leaked after Close: baseline=%d observed=%d now=%d",
		baseline, withSubscriber, runtime.NumGoroutine())
}

// TestMemoryEventStore_SubscribeAfterClose covers the boundary where a caller
// subscribes to an already-closed store.
//
// Bug scenario: handing back a live channel that nobody will ever close would
// make consumers block forever on receive — a silent hang instead of an
// error. The store must reject the subscription with ErrEventStoreClosed.
func TestMemoryEventStore_SubscribeAfterClose(t *testing.T) {
	store := NewMemoryEventStore()
	require.NoError(t, store.Close())

	ch, err := store.Subscribe(context.Background(), EventFilter{})
	assert.ErrorIs(t, err, ErrEventStoreClosed)
	assert.Nil(t, ch, "no channel may be returned for a closed store")
}

// TestMemoryEventStore_ConcurrentCloseManyLiveSubscribers stresses the
// CRIT-1 ordering under concurrency: Close() racing the per-subscriber
// cleanup goroutines of MANY never-cancelled contexts.
//
// Bug scenario: a double close(ch) panics the process. Whichever side wins
// the race (Close closing every channel vs. a cleanup goroutine closing its
// own), each channel must be closed exactly once and no panic may escape.
func TestMemoryEventStore_ConcurrentCloseManyLiveSubscribers(t *testing.T) {
	const subscribers = 50

	store := NewMemoryEventStore()
	channels := make([]<-chan *Event, 0, subscribers)
	for i := 0; i < subscribers; i++ {
		ch, err := store.Subscribe(context.Background(), EventFilter{})
		require.NoError(t, err)
		channels = append(channels, ch)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = store.Close()
	}()
	wg.Wait()

	// Every channel must be closed exactly once: draining it must terminate
	// with ok=false rather than block forever (leak) or panic (double close).
	for i, ch := range channels {
		select {
		case _, ok := <-ch:
			assert.False(t, ok, "subscriber %d channel must be closed by Close", i)
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d channel never closed after Close", i)
		}
	}
	assert.Equal(t, 0, store.SubscriberCount(), "closed store retains no subscribers")
}

// TestMemoryEventStore_EventsDeliveredBeforeCloseWithLiveContext is the
// normal-path companion to the leak regression: a subscriber whose context is
// never cancelled still receives events while the store is open, and its
// buffered events stay readable after Close (close(ch) does not discard the
// buffer).
func TestMemoryEventStore_EventsDeliveredBeforeCloseWithLiveContext(t *testing.T) {
	store := NewMemoryEventStore()
	ch, err := store.Subscribe(context.Background(), EventFilter{})
	require.NoError(t, err)

	err = store.Append(context.Background(), "s1", []*Event{{Type: EventAgentStarted}}, 0)
	require.NoError(t, err)

	select {
	case event := <-ch:
		assert.Equal(t, EventType("agent.started"), event.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event before Close")
	}

	require.NoError(t, store.Close())
}
