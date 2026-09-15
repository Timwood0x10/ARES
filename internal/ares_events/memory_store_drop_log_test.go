package ares_events

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A6 regression: a full subscriber buffer must not drop events silently.
//
// The non-blocking send is intentional — one slow subscriber must not stall
// the publisher or the other subscribers — so the drop itself stays. What was
// wrong is that the ONLY trace of the loss was an in-memory counter that no
// production code ever read (MemoryEventStore.Stats() had zero non-test
// callers), so sustained data loss left no log line at all.
//
// The fix logs a Warn on a sampling schedule (1st drop, then every 1024th).

// subscriberBuffer mirrors the fixed channel capacity in Subscribe.
const subscriberBuffer = 64

// recordingHandler captures log records so a test can assert visibility.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// messages returns all captured messages containing substr.
func (h *recordingHandler) messages(substr string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		if r.Level < slog.LevelWarn {
			continue
		}
		msg := r.Message
		if strings.Contains(msg, substr) {
			out = append(out, msg)
		}
	}
	return out
}

// TestDroppedEventIsLogged pins the fix: when a subscriber's buffer is full
// the event is dropped (non-blocking by design) AND a Warn is emitted, so
// the loss is visible without reading an in-memory counter.
func TestDroppedEventIsLogged(t *testing.T) {
	// log is a package-level *slog.Logger snapshotted at init from the then
	// current slog.Default(), so a later slog.SetDefault does not reach it.
	// The test is in-package, so rebinding the same variable production uses
	// is the honest seam.
	handler := &recordingHandler{}
	prev := log
	log = slog.New(handler)
	t.Cleanup(func() { log = prev })

	store := NewMemoryEventStore()
	_, err := store.Subscribe(context.Background(), EventFilter{})
	require.NoError(t, err)

	// Fill the 64-slot subscriber buffer, then one more must be dropped.
	batch := make([]*Event, subscriberBuffer)
	for i := range batch {
		batch[i] = &Event{StreamID: "s1", Type: "t1"}
	}
	require.NoError(t, store.Append(context.Background(), "s1", batch, 0))
	require.NoError(t, store.Append(context.Background(), "s1", []*Event{{StreamID: "s1", Type: "t1"}}, 0))

	assert.Equal(t, int64(1), store.dropped.Load(),
		"the event past the buffer must be dropped")

	logged := handler.messages("subscriber buffer full")
	assert.NotEmpty(t, logged,
		"a dropped event must emit a Warn; an unlogged drop is invisible data loss")
}

// TestDroppedEventWarnIsSampled pins that the warning is bounded: a burst of
// drops logs the first one and then goes quiet until the next sample point,
// so a persistently slow subscriber cannot flood the log.
func TestDroppedEventWarnIsSampled(t *testing.T) {
	// log is a package-level *slog.Logger snapshotted at init from the then
	// current slog.Default(), so a later slog.SetDefault does not reach it.
	// The test is in-package, so rebinding the same variable production uses
	// is the honest seam.
	handler := &recordingHandler{}
	prev := log
	log = slog.New(handler)
	t.Cleanup(func() { log = prev })

	store := NewMemoryEventStore()
	_, err := store.Subscribe(context.Background(), EventFilter{})
	require.NoError(t, err)

	batch := make([]*Event, subscriberBuffer)
	for i := range batch {
		batch[i] = &Event{StreamID: "s1", Type: "t1"}
	}
	require.NoError(t, store.Append(context.Background(), "s1", batch, 0))
	const burst = 300
	for i := 0; i < burst; i++ {
		require.NoError(t, store.Append(context.Background(), "s1", []*Event{{StreamID: "s1", Type: "t1"}}, 0))
	}

	require.Equal(t, int64(burst), store.dropped.Load(),
		"every overflow event must still be counted")
	logged := handler.messages("subscriber buffer full")
	assert.Len(t, logged, 1,
		"a burst of drops must log exactly once (the first), not once per drop")
}
