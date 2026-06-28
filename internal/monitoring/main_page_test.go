package monitoring

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTab implements Tab for testing.
type mockTab struct {
	name      string
	label     string
	events    []*ares_events.Event
	snapValue any
}

func newMockTab(name, label string) *mockTab {
	return &mockTab{name: name, label: label}
}

func (m *mockTab) Name() string                       { return m.name }
func (m *mockTab) Label() string                      { return m.label }
func (m *mockTab) HandleEvent(evt *ares_events.Event) { m.events = append(m.events, evt) }
func (m *mockTab) Snapshot() any                      { return m.snapValue }

func TestNewMainPage(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		mp := NewMainPage()
		require.NotNil(t, mp)
		assert.NotNil(t, mp.costBar)
		assert.NotNil(t, mp.engine)
		assert.NotNil(t, mp.tabs)
		assert.Nil(t, mp.detailPanel)
		assert.Nil(t, mp.publisher)
	})

	t.Run("with all options", func(t *testing.T) {
		cb := NewCostBar()
		engine := dag.NewEngine()
		tracker := newMockTracker()
		dp := NewDetailPanel(tracker)
		tabMap := map[string]Tab{"test": newMockTab("test", "Test")}

		mp := NewMainPage(
			WithCostBar(cb),
			WithDAG(engine),
			WithDetailPanel(dp),
			WithTabs(tabMap),
		)
		assert.Equal(t, cb, mp.costBar)
		assert.Equal(t, engine, mp.engine)
		assert.Equal(t, dp, mp.detailPanel)
		assert.Len(t, mp.tabs, 1)
	})
}

func TestMainPage_HandleEvent_NilEvent(t *testing.T) {
	mp := NewMainPage()
	mp.HandleEvent(nil)
	// Should not panic.
}

func TestMainPage_HandleEvent_DispatchesToCostBar(t *testing.T) {
	mp := NewMainPage()
	mp.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "estimated_cost": 0.01,
		},
		Timestamp: time.Now(),
	})

	snap := mp.costBar.Snapshot()
	assert.InDelta(t, 0.01, snap.Total, 0.0001)
}

func TestMainPage_HandleEvent_DispatchesToDAG(t *testing.T) {
	mp := NewMainPage()
	mp.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{
			"agent_id": "a1", "name": "worker",
		},
		Timestamp: time.Now(),
	})

	node, ok := mp.engine.GetNode("a1")
	require.True(t, ok)
	assert.Equal(t, dag.StatusRunning, node.Status)
}

func TestMainPage_HandleEvent_DispatchesToTabs(t *testing.T) {
	tab1 := newMockTab("t1", "Tab 1")
	tab2 := newMockTab("t2", "Tab 2")
	mp := NewMainPage(WithTabs(map[string]Tab{
		"t1": tab1,
		"t2": tab2,
	}))

	evt := &ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	}
	mp.HandleEvent(evt)

	assert.Len(t, tab1.events, 1)
	assert.Len(t, tab2.events, 1)
}

func TestMainPage_Snapshot_Empty(t *testing.T) {
	mp := NewMainPage()
	snap := mp.Snapshot()
	assert.InDelta(t, 0, snap.Cost.Total, 0.0001)
	assert.Empty(t, snap.Tasks)
	assert.False(t, snap.UpdateTime.IsZero())
}

func TestMainPage_Snapshot_WithCostAndTasks(t *testing.T) {
	mp := NewMainPage()
	now := time.Now()

	// Create agent.
	mp.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1", "name": "worker"},
		Timestamp: now,
	})

	// Create task.
	mp.HandleEvent(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventTaskCreated,
		Payload:   map[string]any{"task_id": "t1", "name": "build"},
		Timestamp: now,
	})

	// Add cost.
	mp.HandleEvent(&ares_events.Event{
		ID: "e3", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "estimated_cost": 0.05,
		},
		Timestamp: now,
	})

	snap := mp.Snapshot()
	assert.InDelta(t, 0.05, snap.Cost.Total, 0.0001)
	assert.NotEmpty(t, snap.Tasks)
}

func TestMainPage_GetTab(t *testing.T) {
	tab1 := newMockTab("events", "Events")
	tab2 := newMockTab("llm", "LLM")
	mp := NewMainPage(WithTabs(map[string]Tab{
		"events": tab1,
		"llm":    tab2,
	}))

	t.Run("existing tab", func(t *testing.T) {
		tab, ok := mp.GetTab("events")
		assert.True(t, ok)
		assert.Equal(t, "events", tab.Name())
	})

	t.Run("missing tab", func(t *testing.T) {
		_, ok := mp.GetTab("missing")
		assert.False(t, ok)
	})
}

func TestMainPage_ConcurrentAccess(t *testing.T) {
	mp := NewMainPage()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 100; i++ {
			mp.HandleEvent(&ares_events.Event{
				ID: "e", StreamID: "s1", Type: ares_events.EventLLMCall,
				Payload:   map[string]any{"agent_id": "a1", "estimated_cost": 0.001},
				Timestamp: time.Now(),
			})
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		_ = mp.Snapshot()
	}

	<-done
	snap := mp.Snapshot()
	assert.InDelta(t, 0.1, snap.Cost.Total, 0.01)
}

func TestMainPage_GetTab_ImplementsInterface(t *testing.T) {
	var _ Tab = (*mockTab)(nil)
}
