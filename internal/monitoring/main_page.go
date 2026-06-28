package monitoring

import (
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// Tab is the interface that all monitoring tabs must implement.
// It mirrors tabs.Tab to avoid import cycles.
type Tab interface {
	Name() string
	Label() string
	HandleEvent(evt *ares_events.Event)
	Snapshot() any
}

// CostBarReader provides read access to cost bar state.
type CostBarReader interface {
	HandleEvent(evt *ares_events.Event)
	Snapshot() CostBarBreakdown
	Total() AgentCost
	GetCost(agentID string) (*AgentCost, bool)
}

// DAGEngineReader provides read access to the DAG engine state.
type DAGEngineReader interface {
	HandleEvent(evt *ares_events.Event)
	Snapshot() dag.DAGSnapshot
	GetNode(id string) (*dag.DAGNode, bool)
}

// DetailPanelReader provides read access to the detail panel.
type DetailPanelReader interface {
	HandleEvent(evt *ares_events.Event)
	GetDetail(agentID string) (*DetailView, error)
	SetViewedAgent(agentID string)
}

// MainPage assembles the full console state from sub-components.
// It dispatches events to all registered components and produces
// unified snapshots for the console UI.
type MainPage struct {
	mu          sync.RWMutex
	costBar     CostBarReader
	engine      DAGEngineReader
	detailPanel DetailPanelReader
	tabs        map[string]Tab
	publisher   *Publisher
}

// MainPageOption configures optional dependencies for MainPage.
type MainPageOption func(*MainPage)

// WithCostBar sets the cost bar for the main page.
func WithCostBar(cb CostBarReader) MainPageOption {
	return func(mp *MainPage) {
		mp.costBar = cb
	}
}

// WithDAG sets the DAG engine for the main page.
func WithDAG(engine DAGEngineReader) MainPageOption {
	return func(mp *MainPage) {
		mp.engine = engine
	}
}

// WithDetailPanel sets the detail panel for the main page.
func WithDetailPanel(dp DetailPanelReader) MainPageOption {
	return func(mp *MainPage) {
		mp.detailPanel = dp
	}
}

// WithTabs sets the tab map for the main page.
func WithTabs(tabs map[string]Tab) MainPageOption {
	return func(mp *MainPage) {
		mp.tabs = tabs
	}
}

// WithPublisher sets the publisher for the main page.
func WithPublisher(pub *Publisher) MainPageOption {
	return func(mp *MainPage) {
		mp.publisher = pub
	}
}

// NewMainPage creates a MainPage with the given options.
// Defaults: empty cost bar, empty engine, empty tabs map.
func NewMainPage(opts ...MainPageOption) *MainPage {
	mp := &MainPage{
		costBar: NewCostBar(),
		engine:  dag.NewEngine(),
		tabs:    make(map[string]Tab),
	}
	for _, opt := range opts {
		opt(mp)
	}
	return mp
}

// HandleEvent dispatches an event to all sub-components: cost bar,
// DAG engine, detail panel, and all registered tabs.
func (mp *MainPage) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}

	mp.mu.RLock()
	costBar := mp.costBar
	engine := mp.engine
	detail := mp.detailPanel
	tabList := make([]Tab, 0, len(mp.tabs))
	for _, t := range mp.tabs {
		tabList = append(tabList, t)
	}
	mp.mu.RUnlock()

	if costBar != nil {
		costBar.HandleEvent(evt)
	}
	if engine != nil {
		engine.HandleEvent(evt)
	}
	if detail != nil {
		detail.HandleEvent(evt)
	}
	for _, t := range tabList {
		t.HandleEvent(evt)
	}
}

// Snapshot assembles the full console state from all sub-components.
func (mp *MainPage) Snapshot() ConsoleSnapshot {
	mp.mu.RLock()
	costBar := mp.costBar
	engine := mp.engine
	mp.mu.RUnlock()

	snap := ConsoleSnapshot{
		UpdateTime: time.Now(),
	}

	if costBar != nil {
		breakdown := costBar.Snapshot()
		snap.Cost = CostBreakdown{
			Total:    breakdown.Total,
			Currency: breakdown.Currency,
			ByAgent:  make(map[string]AgentCost),
		}
		for _, entry := range breakdown.Entries {
			snap.Cost.ByAgent[entry.AgentID] = AgentCost{
				AgentID:       entry.AgentID,
				EstimatedCost: entry.EstimatedCost,
				Currency:      entry.Currency,
				CallCount:     entry.CallCount,
			}
		}
	}

	if engine != nil {
		dagSnap := engine.Snapshot()
		snap.Tasks = make([]TaskView, 0)
		for _, node := range dagSnap.Nodes {
			if node.Type == "task" {
				snap.Tasks = append(snap.Tasks, TaskView{
					ID:        node.ID,
					Name:      node.Name,
					Status:    node.Status,
					AgentID:   node.ParentID,
					StartedAt: node.CreatedAt,
				})
			}
		}
	}

	return snap
}

// GetTab returns a specific tab by name.
func (mp *MainPage) GetTab(name string) (Tab, bool) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	t, ok := mp.tabs[name]
	return t, ok
}

// CostBar returns the cost bar reader for external access.
func (mp *MainPage) CostBar() CostBarReader {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.costBar
}

// DAGEngine returns the DAG engine reader for external access.
func (mp *MainPage) DAGEngine() DAGEngineReader {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.engine
}

// DetailPanel returns the detail panel reader for external access.
func (mp *MainPage) DetailPanelReader() DetailPanelReader {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.detailPanel
}
