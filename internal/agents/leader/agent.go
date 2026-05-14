package leader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/goagent/internal/agents/base"
	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/memory"
	"github.com/Timwood0x10/goagent/internal/protocol/ahp"

	"golang.org/x/sync/errgroup"
)

// Agent represents the Leader Agent interface.
type Agent interface {
	base.Agent
}

// ProfileParser parses user profile from input.
type ProfileParser interface {
	Parse(ctx context.Context, input string) (*models.UserProfile, error)
}

// TaskPlanner plans tasks based on user profile and input text.
type TaskPlanner interface {
	Plan(ctx context.Context, profile *models.UserProfile, inputText string) ([]*models.Task, error)
}

// TaskDispatcher dispatches tasks to sub-agents.
type TaskDispatcher interface {
	Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error)
	RegisterExecutor(agentType models.AgentType, fn func(ctx context.Context, task *models.Task) (*models.TaskResult, error))
}

// ResultAggregator aggregates results from sub-agents.
type ResultAggregator interface {
	Aggregate(ctx context.Context, results []*models.TaskResult, tasks []*models.Task) (*models.RecommendResult, error)
}

// leaderAgent implements the Leader Agent.
type leaderAgent struct {
	mu            sync.RWMutex
	id            string
	agentType     models.AgentType
	status        models.AgentStatus
	config        *LeaderAgentConfig
	parser        ProfileParser
	planner       TaskPlanner
	dispatcher    TaskDispatcher
	aggregator    ResultAggregator
	messageQueue  *ahp.MessageQueue
	heartbeatMon  *ahp.HeartbeatMonitor
	memoryManager memory.MemoryManager
	sessionID     string

	// Lifecycle management
	stopCh      chan struct{}  // Channel to signal shutdown
	distillWg   sync.WaitGroup // WaitGroup for distillation goroutines
	cleanupOnce sync.Once      // Ensure cleanup runs only once
}

// LeaderAgentConfig holds configuration for LeaderAgent.
type LeaderAgentConfig struct {
	base.Config
	MaxParallelTasks int
	MaxSteps         int
	EnableCache      bool
}

// New creates a new LeaderAgent instance.
func New(
	id string,
	parser ProfileParser,
	planner TaskPlanner,
	dispatcher TaskDispatcher,
	aggregator ResultAggregator,
	msgQueue *ahp.MessageQueue,
	hbMon *ahp.HeartbeatMonitor,
	memMgr memory.MemoryManager,
	cfg *LeaderAgentConfig,
) Agent {
	if cfg == nil {
		cfg = DefaultLeaderAgentConfig()
	}
	cfg.ID = id
	cfg.Type = models.AgentTypeLeader

	return &leaderAgent{
		id:            id,
		agentType:     models.AgentTypeLeader,
		status:        models.AgentStatusOffline,
		config:        cfg,
		parser:        parser,
		planner:       planner,
		dispatcher:    dispatcher,
		aggregator:    aggregator,
		messageQueue:  msgQueue,
		heartbeatMon:  hbMon,
		memoryManager: memMgr,
	}
}

// DefaultLeaderAgentConfig returns default configuration.
func DefaultLeaderAgentConfig() *LeaderAgentConfig {
	return &LeaderAgentConfig{
		Config:           *base.DefaultConfig(models.AgentTypeLeader),
		MaxParallelTasks: DefaultMaxParallelTasks,
		MaxSteps:         DefaultMaxSteps,
		EnableCache:      true,
	}
}

// ID returns the unique identifier.
func (a *leaderAgent) ID() string {
	return a.id
}

// Type returns the agent type.
func (a *leaderAgent) Type() models.AgentType {
	return a.agentType
}

// Status returns the current status.
func (a *leaderAgent) Status() models.AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *leaderAgent) setStatus(status models.AgentStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
}

// Start starts the leader agent.
func (a *leaderAgent) Start(ctx context.Context) error {
	if a.Status() != models.AgentStatusOffline {
		return coreerrors.ErrAgentAlreadyStarted
	}

	a.setStatus(models.AgentStatusStarting)

	// Validate and initialize dependencies
	if a.parser == nil {
		return coreerrors.ErrProfileParserNotInitialized
	}
	if a.planner == nil {
		return coreerrors.ErrTaskPlannerNotInitialized
	}
	if a.dispatcher == nil {
		return coreerrors.ErrDispatchNotInitialized
	}
	if a.aggregator == nil {
		return coreerrors.ErrResultAggNotInitialized
	}

	// Initialize lifecycle channels
	a.stopCh = make(chan struct{})

	// Initialize heartbeat monitor if provided
	if a.heartbeatMon != nil {
		// Start heartbeat monitoring for this agent
		// The heartbeat monitor will track agent health and availability
		a.heartbeatMon.RecordHeartbeat(a.id)

		// In a production environment, you would start a background goroutine
		// to periodically send heartbeats and monitor agent health
		slog.Info("Heartbeat monitor initialized", "agent_id", a.id)
	}

	// Initialize message queue if provided
	if a.messageQueue != nil {
		// Message queue is ready to use for inter-agent communication
		// The queue enables the leader agent to:
		// - Send messages to sub-agents
		// - Receive messages from sub-agents
		// - Coordinate distributed task execution

		slog.Info("Message queue initialized", "agent_id", a.id)
	}

	slog.Info("Leader agent started successfully", "agent_id", a.id)
	a.setStatus(models.AgentStatusReady)
	return nil
}

// Stop stops the leader agent and cleans up resources.
func (a *leaderAgent) Stop(ctx context.Context) error {
	if a.Status() == models.AgentStatusOffline {
		return coreerrors.ErrAgentNotRunning
	}

	a.cleanupOnce.Do(func() {
		// Signal all goroutines to stop
		close(a.stopCh)

		// Wait for distillation goroutines to complete
		a.distillWg.Wait()

		// Note: MessageQueue does not have a Drain method. Messages in the queue
		// will be naturally drained by consumers or discarded when the queue is closed.
		// If needed, consumers should handle remaining messages before stopping.

		// Cleanup heartbeat monitor if provided
		if a.heartbeatMon != nil {
			// Remove agent from heartbeat monitoring
			a.heartbeatMon.RemoveAgent(a.id)
		}

		slog.Info("Leader agent stopped successfully", "agent_id", a.id)
	})

	a.setStatus(models.AgentStatusStopping)
	a.setStatus(models.AgentStatusOffline)
	return nil
}

// Process handles user input and orchestrates the recommendation workflow with automatic memory management.
func (a *leaderAgent) Process(ctx context.Context, input any) (any, error) {
	if a.Status() != models.AgentStatusReady && a.Status() != models.AgentStatusOffline {
		return nil, coreerrors.ErrAgentNotReady
	}

	if a.Status() == models.AgentStatusOffline {
		if err := a.Start(ctx); err != nil {
			return nil, err
		}
	}

	stepCount := 0
	maxSteps := a.config.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	a.setStatus(models.AgentStatusBusy)
	defer a.setStatus(models.AgentStatusReady)

	var strInput string
	switch v := input.(type) {
	case string:
		strInput = v
	case []byte:
		strInput = string(v)
	case fmt.Stringer:
		strInput = v.String()
	default:
		return nil, errors.Wrapf(coreerrors.ErrInvalidInput, "expected string, []byte, or fmt.Stringer, got %T", input)
	}

	var sessionID string
	if a.memoryManager != nil {
		a.mu.Lock()
		sessionID = a.sessionID
		if sessionID == "" {
			newSessionID, err := a.memoryManager.CreateSession(ctx, "default_user")
			if err != nil {
				slog.Warn("Failed to create session", "error", err)
			} else {
				sessionID = newSessionID
				a.sessionID = sessionID
			}
		}
		a.mu.Unlock()

		// Add user input to memory
		if err := a.memoryManager.AddMessage(ctx, sessionID, "user", strInput); err != nil {
			slog.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
		}

		// Build input with context
		inputWithContext, err := a.memoryManager.BuildContext(ctx, strInput, sessionID)
		if err != nil {
			slog.Warn("memory operation failed, proceeding without", "operation", "BuildContext", "error", err)
		} else {
			strInput = inputWithContext
		}

		// Search similar tasks for context
		similarTasks, err := a.memoryManager.SearchSimilarTasks(ctx, strInput, 3)
		if err != nil {
			slog.Warn("memory operation failed, proceeding without", "operation", "SearchSimilarTasks", "error", err)
		} else if len(similarTasks) > 0 {
			slog.Debug("Found similar tasks", "count", len(similarTasks))
			contextStr := "\n\nSimilar previous tasks:\n"
			for _, task := range similarTasks {
				if taskInput, ok := task.Payload["input"].(string); ok {
					contextStr += fmt.Sprintf("- %s\n", taskInput)
				}
			}
			strInput += contextStr
		}
	}

	// Memory: Create task
	var taskID string
	if a.memoryManager != nil {
		var err error
		taskID, err = a.memoryManager.CreateTask(ctx, sessionID, "default_user", strInput)
		if err != nil {
			slog.Warn("Failed to create task - proceeding without task tracking",
				"error", err,
				"session_id", sessionID,
				"impact", "task will not be tracked for distillation")
		}
	}

	// Step 1: Parse profile
	stepCount++
	if stepCount > maxSteps {
		return nil, coreerrors.ErrMaxStepsExceeded
	}

	profile, err := a.parser.Parse(ctx, strInput)
	if err != nil {
		return nil, err
	}

	// Step 2: Plan tasks
	stepCount++
	if stepCount > maxSteps {
		return nil, coreerrors.ErrMaxStepsExceeded
	}

	tasks, err := a.planner.Plan(ctx, profile, strInput)
	if err != nil {
		return nil, err
	}
	slog.Info("Leader tasks created", "module", "leader", "count", len(tasks))

	// Step 3: Dispatch tasks
	stepCount++
	if stepCount > maxSteps {
		return nil, coreerrors.ErrMaxStepsExceeded
	}

	slog.Info("Leader dispatching tasks", "module", "leader")
	results, err := a.dispatcher.Dispatch(ctx, tasks)
	if err != nil {
		return nil, err
	}
	slog.Info("Leader dispatch completed", "module", "leader", "result_count", len(results))
	for i, r := range results {
		slog.Info("Leader task result", "module", "leader", "index", i, "success", r.Success, "items", len(r.Items), "error", r.Error)
	}

	// Step 4: Aggregate results
	stepCount++
	if stepCount > maxSteps {
		return nil, coreerrors.ErrMaxStepsExceeded
	}

	result, err := a.aggregator.Aggregate(ctx, results, tasks)
	if err != nil {
		return nil, err
	}

	// Memory: Update task output and add result to memory
	resultStr := fmt.Sprintf("Generated %d items", len(result.Items))
	if a.memoryManager != nil && taskID != "" {
		if err := a.memoryManager.UpdateTaskOutput(ctx, taskID, resultStr); err != nil {
			slog.Warn("memory operation failed, proceeding without", "operation", "UpdateTaskOutput", "error", err)
		}
	}
	// Add assistant response to memory
	if a.memoryManager != nil {
		if err := a.memoryManager.AddMessage(ctx, a.sessionID, "assistant", resultStr); err != nil {
			slog.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
		}

		// Run distillation in background goroutine with proper lifecycle management.
		// Context is created inside the goroutine to avoid race: defer cancel() in the
		// parent function would cancel the context before the goroutine starts.
		a.distillWg.Add(1)
		go func() {
			defer a.distillWg.Done()

			// Check if agent is stopped before starting.
			select {
			case <-a.stopCh:
				slog.Debug("Distillation skipped: agent stopping", "task_id", taskID)
				return
			default:
			}

			// Create a detached context with its own timeout so distillation
			// continues even if the parent request is cancelled.
			// We use the request context as the base but give it its own timeout.
			distillCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			g, gCtx := errgroup.WithContext(distillCtx)
			g.Go(func() error {
				distilled, err := a.memoryManager.DistillTask(gCtx, taskID)
				if err != nil {
					slog.Warn("Failed to distill task", "error", err, "task_id", taskID)
					return err
				}
				return a.memoryManager.StoreDistilledTask(gCtx, taskID, distilled)
			})

			if err := g.Wait(); err != nil {
				slog.Error("Error in async distillation", "error", err, "task_id", taskID)
			}
		}()
	}

	return result, nil
}

// SendMessage sends a message to another agent.
func (a *leaderAgent) SendMessage(ctx context.Context, msg *ahp.AHPMessage) error {
	if a.messageQueue == nil {
		return coreerrors.ErrQueueNotInitialized
	}
	return a.messageQueue.Enqueue(ctx, msg)
}

// ReceiveMessage receives a message from the message queue.
func (a *leaderAgent) ReceiveMessage(ctx context.Context) (*ahp.AHPMessage, error) {
	if a.messageQueue == nil {
		return nil, coreerrors.ErrQueueNotInitialized
	}
	return a.messageQueue.Dequeue(ctx)
}

// Heartbeat sends a heartbeat signal.
func (a *leaderAgent) Heartbeat(ctx context.Context) error {
	if a.heartbeatMon == nil {
		return nil
	}
	a.heartbeatMon.RecordHeartbeat(a.id)
	return nil
}

// IsAlive checks if the agent is alive.
func (a *leaderAgent) IsAlive() bool {
	return a.Status() == models.AgentStatusReady || a.Status() == models.AgentStatusBusy
}
