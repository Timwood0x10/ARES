package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestArenaTab_Interface(t *testing.T) {
	var tab Tab = NewArenaTab()
	if tab.Name() != "arena" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "arena")
	}
	if tab.Label() != "Arena" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "Arena")
	}
}

func TestArenaTab_HandleFaultInjection(t *testing.T) {
	tab := NewArenaTab()
	tab.HandleEvent(&ares_events.Event{
		ID:   "f1",
		Type: ares_events.EventFailoverTriggered,
		Payload: map[string]any{
			"agent_id":   "a1",
			"fault_type": "network_partition",
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(ArenaTabSnapshot)
	if len(snap.FaultInjections) != 1 {
		t.Fatalf("got %d fault injections, want 1", len(snap.FaultInjections))
	}
	if snap.FaultInjections[0].Type != "network_partition" {
		t.Errorf("Type = %q, want %q", snap.FaultInjections[0].Type, "network_partition")
	}
	if snap.FaultInjections[0].CompletedAt != nil {
		t.Error("CompletedAt should be nil before completion")
	}
}

func TestArenaTab_HandleSurvivalTest(t *testing.T) {
	tab := NewArenaTab()
	// First inject a fault.
	tab.HandleEvent(&ares_events.Event{
		ID:        "f1",
		Type:      ares_events.EventFailoverTriggered,
		Payload:   map[string]any{"agent_id": "a1", "fault_type": "crash"},
		Timestamp: time.Now(),
	})
	// Then complete it.
	tab.HandleEvent(&ares_events.Event{
		ID:        "s1",
		Type:      ares_events.EventFailoverCompleted,
		Payload:   map[string]any{"agent_id": "a1", "status": "completed"},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(ArenaTabSnapshot)
	if len(snap.SurvivalTests) != 1 {
		t.Fatalf("got %d survival tests, want 1", len(snap.SurvivalTests))
	}
	if !snap.SurvivalTests[0].Passed {
		t.Error("SurvivalTest.Passed should be true")
	}
	// The fault injection should now be marked as completed.
	if snap.FaultInjections[0].CompletedAt == nil {
		t.Error("FaultInjection.CompletedAt should be set after completion")
	}
	if !snap.FaultInjections[0].Survived {
		t.Error("FaultInjection.Survived should be true")
	}
}

func TestArenaTab_HandleStepFailure(t *testing.T) {
	tab := NewArenaTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "sf1",
		Type:      ares_events.EventStepFailed,
		Payload:   map[string]any{"agent_id": "a1", "step": "inference"},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(ArenaTabSnapshot)
	if len(snap.SurvivalTests) != 1 {
		t.Fatalf("got %d survival tests, want 1", len(snap.SurvivalTests))
	}
	if snap.SurvivalTests[0].Passed {
		t.Error("step failure should not pass")
	}
	if snap.SurvivalTests[0].TestType != "step_failure" {
		t.Errorf("TestType = %q, want %q", snap.SurvivalTests[0].TestType, "step_failure")
	}
}

func TestArenaTab_IgnoresIrrelevantEvents(t *testing.T) {
	tab := NewArenaTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(ArenaTabSnapshot)
	if len(snap.FaultInjections) != 0 || len(snap.SurvivalTests) != 0 {
		t.Error("non-arena events should be ignored")
	}
}

func TestArenaTab_NilEvent(t *testing.T) {
	tab := NewArenaTab()
	tab.HandleEvent(nil)
}

func TestArenaTab_Capacity(t *testing.T) {
	tab := NewArenaTab()
	for i := 0; i < maxFaultInjections+10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("f%d", i),
			Type:      ares_events.EventFailoverTriggered,
			Payload:   map[string]any{"agent_id": "a1"},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(ArenaTabSnapshot)
	if len(snap.FaultInjections) != maxFaultInjections {
		t.Errorf("fault injection count = %d, want %d", len(snap.FaultInjections), maxFaultInjections)
	}
}
