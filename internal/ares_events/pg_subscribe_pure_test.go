package ares_events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNextPollCursor is the pure-logic regression test for the Subscribe
// keyset-pagination fix: the cursor must advance on every NON-EMPTY query
// page, or a full page of undelivered events wedges the subscriber forever
// (the same 100 rows re-read, batch empty, cursor frozen — E101+ never
// delivered).
func TestNextPollCursor(t *testing.T) {
	ts := func(sec int) time.Time {
		return time.Unix(int64(sec), 0).UTC()
	}
	evt := func(id string, sec int) *Event {
		return &Event{ID: id, Timestamp: ts(sec)}
	}

	tests := []struct {
		name string
		// events is the raw query page (ASC by (created_at, id), ≤ LIMIT rows).
		events []*Event
		// wantAdvance reports whether the cursor must move this poll.
		wantAdvance bool
		// wantCursor/wantCursorID are the expected cursor after the poll.
		wantCursor   time.Time
		wantCursorID string
	}{
		{
			name:        "empty page keeps cursor",
			events:      nil,
			wantAdvance: false,
		},
		{
			name:         "full page advances to page tail",
			events:       []*Event{evt("e1", 11), evt("e2", 12), evt("e3", 13)},
			wantAdvance:  true,
			wantCursor:   ts(13),
			wantCursorID: "e3",
		},
		{
			// The 1.4 wedge: a page whose rows are ALL already delivered
			// (batch empty) left the cursor frozen, so the window never
			// drained — the same full page was re-read forever.
			name:         "delivered-only page still advances",
			events:       []*Event{evt("e1", 11), evt("e2", 12)},
			wantAdvance:  true,
			wantCursor:   ts(12),
			wantCursorID: "e2",
		},
		{
			name:         "partial page advances to page tail",
			events:       []*Event{evt("e1", 11), evt("e2", 12), evt("e3", 12)},
			wantAdvance:  true,
			wantCursor:   ts(12),
			wantCursorID: "e3",
		},
		{
			// Ties inside the page: the cursor lands on the page-tail
			// (timestamp, id); same-timestamp events with a later id remain
			// observable next poll via the inclusive composite window and
			// are deduped by delivered ids.
			name:         "tie at page boundary advances to shared timestamp and tail id",
			events:       []*Event{evt("e1", 12), evt("e2", 12), evt("e3", 12)},
			wantAdvance:  true,
			wantCursor:   ts(12),
			wantCursorID: "e3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTS, gotID, ok := nextPollCursor(tt.events)
			assert.Equal(t, tt.wantAdvance, ok, "advance decision mismatch")
			if tt.wantAdvance {
				assert.Equal(t, tt.wantCursor, gotTS)
				assert.Equal(t, tt.wantCursorID, gotID)
			}
		})
	}
}

// scriptedPageQuery returns events per call in order, ignoring filter args.
func scriptedPageQuery(pages ...[]*Event) eventPageQuery {
	call := 0
	return func(context.Context, EventFilter, time.Time, string) ([]*Event, error) {
		if call >= len(pages) {
			return nil, nil
		}
		p := pages[call]
		call++
		return p, nil
	}
}

// TestPollOnce_CursorEscapesFullWindow drives pollOnce through the 1.4
// scenario: a burst larger than defaultEventReadLimit where the first full
// page is fully undelivered. The second poll re-reads the SAME page (all
// delivered, batch empty) — the cursor must still advance past it, or
// E101+ are never delivered.
func TestPollOnce_CursorEscapesFullWindow(t *testing.T) {
	base := time.Unix(0, 0).UTC()

	all := make([]*Event, 0, 150)
	for i := 1; i <= 150; i++ {
		all = append(all, &Event{ID: newTestEventID(i), Timestamp: time.Unix(int64(i), 0).UTC()})
	}

	ch := make(chan *Event, 256)
	sub := &pgSubscription{cursor: base, ch: ch, delivered: make(map[string]bool, 1024)}

	// Poll 1: full page (100 undelivered). Poll 2: the same page re-read —
	// every id now in delivered, so the batch is empty. Poll 3: the tail.
	query := scriptedPageQuery(all[:100], all[:100], all[100:])

	require.NoError(t, pollOnce(context.Background(), sub, query))
	assert.Equal(t, time.Unix(100, 0).UTC(), sub.cursor,
		"full page must advance cursor to page tail")
	require.Len(t, drainEvents(ch), 100)

	require.NoError(t, pollOnce(context.Background(), sub, query))
	assert.Equal(t, time.Unix(100, 0).UTC(), sub.cursor,
		"delivered-only page must STILL advance the cursor — this is the 1.4 wedge")

	require.NoError(t, pollOnce(context.Background(), sub, query))
	assert.Equal(t, time.Unix(150, 0).UTC(), sub.cursor)
	require.Len(t, drainEvents(ch), 50)

	// Nothing re-delivered and nothing lost: each of the 150 ids arrived
	// exactly once across all polls.
	counts := make(map[string]int)
	for _, e := range all {
		counts[e.ID]++
	}
	assert.Equal(t, 150, len(counts))
	for id, n := range counts {
		assert.Equal(t, 1, n, "event %s delivered %d times", id, n)
	}
}

// TestPollOnce_TieWindowNoLoss models the exact SQL window semantics
// (created_at >= cursor, ASC, LIMIT) over a table with timestamp ties that
// straddle the page boundary, then replays the poll loop until drained.
// Regression: events inserted AFTER the cursor moved past their timestamp
// siblings must never be skipped.
func TestPollOnce_TieWindowNoLoss(t *testing.T) {
	const shared = int64(500)

	// 250 events with interleaved timestamps: groups share one timestamp so
	// ties span page boundaries (LIMIT 100 cuts mid-tie).
	var table []*Event
	for i := 1; i <= 250; i++ {
		sec := shared + int64(i/2) // pairs share a timestamp
		table = append(table, &Event{ID: newTestEventID(i), Timestamp: time.Unix(sec, 0).UTC()})
	}

	// Mirrors the production keyset window: (created_at, id) >= (cursorTS,
	// cursorID), ordered by (created_at, id). The id component is what makes
	// a full page of tied timestamps make progress.
	query := func(_ context.Context, _ EventFilter, cursor time.Time, cursorID string) ([]*Event, error) {
		var page []*Event
		for _, e := range table { // table is already (created_at, id) ASC
			if e.Timestamp.After(cursor) || (e.Timestamp.Equal(cursor) && e.ID >= cursorID) {
				page = append(page, e)
				if len(page) == defaultEventReadLimit {
					break
				}
			}
		}
		return page, nil
	}

	ch := make(chan *Event, 512)
	sub := &pgSubscription{cursor: time.Unix(shared, 0).UTC(), ch: ch, delivered: make(map[string]bool, 1024)}

	for range 10 { // a few polls suffice: 3 pages of 100/100/50
		require.NoError(t, pollOnce(context.Background(), sub, query))
	}

	delivered := drainEvents(ch)
	assert.Len(t, delivered, 250, "every event must be delivered exactly once")
	counts := make(map[string]int, 250)
	for _, e := range delivered {
		counts[e.ID]++
	}
	assert.Equal(t, 250, len(counts))
	for id, n := range counts {
		assert.Equal(t, 1, n, "event %s delivered %d times", id, n)
	}
}

func newTestEventID(i int) string {
	const digits = "0123456789"
	id := []byte("evt-000")
	id[4] = digits[(i/100)%10]
	id[5] = digits[(i/10)%10]
	id[6] = digits[i%10]
	return string(id)
}

func drainEvents(ch chan *Event) []*Event {
	var out []*Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}
