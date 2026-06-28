package monitoring

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// Sentinel errors for the detail panel.
var (
	ErrEmptyAgentID = errors.New("agent ID must not be empty")
)

// TrackerReader provides read access to tracked agent state.
type TrackerReader interface {
	GetAgent(id string) (*UnifiedAgent, bool)
	ListAgents() []UnifiedAgent
	Snapshot() ConsoleStats
}

// AgentLinker provides interaction link data for an agent.
type AgentLinker interface {
	GetLinks(agentID string) []Interaction
}

// CostProvider provides cost data for an agent.
type CostProvider interface {
	GetCost(agentID string) (*AgentCost, bool)
}

// DetailPanel assembles a detailed view for a selected agent.
// It can optionally incorporate interaction links and cost data.
type DetailPanel struct {
	mu          sync.RWMutex
	tracker     TrackerReader
	linker      AgentLinker
	cost        CostProvider
	viewedAgent string
}

// DetailOption configures optional dependencies for DetailPanel.
type DetailOption func(*DetailPanel)

// WithLinker sets the interaction linker for the detail panel.
func WithLinker(linker AgentLinker) DetailOption {
	return func(dp *DetailPanel) {
		dp.linker = linker
	}
}

// WithCostProvider sets the cost provider for the detail panel.
func WithCostProvider(provider CostProvider) DetailOption {
	return func(dp *DetailPanel) {
		dp.cost = provider
	}
}

// NewDetailPanel creates a new DetailPanel.
// The tracker must not be nil.
func NewDetailPanel(tracker TrackerReader, opts ...DetailOption) *DetailPanel {
	if tracker == nil {
		panic("tracker must not be nil")
	}
	dp := &DetailPanel{
		tracker: tracker,
	}
	for _, opt := range opts {
		opt(dp)
	}
	return dp
}

// SetViewedAgent sets which agent the detail panel is currently viewing.
func (dp *DetailPanel) SetViewedAgent(agentID string) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	dp.viewedAgent = agentID
}

// GetDetail assembles a full DetailView for the specified agent.
func (dp *DetailPanel) GetDetail(agentID string) (*DetailView, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w", ErrEmptyAgentID)
	}

	agent, ok := dp.tracker.GetAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	view := &DetailView{
		EntityType: "agent",
		EntityID:   agentID,
		Tabs:       []string{"overview", "timeline", "cost", "interactions"},
		Data: map[string]any{
			"id":         agent.ID,
			"name":       agent.Name,
			"status":     string(agent.Status),
			"role":       agent.Role,
			"model_name": agent.ModelName,
			"task_id":    agent.TaskID,
			"parent_id":  agent.ParentID,
			"started_at": agent.StartedAt,
			"updated_at": agent.UpdatedAt,
		},
	}

	if agent.Tags != nil {
		view.Data["tags"] = agent.Tags
	}
	if agent.Metadata != nil {
		view.Data["metadata"] = agent.Metadata
	}

	// Enrich with cost data if provider is available.
	if dp.cost != nil {
		if cost, ok := dp.cost.GetCost(agentID); ok {
			view.Data["cost"] = cost
		}
	}

	// Enrich with interaction links if linker is available.
	if dp.linker != nil {
		links := dp.linker.GetLinks(agentID)
		if links != nil {
			view.Data["interactions"] = links
		}
	}

	return view, nil
}

// HandleEvent processes an event and updates the viewed agent if it matches.
func (dp *DetailPanel) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}

	dp.mu.RLock()
	viewed := dp.viewedAgent
	dp.mu.RUnlock()

	if viewed == "" {
		return
	}

	agentID := extractDetailAgentID(evt)
	if agentID != viewed {
		return
	}

	// The tracker is updated separately via its own HandleEvent.
	// This method exists to allow the panel to react to changes
	// for the currently viewed agent (e.g., trigger a UI refresh).
}

// extractDetailAgentID reads the agent_id from event payload, falling back to StreamID.
func extractDetailAgentID(evt *ares_events.Event) string {
	if evt.Payload == nil {
		return evt.StreamID
	}
	v, ok := evt.Payload["agent_id"]
	if !ok {
		return evt.StreamID
	}
	s, ok := v.(string)
	if !ok {
		return evt.StreamID
	}
	if s == "" {
		return evt.StreamID
	}
	return s
}
