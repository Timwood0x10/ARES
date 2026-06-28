package tabs

import (
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

const (
	maxFaultInjections = 200
	maxSurvivalTests   = 200
)

// FaultInjection records a fault injection event.
type FaultInjection struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agent_id"`
	Type        string         `json:"type"`
	Details     map[string]any `json:"details,omitempty"`
	TriggeredAt time.Time      `json:"triggered_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Survived    bool           `json:"survived"`
}

// SurvivalTest records a survival test outcome.
type SurvivalTest struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agent_id"`
	TestType  string         `json:"test_type"`
	Passed    bool           `json:"passed"`
	Details   map[string]any `json:"details,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// ArenaTabSnapshot is the snapshot payload returned by ArenaTab.Snapshot.
type ArenaTabSnapshot struct {
	FaultInjections []FaultInjection `json:"fault_injections"`
	SurvivalTests   []SurvivalTest   `json:"survival_tests"`
}

// ArenaTab implements the Tab interface for the Arena tab.
// It tracks fault injections and survival test results.
type ArenaTab struct {
	mu              sync.RWMutex
	faultInjections []FaultInjection
	survivalTests   []SurvivalTest
}

// NewArenaTab creates a new ArenaTab instance.
func NewArenaTab() *ArenaTab {
	return &ArenaTab{
		faultInjections: make([]FaultInjection, 0, maxFaultInjections),
		survivalTests:   make([]SurvivalTest, 0, maxSurvivalTests),
	}
}

// Name returns the tab identifier.
func (t *ArenaTab) Name() string { return "arena" }

// Label returns the human-readable tab name.
func (t *ArenaTab) Label() string { return "Arena" }

// HandleEvent processes arena-related events.
func (t *ArenaTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case ares_events.EventFailoverTriggered:
		t.handleFaultInjection(evt)
	case ares_events.EventFailoverCompleted:
		t.handleSurvivalTest(evt)
	case ares_events.EventStepFailed:
		t.handleStepFailure(evt)
	}
}

// Snapshot returns the current arena state.
func (t *ArenaTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	fi := make([]FaultInjection, len(t.faultInjections))
	copy(fi, t.faultInjections)
	st := make([]SurvivalTest, len(t.survivalTests))
	copy(st, t.survivalTests)

	return ArenaTabSnapshot{
		FaultInjections: fi,
		SurvivalTests:   st,
	}
}

func (t *ArenaTab) handleFaultInjection(evt *ares_events.Event) {
	fi := FaultInjection{
		ID:          evt.ID,
		AgentID:     getString(evt.Payload, "agent_id"),
		Type:        getString(evt.Payload, "fault_type"),
		Details:     evt.Payload,
		TriggeredAt: evt.Timestamp,
	}
	if fi.AgentID == "" {
		fi.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.faultInjections) >= maxFaultInjections {
		t.faultInjections = t.faultInjections[1:]
	}
	t.faultInjections = append(t.faultInjections, fi)
}

func (t *ArenaTab) handleSurvivalTest(evt *ares_events.Event) {
	now := evt.Timestamp
	st := SurvivalTest{
		ID:        evt.ID,
		AgentID:   getString(evt.Payload, "agent_id"),
		TestType:  "failover",
		Passed:    getString(evt.Payload, "status") == "completed",
		Details:   evt.Payload,
		Timestamp: evt.Timestamp,
	}
	if st.AgentID == "" {
		st.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Mark the matching fault injection as completed.
	for i := len(t.faultInjections) - 1; i >= 0; i-- {
		fi := &t.faultInjections[i]
		if fi.AgentID == st.AgentID && fi.CompletedAt == nil {
			fi.CompletedAt = &now
			fi.Survived = st.Passed
			break
		}
	}

	if len(t.survivalTests) >= maxSurvivalTests {
		t.survivalTests = t.survivalTests[1:]
	}
	t.survivalTests = append(t.survivalTests, st)
}

func (t *ArenaTab) handleStepFailure(evt *ares_events.Event) {
	st := SurvivalTest{
		ID:        evt.ID,
		AgentID:   getString(evt.Payload, "agent_id"),
		TestType:  "step_failure",
		Passed:    false,
		Details:   evt.Payload,
		Timestamp: evt.Timestamp,
	}
	if st.AgentID == "" {
		st.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.survivalTests) >= maxSurvivalTests {
		t.survivalTests = t.survivalTests[1:]
	}
	t.survivalTests = append(t.survivalTests, st)
}
