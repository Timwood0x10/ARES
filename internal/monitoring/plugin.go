package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// Sentinel errors for the plugin.
var (
	ErrPluginAlreadyStarted = errors.New("monitor plugin already started")
	ErrPluginNotStarted     = errors.New("monitor plugin not started")
	ErrNotStarted           = errors.New("console not started, call Start first")
	ErrNotImplemented       = errors.New("not implemented")
	ErrAgentNotFound        = errors.New("agent not found")
	ErrCostNotConfigured    = errors.New("cost bar not configured")
	ErrDetailNotConfigured  = errors.New("detail panel not configured")
	ErrInteractionNil       = errors.New("interaction engine not configured")
)

// pluginOptions accumulates deferred configuration for MonitorPlugin.
type pluginOptions struct {
	hub         WSHub
	interval    time.Duration
	runtimeCtrl dag.RuntimeController
	orchCtrl    dag.OrchestratorController
	mcp         MCPManager
	pruneCfg    *PruneConfig
	tracker     AgentTrackerReader
	linker      TraceReader
	hasRuntime  bool
	hasOrch     bool
}

// MonitorPlugin implements ares_runtime.RuntimePlugin and ConsoleAPI.
// It assembles the main page, collector, and publisher into a single
// lifecycle-managed unit. The real entry point is RuntimePlugin.Start(ctx, bus);
// ConsoleAPI has no lifecycle methods of its own.
type MonitorPlugin struct {
	mainPage  *MainPage
	collector *Collector
	publisher *Publisher
	bus       ares_runtime.EventBus

	// Optional sub-components.
	engine      *dag.Engine
	interEngine *dag.InteractionEngine
	mcp         MCPManager
	pruner      *Pruner

	// Deferred options.
	opts pluginOptions

	cancel    context.CancelFunc
	isStarted bool
}

// Option configures the MonitorPlugin.
type Option func(*pluginOptions)

// WithWSHub sets the WebSocket hub for real-time push.
func WithWSHub(hub WSHub) Option {
	return func(o *pluginOptions) {
		o.hub = hub
	}
}

// WithSnapshotInterval sets the publisher push interval.
func WithSnapshotInterval(d time.Duration) Option {
	return func(o *pluginOptions) {
		o.interval = d
	}
}

// WithRuntimeManager sets the runtime controller for interaction actions.
func WithRuntimeManager(ctrl dag.RuntimeController) Option {
	return func(o *pluginOptions) {
		o.runtimeCtrl = ctrl
		o.hasRuntime = true
	}
}

// WithOrchestrator sets the orchestrator controller for interaction actions.
func WithOrchestrator(ctrl dag.OrchestratorController) Option {
	return func(o *pluginOptions) {
		o.orchCtrl = ctrl
		o.hasOrch = true
	}
}

// WithCostAlertThreshold sets the cost alert threshold.
// This is a placeholder for future cost alert integration.
func WithCostAlertThreshold(_ float64) Option {
	return func(_ *pluginOptions) {}
}

// WithMCP sets the MCP manager for tool listing and invocation.
func WithMCP(mcp MCPManager) Option {
	return func(o *pluginOptions) {
		o.mcp = mcp
	}
}

// WithPruneConfig sets the TTL pruning configuration.
func WithPruneConfig(cfg PruneConfig) Option {
	return func(o *pluginOptions) {
		o.pruneCfg = &cfg
	}
}

// WithAgentTracker sets the agent tracker for agent state aggregation.
func WithAgentTracker(tracker AgentTrackerReader) Option {
	return func(o *pluginOptions) {
		o.tracker = tracker
	}
}

// WithTraceLinker sets the trace linker for distributed trace aggregation.
func WithTraceLinkerOption(linker TraceReader) Option {
	return func(o *pluginOptions) {
		o.linker = linker
	}
}

// NewConsole creates a new MonitorPlugin implementing ConsoleAPI.
func NewConsole(opts ...Option) ConsoleAPI {
	o := pluginOptions{interval: 2 * time.Second}
	for _, opt := range opts {
		opt(&o)
	}

	engine := dag.NewEngine()
	mpOpts := []MainPageOption{WithDAG(engine)}
	if o.tracker != nil {
		mpOpts = append(mpOpts, WithTracker(o.tracker))
	}
	if o.linker != nil {
		mpOpts = append(mpOpts, WithTraceLinker(o.linker))
	}
	p := &MonitorPlugin{
		engine:    engine,
		mainPage:  NewMainPage(mpOpts...),
		mcp:       o.mcp,
		opts:      o,
		isStarted: false,
	}

	// Create publisher with accumulated options.
	pubOpts := []PublisherOption{WithInterval(o.interval)}
	if o.hub != nil {
		pubOpts = append(pubOpts, WithHub(o.hub))
	}
	p.publisher = NewPublisher(p.mainPage, pubOpts...)
	if p.publisher != nil {
		p.mainPage.publisher = p.publisher
	}

	// Create InteractionEngine if any controller was provided.
	if o.hasRuntime || o.hasOrch {
		p.interEngine = dag.NewInteractionEngine(engine, o.runtimeCtrl, o.orchCtrl)
	}

	// Create pruner if config was provided.
	if o.pruneCfg != nil {
		p.pruner = NewPruner(p.mainPage, *o.pruneCfg)
	}

	return p
}

// Name returns the plugin name.
func (p *MonitorPlugin) Name() string {
	return "monitor"
}

// Capabilities returns the plugin capabilities.
func (p *MonitorPlugin) Capabilities() []ares_runtime.Capability {
	return []ares_runtime.Capability{ares_runtime.CapObserver}
}

// Start initializes the collector and publisher. The plugin subscribes
// to all events on the bus and begins processing.
func (p *MonitorPlugin) Start(ctx context.Context, bus ares_runtime.EventBus) error {
	if p.isStarted {
		return ErrPluginAlreadyStarted
	}

	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	// Store bus reference for later use by sub-components.
	p.bus = bus

	// Create and start collector (only if bus is provided).
	if bus != nil {
		p.collector = NewCollector(bus, p.mainPage)
		if p.collector != nil {
			if err := p.collector.Start(ctx); err != nil {
				cancel()
				return err
			}
		}
	}

	// Start publisher.
	if p.publisher != nil {
		p.publisher.Start(ctx)
	}

	// Start pruner.
	if p.pruner != nil {
		p.pruner.Start(ctx)
	}

	p.isStarted = true
	return nil
}

// Stop shuts down the publisher and collector, cancelling all goroutines.
func (p *MonitorPlugin) Stop(_ context.Context) error {
	if !p.isStarted {
		return nil
	}

	if p.pruner != nil {
		p.pruner.Stop()
	}
	if p.publisher != nil {
		p.publisher.Stop()
	}
	if p.collector != nil {
		p.collector.Stop()
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}

	p.isStarted = false
	return nil
}

// RegisterRoutes delegates HTTP route registration to the publisher.
func (p *MonitorPlugin) RegisterRoutes(mux *http.ServeMux) {
	if p.publisher != nil {
		p.publisher.RegisterRoutes(mux)
	}
}

// --- ConsoleAPI implementation ---

// Snapshot returns the full console state.
func (p *MonitorPlugin) Snapshot(_ context.Context) (*ConsoleSnapshot, error) {
	snap := p.mainPage.Snapshot()
	return &snap, nil
}

// DAG returns the current DAG snapshot.
func (p *MonitorPlugin) DAG(_ context.Context) (*dag.DAGSnapshot, error) {
	snap := p.engine.Snapshot()
	return &snap, nil
}

// Events returns recent events. Currently returns an empty slice as event
// history is not yet wired.
func (p *MonitorPlugin) Events(_ context.Context, _ int) ([]*ares_events.Event, error) {
	return nil, fmt.Errorf("events: %w", ErrNotImplemented)
}

// Agent returns details for a single agent.
func (p *MonitorPlugin) Agent(_ context.Context, agentID string) (*UnifiedAgent, error) {
	return nil, fmt.Errorf("agent %q: %w", agentID, ErrAgentNotFound)
}

// AgentCost returns the cost breakdown for a specific agent.
func (p *MonitorPlugin) AgentCost(_ context.Context, agentID string) (*AgentCost, error) {
	cb := p.mainPage.CostBar()
	if cb == nil {
		return nil, fmt.Errorf("agent cost: %w", ErrCostNotConfigured)
	}
	cost, ok := cb.GetCost(agentID)
	if !ok {
		return nil, fmt.Errorf("agent cost %q: %w", agentID, ErrAgentNotFound)
	}
	return cost, nil
}

// CostBreakdown returns the full cost breakdown.
func (p *MonitorPlugin) CostBreakdown(_ context.Context) (*CostBreakdown, error) {
	snap := p.mainPage.Snapshot()
	return &snap.Cost, nil
}

// CostAlerts returns active cost alerts. Currently returns an empty slice.
func (p *MonitorPlugin) CostAlerts(_ context.Context) ([]CostAlert, error) {
	return nil, fmt.Errorf("cost alerts: %w", ErrNotImplemented)
}

// Tasks returns all task views, optionally filtered by status.
func (p *MonitorPlugin) Tasks(_ context.Context, _ *dag.NodeStatus) ([]TaskView, error) {
	snap := p.mainPage.Snapshot()
	return snap.Tasks, nil
}

// Traces returns trace spans for a given trace ID.
func (p *MonitorPlugin) Traces(_ context.Context, traceID string) ([]TraceSpan, error) {
	linker := p.mainPage.Linker()
	if linker == nil {
		return nil, fmt.Errorf("traces: %w", ErrNotImplemented)
	}
	spans := linker.GetTrace(traceID)
	return spans, nil
}

// Timeline returns timeline events for a specific node.
func (p *MonitorPlugin) Timeline(_ context.Context, nodeID string) ([]dag.TimelineEvent, error) {
	node, ok := p.engine.GetNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("timeline node %q: %w", nodeID, ErrAgentNotFound)
	}
	return node.Timeline, nil
}

// Actions returns available actions for a specific node.
func (p *MonitorPlugin) Actions(_ context.Context, _ string) ([]NodeAction, error) {
	return []NodeAction{
		{ID: "kill", Name: "Kill", Enabled: true},
		{ID: "resume", Name: "Resume", Enabled: true},
		{ID: "retry", Name: "Retry", Enabled: true},
		{ID: "inspect", Name: "Inspect", Enabled: true},
	}, nil
}

// ExecuteAction performs an action on a node.
func (p *MonitorPlugin) ExecuteAction(ctx context.Context, actionID string) (*ActionResult, error) {
	if p.interEngine == nil {
		return nil, fmt.Errorf("execute action: %w", ErrInteractionNil)
	}
	result, err := p.interEngine.ExecuteAction(ctx, "", actionID)
	if err != nil {
		return nil, err
	}
	return &ActionResult{
		ActionID: result.ActionID,
		Success:  result.Success,
		Message:  result.Message,
	}, nil
}

// Interactions returns recent interactions. Not yet wired.
func (p *MonitorPlugin) Interactions(_ context.Context, _ int) ([]Interaction, error) {
	return nil, fmt.Errorf("interactions: %w", ErrNotImplemented)
}

// Detail returns a detailed view for a selected entity.
func (p *MonitorPlugin) Detail(_ context.Context, _, entityID string) (*DetailView, error) {
	dp := p.mainPage.DetailPanelReader()
	if dp == nil {
		return nil, fmt.Errorf("detail: %w", ErrDetailNotConfigured)
	}
	return dp.GetDetail(entityID)
}

// AgentMemory returns the memory state of an agent. Not yet wired.
func (p *MonitorPlugin) AgentMemory(_ context.Context, _ string) (*AgentMemory, error) {
	return nil, fmt.Errorf("agent memory: %w", ErrNotImplemented)
}

// AgentEvolution returns the evolutionary history of an agent. Not yet wired.
func (p *MonitorPlugin) AgentEvolution(_ context.Context, _ string) (*AgentEvolution, error) {
	return nil, fmt.Errorf("agent evolution: %w", ErrNotImplemented)
}

// MCPToolCalls returns MCP tool call records. Not yet wired.
func (p *MonitorPlugin) MCPToolCalls(_ context.Context, _ string, _ int) ([]MCPToolCall, error) {
	return nil, fmt.Errorf("MCP tool calls: %w", ErrNotImplemented)
}

// LLMCalls returns LLM call records. Not yet wired.
func (p *MonitorPlugin) LLMCalls(_ context.Context, _ string, _ int) ([]LLMCallRecord, error) {
	return nil, fmt.Errorf("LLM calls: %w", ErrNotImplemented)
}

// Recommendations returns current recommendations. Not yet wired.
func (p *MonitorPlugin) Recommendations(_ context.Context) ([]Recommendation, error) {
	return nil, fmt.Errorf("recommendations: %w", ErrNotImplemented)
}

// ListMCPTools returns all available MCP tools.
func (p *MonitorPlugin) ListMCPTools(ctx context.Context) ([]MCPToolInfo, error) {
	if p.mcp == nil {
		return nil, fmt.Errorf("list MCP tools: %w", ErrNotImplemented)
	}
	return p.mcp.ListTools(ctx)
}

// CallMCPTool invokes an MCP tool by name with the given arguments.
func (p *MonitorPlugin) CallMCPTool(ctx context.Context, toolName string, args map[string]any) (*MCPToolResult, error) {
	if p.mcp == nil {
		return nil, fmt.Errorf("call MCP tool: %w", ErrNotImplemented)
	}
	return p.mcp.CallTool(ctx, toolName, args)
}

// RunHTTPServer starts the Gin-based HTTP server on the given address.
// This is a convenience method that creates an HTTPServer if one is not
// already attached.
func (p *MonitorPlugin) RunHTTPServer(addr string) error {
	srv := NewHTTPServer(p)
	return srv.Run(addr)
}
