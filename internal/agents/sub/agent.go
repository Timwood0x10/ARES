package sub

import (
	"context"
	"sync"

	"github.com/Timwood0x10/goagent/internal/agents/base"
	"github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/protocol/ahp"
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
}

// MessageHandler handles incoming messages.
type MessageHandler interface {
	Handle(ctx context.Context, msg *ahp.AHPMessage) error
}

// ToolBinder binds tools to the agent.
type ToolBinder interface {
	BindTool(name string, toolFunc func(ctx context.Context, args map[string]any) (any, error))
	CallTool(ctx context.Context, name string, args map[string]any) (any, error)
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
) Agent {
	if cfg == nil {
		cfg = DefaultSubAgentConfig(agentType)
	}
	cfg.ID = id
	cfg.Type = agentType

	return &subAgent{
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
	if a.Status() != models.AgentStatusOffline {
		return errors.ErrAgentAlreadyStarted
	}

	a.setStatus(models.AgentStatusStarting)
	a.setStatus(models.AgentStatusReady)
	return nil
}

// Stop stops the sub agent.
func (a *subAgent) Stop(ctx context.Context) error {
	if a.Status() == models.AgentStatusOffline {
		return errors.ErrAgentNotRunning
	}

	a.setStatus(models.AgentStatusStopping)
	a.setStatus(models.AgentStatusOffline)
	return nil
}

// Process handles input and returns result.
func (a *subAgent) Process(ctx context.Context, input any) (any, error) {
	if a.Status() != models.AgentStatusReady && a.Status() != models.AgentStatusOffline {
		return nil, errors.ErrAgentNotReady
	}

	if a.Status() == models.AgentStatusOffline {
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
	return a.executor.Execute(ctx, task)
}
