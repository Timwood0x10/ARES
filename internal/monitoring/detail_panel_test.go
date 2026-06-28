package monitoring

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTrackerReader implements TrackerReader for testing.
type mockTrackerReader struct {
	agents map[string]*UnifiedAgent
	stats  ConsoleStats
}

func newMockTracker(agents ...*UnifiedAgent) *mockTrackerReader {
	m := &mockTrackerReader{agents: make(map[string]*UnifiedAgent)}
	for _, a := range agents {
		m.agents[a.ID] = a
	}
	return m
}

func (m *mockTrackerReader) GetAgent(id string) (*UnifiedAgent, bool) {
	a, ok := m.agents[id]
	if !ok {
		return nil, false
	}
	cp := *a
	return &cp, true
}

func (m *mockTrackerReader) ListAgents() []UnifiedAgent {
	result := make([]UnifiedAgent, 0, len(m.agents))
	for _, a := range m.agents {
		cp := *a
		result = append(result, cp)
	}
	return result
}

func (m *mockTrackerReader) Snapshot() ConsoleStats {
	return m.stats
}

// mockAgentLinker implements AgentLinker for testing.
type mockAgentLinker struct {
	links map[string][]Interaction
}

func (m *mockAgentLinker) GetLinks(agentID string) []Interaction {
	if m.links == nil {
		return nil
	}
	return m.links[agentID]
}

// mockCostProvider implements CostProvider for testing.
type mockCostProvider struct {
	costs map[string]*AgentCost
}

func (m *mockCostProvider) GetCost(agentID string) (*AgentCost, bool) {
	if m.costs == nil {
		return nil, false
	}
	c, ok := m.costs[agentID]
	if !ok {
		return nil, false
	}
	cp := *c
	return &cp, true
}

func sampleAgent(id string) *UnifiedAgent {
	return &UnifiedAgent{
		ID:        id,
		Name:      "worker-" + id,
		Status:    dag.StatusRunning,
		Role:      "coder",
		ModelName: "gpt-4",
		TaskID:    "t1",
		StartedAt: time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 28, 10, 5, 0, 0, time.UTC),
	}
}

func TestNewDetailPanel(t *testing.T) {
	tracker := newMockTracker()

	t.Run("with defaults", func(t *testing.T) {
		dp := NewDetailPanel(tracker)
		require.NotNil(t, dp)
		assert.Nil(t, dp.linker)
		assert.Nil(t, dp.cost)
	})

	t.Run("with linker", func(t *testing.T) {
		linker := &mockAgentLinker{}
		dp := NewDetailPanel(tracker, WithLinker(linker))
		assert.NotNil(t, dp.linker)
	})

	t.Run("with cost provider", func(t *testing.T) {
		cost := &mockCostProvider{}
		dp := NewDetailPanel(tracker, WithCostProvider(cost))
		assert.NotNil(t, dp.cost)
	})

	t.Run("with all options", func(t *testing.T) {
		linker := &mockAgentLinker{}
		cost := &mockCostProvider{}
		dp := NewDetailPanel(tracker, WithLinker(linker), WithCostProvider(cost))
		assert.NotNil(t, dp.linker)
		assert.NotNil(t, dp.cost)
	})
}

func TestNewDetailPanel_NilTracker(t *testing.T) {
	assert.Panics(t, func() {
		NewDetailPanel(nil)
	})
}

func TestGetDetail_BasicAgent(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)
	require.NotNil(t, view)

	assert.Equal(t, "agent", view.EntityType)
	assert.Equal(t, "a1", view.EntityID)
	assert.Contains(t, view.Tabs, "overview")
	assert.Contains(t, view.Tabs, "cost")
	assert.Contains(t, view.Tabs, "interactions")

	assert.Equal(t, "a1", view.Data["id"])
	assert.Equal(t, "worker-a1", view.Data["name"])
	assert.Equal(t, "running", view.Data["status"])
	assert.Equal(t, "coder", view.Data["role"])
	assert.Equal(t, "gpt-4", view.Data["model_name"])
}

func TestGetDetail_WithCost(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	costProv := &mockCostProvider{
		costs: map[string]*AgentCost{
			"a1": {
				AgentID:       "a1",
				InputTokens:   500,
				OutputTokens:  200,
				EstimatedCost: 0.015,
				CallCount:     3,
			},
		},
	}

	dp := NewDetailPanel(tracker, WithCostProvider(costProv))

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)

	costData, ok := view.Data["cost"]
	require.True(t, ok)
	cost, ok := costData.(*AgentCost)
	require.True(t, ok)
	assert.Equal(t, int64(500), cost.InputTokens)
	assert.Equal(t, 3, cost.CallCount)
}

func TestGetDetail_WithLinks(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	linker := &mockAgentLinker{
		links: map[string][]Interaction{
			"a1": {
				{ID: "i1", FromID: "a1", ToID: "a2", Type: "message"},
			},
		},
	}

	dp := NewDetailPanel(tracker, WithLinker(linker))

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)

	interactions, ok := view.Data["interactions"]
	require.True(t, ok)
	links, ok := interactions.([]Interaction)
	require.True(t, ok)
	assert.Len(t, links, 1)
	assert.Equal(t, "a1", links[0].FromID)
}

func TestGetDetail_WithAllEnrichment(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	costProv := &mockCostProvider{
		costs: map[string]*AgentCost{
			"a1": {AgentID: "a1", EstimatedCost: 0.05},
		},
	}
	linker := &mockAgentLinker{
		links: map[string][]Interaction{
			"a1": {{ID: "i1", FromID: "a1", ToID: "a2", Type: "delegate"}},
		},
	}

	dp := NewDetailPanel(tracker, WithLinker(linker), WithCostProvider(costProv))

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)
	assert.NotNil(t, view.Data["cost"])
	assert.NotNil(t, view.Data["interactions"])
}

func TestGetDetail_AgentNotFound(t *testing.T) {
	tracker := newMockTracker()
	dp := NewDetailPanel(tracker)

	_, err := dp.GetDetail("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent not found")
}

func TestGetDetail_EmptyAgentID(t *testing.T) {
	tracker := newMockTracker()
	dp := NewDetailPanel(tracker)

	_, err := dp.GetDetail("")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyAgentID)
}

func TestGetDetail_NoCostProvider(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)
	_, hasCost := view.Data["cost"]
	assert.False(t, hasCost)
}

func TestGetDetail_NoLinker(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)
	_, hasLinks := view.Data["interactions"]
	assert.False(t, hasLinks)
}

func TestGetDetail_AgentWithTagsAndMetadata(t *testing.T) {
	agent := sampleAgent("a1")
	agent.Tags = []string{"ml", "training"}
	agent.Metadata = map[string]any{"region": "us-east-1"}
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ml", "training"}, view.Data["tags"])
	assert.Equal(t, map[string]any{"region": "us-east-1"}, view.Data["metadata"])
}

func TestGetDetail_AgentWithParentID(t *testing.T) {
	agent := sampleAgent("a1")
	agent.ParentID = "parent-1"
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)

	view, err := dp.GetDetail("a1")
	require.NoError(t, err)
	assert.Equal(t, "parent-1", view.Data["parent_id"])
}

func TestHandleEvent_MatchingAgent(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)
	dp.SetViewedAgent("a1")

	// Should not panic and should be a no-op (tracker is updated externally).
	dp.HandleEvent(&ares_events.Event{
		ID:        "e1",
		StreamID:  "s1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	})
}

func TestHandleEvent_NonMatchingAgent(t *testing.T) {
	agent := sampleAgent("a1")
	tracker := newMockTracker(agent)
	dp := NewDetailPanel(tracker)
	dp.SetViewedAgent("a1")

	// Event for different agent — no effect.
	dp.HandleEvent(&ares_events.Event{
		ID:        "e1",
		StreamID:  "s1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a2"},
		Timestamp: time.Now(),
	})
}

func TestHandleEvent_NilEvent(t *testing.T) {
	tracker := newMockTracker()
	dp := NewDetailPanel(tracker)
	dp.SetViewedAgent("a1")

	// Should not panic.
	dp.HandleEvent(nil)
}

func TestHandleEvent_NoViewedAgent(t *testing.T) {
	tracker := newMockTracker()
	dp := NewDetailPanel(tracker)

	// No agent set — should be a no-op.
	dp.HandleEvent(&ares_events.Event{
		ID:        "e1",
		StreamID:  "s1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	})
}

func TestHandleEvent_EmptyPayloadAgentID(t *testing.T) {
	tracker := newMockTracker()
	dp := NewDetailPanel(tracker)
	dp.SetViewedAgent("stream-1")

	// No agent_id in payload — falls back to StreamID.
	dp.HandleEvent(&ares_events.Event{
		ID:        "e1",
		StreamID:  "stream-1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
}

func TestSetViewedAgent(t *testing.T) {
	tracker := newMockTracker()
	dp := NewDetailPanel(tracker)

	dp.SetViewedAgent("a1")
	dp.mu.RLock()
	assert.Equal(t, "a1", dp.viewedAgent)
	dp.mu.RUnlock()

	dp.SetViewedAgent("a2")
	dp.mu.RLock()
	assert.Equal(t, "a2", dp.viewedAgent)
	dp.mu.RUnlock()
}
