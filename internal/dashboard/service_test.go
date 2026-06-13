package dashboard

import (
	"context"
	"testing"
	"time"

	"goagentx/internal/agents/base"
	"goagentx/internal/events"
	"goagentx/internal/runtime"
)

// mockRuntime implements runtime.Runtime for testing.
type mockRuntime struct {
	stats runtime.RuntimeStats
}

func (m *mockRuntime) StartAgent(_ context.Context, _ base.Agent) error { return nil }
func (m *mockRuntime) StopAgent(_ context.Context, _ string) error      { return nil }
func (m *mockRuntime) RestartAgent(_ context.Context, _ string) error   { return nil }
func (m *mockRuntime) RestoreAgent(_ context.Context, _ string, _ runtime.AgentFactory) error {
	return nil
}
func (m *mockRuntime) NotifyAgentDead(_ string, _ string)                 {}
func (m *mockRuntime) RegisterAgent(_ base.Agent, _ runtime.AgentFactory) {}
func (m *mockRuntime) Start(_ context.Context) error                      { return nil }
func (m *mockRuntime) Stop() error                                        { return nil }
func (m *mockRuntime) Stats() runtime.RuntimeStats                        { return m.stats }

// mockAgentProvider implements AgentProvider for testing.
type mockAgentProvider struct {
	agents []AgentInfo
}

func (m *mockAgentProvider) ListAgents() []AgentInfo {
	return m.agents
}

func (m *mockAgentProvider) GetAgent(id string) (*AgentInfo, bool) {
	for _, a := range m.agents {
		if a.ID == id {
			return &a, true
		}
	}
	return nil, false
}

// mockEventStore implements events.EventStore for testing.
type mockEventStore struct {
	events []*events.Event
}

func (m *mockEventStore) Append(_ context.Context, _ string, _ []*events.Event, _ int64) error {
	return nil
}

func (m *mockEventStore) Read(_ context.Context, _ string, _ events.ReadOptions) ([]*events.Event, error) {
	return m.events, nil
}

func (m *mockEventStore) ReadAll(_ context.Context, _ events.ReadOptions) ([]*events.Event, error) {
	return m.events, nil
}

func (m *mockEventStore) Subscribe(_ context.Context, _ events.EventFilter) (<-chan *events.Event, error) {
	ch := make(chan *events.Event, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	return ch, nil
}

func (m *mockEventStore) StreamVersion(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func TestDashboardServiceGetOverview(t *testing.T) {
	rt := &mockRuntime{
		stats: runtime.RuntimeStats{
			ActiveAgents:  3,
			TotalRestarts: 5,
			Uptime:        10 * time.Minute,
		},
	}

	service := NewDashboardService(rt, nil, nil, nil, nil)
	overview, err := service.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("GetOverview error: %v", err)
	}

	if overview.AgentCount != 3 {
		t.Errorf("AgentCount = %d, want 3", overview.AgentCount)
	}
	if overview.RuntimeStats.TotalRestarts != 5 {
		t.Errorf("TotalRestarts = %d, want 5", overview.RuntimeStats.TotalRestarts)
	}
	if overview.Uptime == "" {
		t.Error("Uptime should not be empty")
	}
}

func TestDashboardServiceListAgents(t *testing.T) {
	agents := &mockAgentProvider{
		agents: []AgentInfo{
			{ID: "agent-1", Type: "leader", Status: "ready", Restarts: 0},
			{ID: "agent-2", Type: "sub", Status: "busy", Restarts: 2},
		},
	}

	rt := &mockRuntime{}
	service := NewDashboardService(rt, agents, nil, nil, nil)

	views := service.ListAgents()
	if len(views) != 2 {
		t.Fatalf("ListAgents count = %d, want 2", len(views))
	}
	if views[0].ID != "agent-1" {
		t.Errorf("views[0].ID = %s, want agent-1", views[0].ID)
	}
	if views[1].Restarts != 2 {
		t.Errorf("views[1].Restarts = %d, want 2", views[1].Restarts)
	}
}

func TestDashboardServiceListAgentsNilProvider(t *testing.T) {
	rt := &mockRuntime{}
	service := NewDashboardService(rt, nil, nil, nil, nil)

	views := service.ListAgents()
	if views != nil {
		t.Errorf("expected nil, got %v", views)
	}
}

func TestDashboardServiceGetAgent(t *testing.T) {
	agents := &mockAgentProvider{
		agents: []AgentInfo{
			{ID: "agent-1", Type: "leader", Status: "ready"},
		},
	}

	rt := &mockRuntime{}
	service := NewDashboardService(rt, agents, nil, nil, nil)

	view, err := service.GetAgent("agent-1")
	if err != nil {
		t.Fatalf("GetAgent error: %v", err)
	}
	if view.ID != "agent-1" {
		t.Errorf("ID = %s, want agent-1", view.ID)
	}
}

func TestDashboardServiceGetAgentNotFound(t *testing.T) {
	agents := &mockAgentProvider{}
	rt := &mockRuntime{}
	service := NewDashboardService(rt, agents, nil, nil, nil)

	_, err := service.GetAgent("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestDashboardServiceGetEvents(t *testing.T) {
	now := time.Now()
	store := &mockEventStore{
		events: []*events.Event{
			{ID: "e1", StreamID: "s1", Type: events.EventAgentStarted, Timestamp: now},
			{ID: "e2", StreamID: "s1", Type: events.EventTaskCreated, Timestamp: now},
		},
	}

	rt := &mockRuntime{}
	service := NewDashboardService(rt, nil, store, nil, nil)

	events, err := service.GetEvents(context.Background(), EventQueryParams{Limit: 10})
	if err != nil {
		t.Fatalf("GetEvents error: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events count = %d, want 2", len(events))
	}
}

func TestDashboardServiceGetEventsWithTypeFilter(t *testing.T) {
	now := time.Now()
	store := &mockEventStore{
		events: []*events.Event{
			{ID: "e1", StreamID: "s1", Type: events.EventAgentStarted, Timestamp: now},
			{ID: "e2", StreamID: "s1", Type: events.EventTaskCreated, Timestamp: now},
		},
	}

	rt := &mockRuntime{}
	service := NewDashboardService(rt, nil, store, nil, nil)

	events, err := service.GetEvents(context.Background(), EventQueryParams{
		Types: []string{"agent.started"},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetEvents error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("events count = %d, want 1", len(events))
	}
}

func TestDashboardServiceGetMCPServers(t *testing.T) {
	rt := &mockRuntime{}
	service := NewDashboardService(rt, nil, nil, nil, nil)

	servers := service.GetMCPServers()
	if servers != nil {
		t.Errorf("expected nil, got %v", servers)
	}
}
