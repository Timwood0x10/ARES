package sub

import (
	"context"
	"sync"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Agent represents the Sub Agent interface.
type Agent interface {
	base.Agent
	// Execute executes a task and returns result.
	Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error)
}

// TaskExecutor executes tasks.
type TaskExecutor interface {
	Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error)
	// RegisterFallback registers a type-specific handler used when the LLM
	// is unavailable or execution fails.
	RegisterFallback(agentType models.AgentType, handler FallbackHandler)
}

// MessageHandler handles incoming messages.
type MessageHandler interface {
	Handle(ctx context.Context, msg *ahp.AHPMessage) error
}

// ToolBinder binds tools to the agent.
type ToolBinder interface {
	BindTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error))
	CallTool(ctx context.Context, name string, args map[string]any) (any, error)
	ListTools() []string
	IsToolIdempotent(name string) bool
	ListIdempotentTools() []string
	GetToolSchemas() []resources.ToolSchema
	BridgeFromRegistry(registry *resources.Registry)
	WithPlannerBridge(bridge interface {
		Execute(ctx context.Context, toolName string, params map[string]any, userRequest string) (resources.Result, error)
	})
}

// Compile-time check: subAgent must satisfy base.StatefulAgent.
var _ base.StatefulAgent = (*subAgent)(nil)

// SubAgentOption configures a subAgent instance.
type SubAgentOption func(*subAgent)

// WithEventStore sets the event store for event sourcing.
func WithEventStore(store ares_events.EventStore) SubAgentOption {
	return func(a *subAgent) {
		a.eventStore = store
	}
}

// subAgent implements a Sub Agent.
type subAgent struct {
	mu           sync.RWMutex
	id           string
	agentType    models.AgentType
	status       models.AgentStatus
	config       *SubAgentConfig
	executor     TaskExecutor
	handler      MessageHandler
	tools        map[string]func(ctx context.Context, args map[string]any) (any, error)
	messageQueue *ahp.MessageQueue
	heartbeatMon *ahp.HeartbeatMonitor
	eventStore   ares_events.EventStore

	// Lifecycle management
	stopCh   chan struct{}  // Signals goroutines to stop.
	streamWg sync.WaitGroup // Tracks active ProcessStream goroutines.
}

// SubAgentConfig holds configuration for SubAgent.
type SubAgentConfig struct {
	base.Config
	EnableTools bool
}

// New creates a new SubAgent instance.
func New(
	id string,
	agentType models.AgentType,
	executor TaskExecutor,
	handler MessageHandler,
	msgQueue *ahp.MessageQueue,
	hbMon *ahp.HeartbeatMonitor,
	cfg *SubAgentConfig,
	opts ...SubAgentOption,
) Agent {
	if cfg == nil {
		cfg = DefaultSubAgentConfig(agentType)
	}
	cfg.ID = id
	cfg.Type = agentType

	a := &subAgent{
		id:           id,
		agentType:    agentType,
		status:       models.AgentStatusOffline,
		config:       cfg,
		executor:     executor,
		handler:      handler,
		tools:        make(map[string]func(ctx context.Context, args map[string]any) (any, error)),
		messageQueue: msgQueue,
		heartbeatMon: hbMon,
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// DefaultSubAgentConfig returns default configuration.
func DefaultSubAgentConfig(agentType models.AgentType) *SubAgentConfig {
	return &SubAgentConfig{
		Config:      *base.DefaultConfig(agentType),
		EnableTools: true,
	}
}

// ID returns the unique identifier.
func (a *subAgent) ID() string {
	return a.id
}

// Type returns the agent type.
func (a *subAgent) Type() models.AgentType {
	return a.agentType
}

// Status returns the current status.
func (a *subAgent) Status() models.AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *subAgent) setStatus(status models.AgentStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
}

// Start starts the sub agent.
func (a *subAgent) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.status != models.AgentStatusOffline {
		a.mu.Unlock()
		return errors.ErrAgentAlreadyStarted
	}
	a.status = models.AgentStatusStarting
	a.stopCh = make(chan struct{})
	a.mu.Unlock()

	a.setStatus(models.AgentStatusReady)

	// Wire event store to executor for tool/LLM call ares_events.
	if a.eventStore != nil {
		if setter, ok := a.executor.(interface {
			SetEventStore(ares_events.EventStore, string)
		}); ok {
			setter.SetEventStore(a.eventStore, a.id)
		}
	}

	a.emitEvent(ctx, ares_events.EventAgentStarted, map[string]any{
		"agent_id": a.id,
		"type":     string(a.agentType),
	})

	return nil
}

// Stop stops the sub agent and waits for active stream goroutines.
func (a *subAgent) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.status == models.AgentStatusOffline {
		a.mu.Unlock()
		return errors.ErrAgentNotRunning
	}
	a.status = models.AgentStatusStopping
	stopCh := a.stopCh
	a.mu.Unlock()

	// Signal all goroutines to stop and wait for them.
	if stopCh != nil {
		close(stopCh)
	}
	a.streamWg.Wait()

	a.emitEvent(ctx, ares_events.EventAgentStopped, map[string]any{
		"agent_id": a.id,
	})

	a.setStatus(models.AgentStatusOffline)
	return nil
}

// Process handles input and returns result.
func (a *subAgent) Process(ctx context.Context, input any) (any, error) {
	a.mu.RLock()
	status := a.status
	a.mu.RUnlock()

	if status != models.AgentStatusReady && status != models.AgentStatusOffline {
		return nil, errors.ErrAgentNotReady
	}

	if status == models.AgentStatusOffline {
		if err := a.Start(ctx); err != nil {
			return nil, err
		}
	}

	a.setStatus(models.AgentStatusBusy)
	defer a.setStatus(models.AgentStatusReady)

	task, ok := input.(*models.Task)
	if !ok {
		return nil, errors.ErrInvalidInput
	}

	if a.executor == nil {
		return nil, errors.ErrInvalidState
	}

	return a.executor.Execute(ctx, task)
}

// SendMessage sends a message to another agent.
func (a *subAgent) SendMessage(ctx context.Context, msg *ahp.AHPMessage) error {
	if a.messageQueue == nil {
		return errors.ErrQueueNotInitialized
	}
	return a.messageQueue.Enqueue(ctx, msg)
}

// ReceiveMessage receives a message from the message queue.
func (a *subAgent) ReceiveMessage(ctx context.Context) (*ahp.AHPMessage, error) {
	if a.messageQueue == nil {
		return nil, errors.ErrQueueNotInitialized
	}
	return a.messageQueue.Dequeue(ctx)
}

// Heartbeat sends a heartbeat signal.
func (a *subAgent) Heartbeat(ctx context.Context) error {
	if a.heartbeatMon == nil {
		return nil
	}
	a.heartbeatMon.RecordHeartbeat(a.id)
	return nil
}

// IsAlive checks if the agent is alive.
func (a *subAgent) IsAlive() bool {
	return a.Status() == models.AgentStatusReady || a.Status() == models.AgentStatusBusy
}

// Execute executes a task and returns result.
func (a *subAgent) Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
	if a.executor == nil {
		return nil, errors.ErrNilPointer
	}

	a.emitEvent(ctx, ares_events.EventTaskCreated, map[string]any{
		"task_id":  task.TaskID,
		"agent_id": a.id,
	})

	result, err := a.executor.Execute(ctx, task)
	if err != nil {
		a.emitEvent(ctx, ares_events.EventTaskFailed, map[string]any{
			"task_id":  task.TaskID,
			"agent_id": a.id,
			"error":    err.Error(),
		})
		return nil, err
	}

	a.emitEvent(ctx, ares_events.EventTaskCompleted, map[string]any{
		"task_id":  task.TaskID,
		"agent_id": a.id,
	})

	return result, nil
}

// ProcessStream handles input and returns a stream of ares_events.
func (a *subAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	if a.Status() != models.AgentStatusReady && a.Status() != models.AgentStatusOffline {
		return nil, errors.ErrAgentNotReady
	}

	if a.Status() == models.AgentStatusOffline {
		if err := a.Start(ctx); err != nil {
			return nil, err
		}
	}

	task, ok := input.(*models.Task)
	if !ok {
		return nil, errors.ErrInvalidInput
	}

	if a.executor == nil {
		return nil, errors.ErrInvalidState
	}

	ch := make(chan base.AgentEvent, 64)

	a.mu.RLock()
	stopCh := a.stopCh
	a.mu.RUnlock()

	a.streamWg.Add(1)
	go func() {
		defer close(ch)
		defer a.streamWg.Done()

		a.setStatus(models.AgentStatusBusy)
		defer a.setStatus(models.AgentStatusReady)

		// Send task start event
		select {
		case ch <- base.AgentEvent{Type: base.EventTaskStart, Source: a.id, Data: task}:
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		}

		a.emitEvent(ctx, ares_events.EventTaskCreated, map[string]any{
			"task_id":  task.TaskID,
			"agent_id": a.id,
		})

		// Execute task
		result, err := a.executor.Execute(ctx, task)
		if err != nil {
			a.emitEvent(ctx, ares_events.EventTaskFailed, map[string]any{
				"task_id":  task.TaskID,
				"agent_id": a.id,
				"error":    err.Error(),
			})

			select {
			case ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Err: err}:
			case <-ctx.Done():
			case <-stopCh:
			}
			return
		}

		a.emitEvent(ctx, ares_events.EventTaskCompleted, map[string]any{
			"task_id":  task.TaskID,
			"agent_id": a.id,
		})

		// Send task complete event
		select {
		case ch <- base.AgentEvent{Type: base.EventTaskComplete, Source: a.id, Data: result}:
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		}

		// Send final result
		select {
		case ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Data: result}:
		case <-ctx.Done():
		case <-stopCh:
		}
	}()

	return ch, nil
}

// RestoreState restores the sub-agent's state from persisted data.
// Implements base.StatefulAgent for resurrection support.
func (a *subAgent) RestoreState(state map[string]any) error {
	if state == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Sub-agents are simpler than leaders — just restore status if needed.
	if status, ok := state["status"].(string); ok && status != "" {
		a.status = models.AgentStatus(status)
	}
	return nil
}

// ReplayEvents replays ares_events to reconstruct sub-agent state.
// Implements base.StatefulAgent for resurrection support.
func (a *subAgent) ReplayEvents(evts []*ares_events.Event) error {
	if len(evts) == 0 {
		return nil
	}
	// Sub-agents track task completion for operational recovery.
	for _, ev := range evts {
		if ev == nil {
			continue
		}
		if ev.Type == ares_events.EventTaskCompleted {
			log.Debug("sub-agent replayed task completion",
				"agent_id", a.id,
				"task_id", ev.Payload["task_id"],
			)
		}
	}
	return nil
}

// Snapshot returns a serializable snapshot of the sub-agent's state.
// Implements base.StatefulAgent for resurrection support.
func (a *subAgent) Snapshot() (map[string]any, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return map[string]any{
		"agent_id": a.id,
		"status":   string(a.status),
	}, nil
}

// emitEvent appends a single event using the canonical ares_events.Emit.
func (a *subAgent) emitEvent(ctx context.Context, eventType ares_events.EventType, payload map[string]any) {
	if ares_events.Emit(ctx, a.eventStore, a.id, eventType, "sub", payload) {
		log.Debug("event emitted", "agent_id", a.id, "type", eventType)
	}
}
