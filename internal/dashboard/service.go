package dashboard

import (
	"context"
	"fmt"
	"time"

	"goagentx/internal/events"
	"goagentx/internal/memory"
	"goagentx/internal/runtime"
)

// AgentInfo holds basic agent information for the dashboard.
type AgentInfo struct {
	ID       string
	Type     string
	Status   string
	Restarts int
}

// AgentProvider abstracts agent listing for the dashboard.
// The runtime.Manager will implement this.
type AgentProvider interface {
	ListAgents() []AgentInfo
	GetAgent(id string) (*AgentInfo, bool)
}

// MCPStatusProvider abstracts MCP status for the dashboard.
// The mcp.MCPManager will implement this via MCPServerLister.
type MCPStatusProvider interface {
	ListServers() []MCPServerStatusView
}

// MCPServerStatusView is a dashboard-safe view of MCP server status.
type MCPServerStatusView struct {
	Name      string        `json:"name"`
	Connected bool          `json:"connected"`
	ToolCount int           `json:"tool_count"`
	Version   string        `json:"version"`
	Error     string        `json:"error,omitempty"`
	ConnAt    time.Time     `json:"connected_at,omitempty"`
	Tools     []MCPToolView `json:"tools"`
}

// DashboardConfig holds configuration for the dashboard.
type DashboardConfig struct {
	Addr           string        `yaml:"addr" json:"addr"`
	EnableAuth     bool          `yaml:"enable_auth" json:"enable_auth"`
	StaticDir      string        `yaml:"static_dir" json:"static_dir"`
	WSPingInterval time.Duration `yaml:"ws_ping_interval" json:"ws_ping_interval"`
}

// DashboardService provides data access for dashboard endpoints.
type DashboardService struct {
	rt         runtime.Runtime
	agents     AgentProvider
	eventStore events.EventStore
	memMgr     memory.MemoryManager
	mcpMgr     MCPStatusProvider
	startTime  time.Time
}

// NewDashboardService creates a new DashboardService.
// mcpMgr may be nil if MCP is not configured.
func NewDashboardService(
	rt runtime.Runtime,
	agents AgentProvider,
	eventStore events.EventStore,
	memMgr memory.MemoryManager,
	mcpMgr MCPStatusProvider,
) *DashboardService {
	return &DashboardService{
		rt:         rt,
		agents:     agents,
		eventStore: eventStore,
		memMgr:     memMgr,
		mcpMgr:     mcpMgr,
		startTime:  time.Now(),
	}
}

// GetOverview returns the system overview.
func (s *DashboardService) GetOverview(ctx context.Context) (*SystemOverview, error) {
	overview := &SystemOverview{
		Uptime:      time.Since(s.startTime).Round(time.Second).String(),
		AgentCount:  0,
		ActiveTasks: 0,
		TotalEvents: 0,
	}

	if s.rt != nil {
		stats := s.rt.Stats()
		overview.AgentCount = stats.ActiveAgents
		overview.RuntimeStats = RuntimeStatsView{
			ActiveAgents:  stats.ActiveAgents,
			TotalRestarts: stats.TotalRestarts,
			UptimeSeconds: int64(stats.Uptime.Seconds()),
		}
	}

	// TODO: EventStore does not expose a Count method; TotalEvents is reported as 0
	// until a dedicated counting mechanism is added to the events package.

	// Memory stats.
	if s.memMgr != nil {
		overview.MemoryStats = s.getMemoryStats(ctx)
	}

	// MCP status.
	if s.mcpMgr != nil {
		overview.MCPStatus = s.getMCPOverview()
	}

	return overview, nil
}

// ListAgents returns all agents.
func (s *DashboardService) ListAgents() []AgentView {
	if s.agents == nil {
		return nil
	}

	infos := s.agents.ListAgents()
	views := make([]AgentView, 0, len(infos))

	for _, info := range infos {
		views = append(views, AgentView{
			ID:       info.ID,
			Type:     info.Type,
			Status:   info.Status,
			Restarts: info.Restarts,
		})
	}

	return views
}

// GetAgent returns a specific agent by ID.
func (s *DashboardService) GetAgent(id string) (*AgentView, error) {
	if s.agents == nil {
		return nil, fmt.Errorf("agent provider not available")
	}

	info, ok := s.agents.GetAgent(id)
	if !ok {
		return nil, fmt.Errorf("agent %s not found", id)
	}

	return &AgentView{
		ID:       info.ID,
		Type:     info.Type,
		Status:   info.Status,
		Restarts: info.Restarts,
	}, nil
}

// GetAgentEvents returns events for a specific agent.
func (s *DashboardService) GetAgentEvents(ctx context.Context, agentID string, limit int) ([]EventView, error) {
	if s.eventStore == nil {
		return nil, nil
	}

	if limit <= 0 {
		limit = 50
	}

	events, err := s.eventStore.Read(ctx, agentID, events.ReadOptions{
		Limit:     limit,
		Direction: events.ReadDescending,
	})
	if err != nil {
		return nil, fmt.Errorf("read agent events: %w", err)
	}

	return toEventViews(events), nil
}

// GetEvents returns events with optional filtering.
func (s *DashboardService) GetEvents(ctx context.Context, params EventQueryParams) ([]EventView, error) {
	if s.eventStore == nil {
		return nil, nil
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	opts := events.ReadOptions{
		Limit: limit,
		Since: params.Since,
	}

	if params.Direction == "desc" {
		opts.Direction = events.ReadDescending
	} else {
		opts.Direction = events.ReadAscending
	}

	var evts []*events.Event
	var err error

	if params.StreamID != "" {
		evts, err = s.eventStore.Read(ctx, params.StreamID, opts)
	} else {
		evts, err = s.eventStore.ReadAll(ctx, opts)
	}

	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}

	views := toEventViews(evts)

	// Filter by event types if specified.
	if len(params.Types) > 0 {
		typeSet := make(map[string]struct{}, len(params.Types))
		for _, t := range params.Types {
			typeSet[t] = struct{}{}
		}
		filtered := make([]EventView, 0, len(views))
		for _, v := range views {
			if _, ok := typeSet[v.Type]; ok {
				filtered = append(filtered, v)
			}
		}
		views = filtered
	}

	return views, nil
}

// GetSessions returns memory sessions.
func (s *DashboardService) GetSessions(_ context.Context) []SessionView {
	// MemoryManager doesn't expose session listing directly.
	// This will be enhanced when the memory package gains ListSessions.
	return nil
}

// GetSessionMessages returns messages for a specific session.
func (s *DashboardService) GetSessionMessages(ctx context.Context, sessionID string) (*SessionView, error) {
	if s.memMgr == nil {
		return nil, fmt.Errorf("memory manager not available")
	}

	msgs, err := s.memMgr.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	views := make([]MessageView, 0, len(msgs))
	for _, m := range msgs {
		views = append(views, MessageView{
			Role:    m.Role,
			Content: m.Content,
			Time:    m.Time,
		})
	}

	return &SessionView{
		SessionID:    sessionID,
		MessageCount: len(views),
		Messages:     views,
	}, nil
}

// SearchMemory searches distilled memories.
func (s *DashboardService) SearchMemory(ctx context.Context, query string, limit int) ([]DistilledMemoryView, error) {
	if s.memMgr == nil {
		return nil, fmt.Errorf("memory manager not available")
	}

	if limit <= 0 {
		limit = 10
	}

	tasks, err := s.memMgr.SearchSimilarTasks(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}

	views := make([]DistilledMemoryView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, DistilledMemoryView{
			ID:        t.TaskID,
			Type:      string(t.TaskType),
			Content:   fmt.Sprintf("%v", t.Payload),
			Source:    string(t.AgentType),
			CreatedAt: t.CreatedAt,
		})
	}

	return views, nil
}

// GetMCPServers returns MCP server status.
func (s *DashboardService) GetMCPServers() []MCPServerView {
	if s.mcpMgr == nil {
		return nil
	}

	servers := s.mcpMgr.ListServers()
	views := make([]MCPServerView, 0, len(servers))

	for _, srv := range servers {
		view := MCPServerView{
			Name:      srv.Name,
			Connected: srv.Connected,
			Version:   srv.Version,
			Error:     srv.Error,
			ConnAt:    srv.ConnAt,
			Tools:     srv.Tools,
		}
		views = append(views, view)
	}

	return views
}

// getMemoryStats returns memory subsystem statistics.
func (s *DashboardService) getMemoryStats(_ context.Context) MemoryStats {
	// MemoryManager doesn't expose stats directly.
	// Return placeholder values.
	return MemoryStats{}
}

// getMCPOverview returns MCP subsystem overview.
func (s *DashboardService) getMCPOverview() *MCPOverview {
	servers := s.mcpMgr.ListServers()
	overview := &MCPOverview{
		ServerCount: len(servers),
	}

	for _, srv := range servers {
		if srv.Connected {
			overview.ConnectedCount++
		}
		overview.TotalTools += srv.ToolCount
	}

	return overview
}

// toEventViews converts events to EventView slices.
func toEventViews(evts []*events.Event) []EventView {
	views := make([]EventView, 0, len(evts))
	for _, e := range evts {
		views = append(views, EventView{
			ID:        e.ID,
			StreamID:  e.StreamID,
			Type:      string(e.Type),
			Payload:   e.Payload,
			Version:   e.Version,
			Timestamp: e.Timestamp,
		})
	}
	return views
}
