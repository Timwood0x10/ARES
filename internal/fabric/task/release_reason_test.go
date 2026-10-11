package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// TestReleaseReasonRecordsCause pins the reclaim attribution: ReleaseReason
// carries WHY the holder stepped back on the task.released event, while a plain
// Release stays payload-free. Without it a reclaim was indistinguishable from
// an ordinary release — the gap that made a budget-starved agent look exactly
// like a missing executor from the event stream.
func TestReleaseReasonRecordsCause(t *testing.T) {
	ctx := context.Background()
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	if err := f.Create(&Task{ID: "t1", Capability: "code"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := f.ReleaseReason("t1", "agent-a", epoch, "budget_reserved"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// The transition itself is unchanged: READY, unowned, no lease.
	tk, err := f.Task("t1")
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if tk.State != StateReady || tk.Owner != "" || tk.Lease != nil {
		t.Fatalf("after release: state=%s owner=%q lease=%v; want READY/unowned/nil",
			tk.State, tk.Owner, tk.Lease)
	}

	evs, err := store.Read(ctx, "t1", ares_events.ReadOptions{})
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var found bool
	for _, ev := range evs {
		if ev.Type != ares_events.EventTaskReleased {
			continue
		}
		found = true
		if got := ev.Payload[payloadKeyReleaseReason]; got != "budget_reserved" {
			t.Fatalf("task.released reason = %v, want %q", got, "budget_reserved")
		}
		// Provenance: the event still names the holder that released it.
		if got := ev.Payload["agent_id"]; got != "agent-a" {
			t.Fatalf("task.released agent_id = %v, want agent-a", got)
		}
	}
	if !found {
		t.Fatal("no task.released event recorded")
	}
}

// TestReleaseWithoutReasonKeepsPayloadClean is the no-regression guard: the
// plain Release path (cancellation, preemption-driven release) must not grow a
// reason key, so existing consumers see byte-identical payloads.
func TestReleaseWithoutReasonKeepsPayloadClean(t *testing.T) {
	ctx := context.Background()
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	if err := f.Create(&Task{ID: "t2", Capability: "code"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	epoch, err := f.Acquire("t2", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := f.Release("t2", "agent-a", epoch); err != nil {
		t.Fatalf("release: %v", err)
	}

	evs, err := store.Read(ctx, "t2", ares_events.ReadOptions{})
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	for _, ev := range evs {
		if ev.Type != ares_events.EventTaskReleased {
			continue
		}
		if _, present := ev.Payload[payloadKeyReleaseReason]; present {
			t.Fatalf("plain Release must not carry %q, payload=%v", payloadKeyReleaseReason, ev.Payload)
		}
	}
}
