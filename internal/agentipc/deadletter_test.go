package agentipc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestDeadLetter_RecordsUnreachable verifies: a send to an
// unregistered agent is recorded in the dead-letter store.
func TestDeadLetter_RecordsUnreachable(t *testing.T) {
	b := NewBus()
	if err := b.Send(context.Background(), "a", "ghost", "t", "p"); !errors.Is(err, ErrAgentNotRegistered) {
		t.Fatalf("want ErrAgentNotRegistered, got %v", err)
	}
	if got := b.DeadLetters().Count(); got != 1 {
		t.Fatalf("dead letters = %d, want 1", got)
	}
	dl := b.DeadLetters().Snapshot()[0]
	if dl.To != "ghost" || dl.Topic != "t" || dl.Reason != ErrAgentNotRegistered.Error() {
		t.Fatalf("dead letter fields mismatch: %+v", dl)
	}
}

// TestDeadLetter_RecordsTimeout verifies a timed-out Request lands in the
// dead-letter store with the timeout reason.
func TestDeadLetter_RecordsTimeout(t *testing.T) {
	b := NewBus()
	_ = b.Register("slow", func(ctx context.Context, m *Message) (*Message, error) {
		time.Sleep(50 * time.Millisecond)
		return nil, nil // async reply never arrives
	})
	_, err := b.Request(context.Background(), "caller", "slow", "t", "p", 5*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if got := b.DeadLetters().Count(); got != 1 {
		t.Fatalf("dead letters = %d, want 1", got)
	}
}

// TestDeadLetter_CapacityEviction verifies the ring bound: oldest entries are
// evicted when capacity is exceeded (unbounded-growth guard).
func TestDeadLetter_CapacityEviction(t *testing.T) {
	s := NewDeadLetterStore(3)
	for i := 0; i < 5; i++ {
		s.Record("a", "b", "t", i, "boom", "trace-9")
	}
	if got := s.Count(); got != 3 {
		t.Fatalf("count = %d, want 3 (ring bound)", got)
	}
	snap := s.Snapshot()
	if snap[0].ID != 3 {
		t.Fatalf("oldest retained id = %d, want 3 (1 and 2 evicted)", snap[0].ID)
	}
	if snap[0].TraceID != "trace-9" {
		t.Fatalf("trace id = %q, want it preserved on the record", snap[0].TraceID)
	}
}

// TestDeadLetter_EvictionOverwritesInPlace is the leak this store used to have: eviction sliced the
// front off (`entries = entries[1:]`), which advances the slice header but leaves the backing array
// — and therefore every evicted DeadLetter, Payload included — reachable for the life of the
// process. A long-lived bus with steady failures grew without bound at one payload per eviction.
//
// The assertion is on the address of the first slot: a fixed ring reuses the same array forever, so
// the base never moves. The old code moved it on every eviction (and reallocated periodically),
// which is the mechanism that pinned the evicted payloads.
//
// A finalizer-based version of this test was tried first and did NOT catch the regression: the
// payload was collected even with the leak, so it proved nothing and was replaced.
func TestDeadLetter_EvictionOverwritesInPlace(t *testing.T) {
	store := NewDeadLetterStore(2)
	store.Record("a", "b", "topic", "first", "timeout", "trace-1")
	store.Record("a", "b", "topic", "second", "timeout", "trace-2")

	base := fmt.Sprintf("%p", &store.entries[0])
	snapshot := store.Snapshot()

	for i := 0; i < 8; i++ {
		store.Record("a", "b", "topic", fmt.Sprintf("later-%d", i), "timeout", "trace-x")
	}

	if got := fmt.Sprintf("%p", &store.entries[0]); got != base {
		t.Fatalf("the backing array moved (%s -> %s): eviction is shifting the slice instead of "+
			"overwriting a slot, which keeps evicted payloads reachable", base, got)
	}
	if got := len(store.entries); got != 2 {
		t.Fatalf("retained %d slots for capacity 2", got)
	}

	// The ring must still read oldest-first: the first two entries are the newest writes.
	got := store.Snapshot()
	if got[len(got)-1].Payload != "later-7" {
		t.Fatalf("newest entry is %v, want later-7", got[len(got)-1].Payload)
	}
	if got[0].Payload != "later-6" {
		t.Fatalf("oldest retained entry is %v, want later-6", got[0].Payload)
	}
	_ = snapshot
}
