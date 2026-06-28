package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
)

func TestArenaTab_Interface(t *testing.T) {
	var tab Tab = NewArenaTab()
	assert.Equal(t, "arena", tab.Name())
	assert.Equal(t, "Arena", tab.Label())
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
	assert.Len(t, snap.FaultInjections, 1)
	assert.Equal(t, "network_partition", snap.FaultInjections[0].Type)
	assert.Nil(t, snap.FaultInjections[0].CompletedAt)
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
	assert.Len(t, snap.SurvivalTests, 1)
	assert.True(t, snap.SurvivalTests[0].Passed)
	assert.NotNil(t, snap.FaultInjections[0].CompletedAt)
	assert.True(t, snap.FaultInjections[0].Survived)
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
	assert.Len(t, snap.SurvivalTests, 1)
	assert.False(t, snap.SurvivalTests[0].Passed)
	assert.Equal(t, "step_failure", snap.SurvivalTests[0].TestType)
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
	assert.Empty(t, snap.FaultInjections)
	assert.Empty(t, snap.SurvivalTests)
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
	assert.Equal(t, maxFaultInjections, len(snap.FaultInjections))
}

func TestArenaTab_Trim(t *testing.T) {
	tab := NewArenaTab()
	for i := 0; i < 10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("f%d", i),
			Type:      ares_events.EventFailoverTriggered,
			Payload:   map[string]any{"agent_id": "a1"},
			Timestamp: time.Now(),
		})
	}
	for i := 0; i < 8; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("s%d", i),
			Type:      ares_events.EventFailoverCompleted,
			Payload:   map[string]any{"agent_id": "a1", "status": "completed"},
			Timestamp: time.Now(),
		})
	}

	tab.Trim(3)
	assert.Equal(t, 3, len(tab.faultInjections))
	assert.Equal(t, 3, len(tab.survivalTests))
}
