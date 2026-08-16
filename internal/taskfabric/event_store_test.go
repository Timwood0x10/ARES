package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// TestFabricEventsPersistToStore verifies P2-C: with an event store attached,
// every task lifecycle transition is published as a task.* event on the
// task's stream, and the final state can be rebuilt from the store alone
// (cross-restart rebuild — Evidence-Driven).
func TestFabricEventsPersistToStore(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)

	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := f.Start("t1", "agent-a", epoch); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := f.Complete("t1", "agent-a", epoch); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Rebuild state from the store alone (cross-restart).
	events, err := store.Read(context.Background(), "t1", ares_events.ReadOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("want >=4 persisted events, got %d", len(events))
	}
	state := StateReady
	owner := ""
	for _, ev := range events {
		switch ev.Type {
		case ares_events.EventTaskCreated:
			state = StateReady
		case ares_events.EventTaskAcquired:
			state = StateLeased
			owner, _ = ev.Payload["agent_id"].(string)
		case ares_events.EventTaskStarted:
			state = StateRunning
		case ares_events.EventTaskCompleted:
			state = StateCompleted
		}
	}
	if state != StateCompleted || owner != "agent-a" {
		t.Fatalf("store rebuild must end COMPLETED by agent-a, got state=%s owner=%q", state, owner)
	}
}

// TestFabricNoStoreKeepsInMemoryLog verifies the fabric stays zero-value
// usable without a store: the in-memory log is the only sink.
func TestFabricNoStoreKeepsInMemoryLog(t *testing.T) {
	f := NewFabric()
	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(f.Events()) != 1 || f.Events()[0].Type != EventTaskCreated {
		t.Fatalf("want 1 in-memory created event, got %v", f.Events())
	}
}

// TestTaskEventTypeMapping verifies every fabric event type maps to a
// non-empty ares_events task.* event type.
func TestTaskEventTypeMapping(t *testing.T) {
	types := []EventType{
		EventTaskCreated, EventTaskReady, EventTaskAcquired, EventTaskStarted,
		EventTaskYielded, EventTaskCheckpointed, EventTaskPreempted,
		EventTaskReleased, EventTaskCompleted, EventTaskFailed,
		EventTaskExpired, EventTaskStolen,
	}
	for _, typ := range types {
		if taskEventType(typ) == "" {
			t.Fatalf("event type %s must map to a task.* event", typ)
		}
	}
}
