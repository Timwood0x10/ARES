package leader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/callbacks"
	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/protocol/ahp"

	"golang.org/x/sync/errgroup"
)

// Agent represents the Leader Agent interface.
type Agent interface {
	base.Agent
}

// Compile-time check: leaderAgent must satisfy base.StatefulAgent.
var _ base.StatefulAgent = (*leaderAgent)(nil)

// ProfileParser parses user profile from input.
type ProfileParser interface {
	Parse(ctx context.Context, input string) (*models.UserProfile, error)
}

// TaskPlanner plans tasks based on user profile and input text.
type TaskPlanner interface {
	Plan(ctx context.Context, profile *models.UserProfile, inputText string) ([]*models.Task, error)
	// Replan creates new tasks based on previous result and feedback.
	// This is used for iterative refinement when the initial result is insufficient.
	Replan(ctx context.Context, profile *models.UserProfile, inputText string, previousResult *models.RecommendResult, feedback string) ([]*models.Task, error)
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

// LeaderOption configures a leaderAgent instance.
type LeaderOption func(*leaderAgent)

// WithCheckpoint sets the checkpoint repository for failover recovery.
func WithCheckpoint(cp *CheckpointRepository) LeaderOption {
	return func(a *leaderAgent) {
		a.checkpoint = cp
	}
}

// WithEventStore sets the event store for event sourcing.
func WithEventStore(store events.EventStore) LeaderOption {
	return func(a *leaderAgent) {
		a.eventStore = store
		// Wire event store to profile parser for LLM call tracking.
		if pp, ok := a.parser.(*profileParser); ok {
			pp.WithEventStore(store)
		}
	}
}

// WithCallbacks sets the callback emitter for lifecycle event emission.
func WithCallbacks(emitter callbacks.Emitter) LeaderOption {
	return func(a *leaderAgent) {
		a.callbacks = emitter
	}
}

// WithFeedbackService sets the experience feedback service for bandit reinforcement.
func WithFeedbackService(svc *experience.FeedbackService) LeaderOption {
	return func(a *leaderAgent) {
		a.feedbackSvc = svc
	}
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
	feedbackSvc   *experience.FeedbackService
	sessionID     string
	checkpoint    *CheckpointRepository
	eventStore    events.EventStore
	callbacks     callbacks.Emitter // Optional: emits lifecycle callback events.

	// Snapshot/restore state fields for resurrection support.
	lastTaskID          string
	lastCompletedTaskID string // ID of most recently completed task (differs from lastTaskID which is "created")
	conversationSummary string
	lastInteractionTime time.Time

	// Lifecycle management
	stopCh       chan struct{}   // Channel to signal shutdown
	distillMu    sync.Mutex      // Protects stopCh-close vs distillWg.Add ordering
	distillWg    sync.WaitGroup  // WaitGroup for distillation goroutines
	distillEg    *errgroup.Group // Errgroup for distillation goroutines
	streamEg     *errgroup.Group // Errgroup for streaming pipeline goroutines
	processingMu sync.Mutex      // Ensures mutual exclusion of Process/ProcessStream
	cleanupOnce  sync.Once       // Ensure cleanup runs only once
}

// LeaderAgentConfig holds configuration for LeaderAgent.
type LeaderAgentConfig struct {
	base.Config
	MaxParallelTasks int
	MaxSteps         int
	EnableCache      bool
	UserID           string // UserID for session and task creation. Defaults to "default_user" if empty.
	Loop             LoopConfig
}

// LoopConfig holds configuration for agent loop behavior.
type LoopConfig struct {
	// MaxIterations is the maximum number of loop iterations (default: 3).
	MaxIterations int
	// QualityThreshold is the minimum quality score to accept result (default: 0.7).
	QualityThreshold float64
	// EnableReflection enables reflection and re-planning (default: false).
	EnableReflection bool
	// MaxTotalLLMCalls is the maximum total LLM calls across all iterations (default: 50).
	MaxTotalLLMCalls int
	// MaxLoopDuration is the maximum duration for the entire loop (default: 10 minutes).
	MaxLoopDuration time.Duration
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
	opts ...LeaderOption,
) Agent {
	if cfg == nil {
		cfg = DefaultLeaderAgentConfig()
	}
	cfg.ID = id
	cfg.Type = models.AgentTypeLeader

	a := &leaderAgent{
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

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// DefaultLeaderAgentConfig returns default configuration.
func DefaultLeaderAgentConfig() *LeaderAgentConfig {
	return &LeaderAgentConfig{
		Config:           *base.DefaultConfig(models.AgentTypeLeader),
		MaxParallelTasks: DefaultMaxParallelTasks,
		MaxSteps:         DefaultMaxSteps,
		EnableCache:      true,
		Loop: LoopConfig{
			MaxIterations:    3,
			QualityThreshold: 0.7,
			EnableReflection: false,
			MaxTotalLLMCalls: 50,
			MaxLoopDuration:  10 * time.Minute,
		},
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
func (a *leaderAgent) Start(ctx context.Context) (startErr error) {
	a.mu.Lock()
	if a.status != models.AgentStatusOffline {
		a.mu.Unlock()
		return coreerrors.ErrAgentAlreadyStarted
	}
	a.status = models.AgentStatusStarting
	a.mu.Unlock()

	// Reset status to Offline if startup fails for any reason.
	defer func() {
		if startErr != nil {
			a.setStatus(models.AgentStatusOffline)
		}
	}()

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

	// Initialize lifecycle channels and errgroups.
	a.stopCh = make(chan struct{})
	a.distillEg = &errgroup.Group{}
	a.streamEg = &errgroup.Group{}

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

	// Emit agent started event.
	a.emitEvent(ctx, events.EventAgentStarted, map[string]any{
		"agent_id": a.id,
		"type":     string(a.agentType),
	})

	slog.Info("Leader agent started successfully", "agent_id", a.id)
	a.setStatus(models.AgentStatusReady)
	return nil
}

// Stop stops the leader agent and cleans up resources.
func (a *leaderAgent) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.status == models.AgentStatusOffline {
		a.mu.Unlock()
		return coreerrors.ErrAgentNotRunning
	}
	a.status = models.AgentStatusStopping
	a.mu.Unlock()

	a.cleanupOnce.Do(func() {
		// Signal all goroutines to stop.
		a.distillMu.Lock()
		close(a.stopCh)
		a.distillMu.Unlock()

		// Wait for background goroutines to complete.
		a.distillWg.Wait()
		if a.distillEg != nil {
			if err := a.distillEg.Wait(); err != nil {
				slog.Warn("Errors from distillation goroutines during shutdown",
					"error", err)
			}
		}
		if a.streamEg != nil {
			if err := a.streamEg.Wait(); err != nil {
				slog.Warn("Errors from streaming goroutines during shutdown",
					"error", err)
			}
		}

		// Cleanup heartbeat monitor if provided.
		if a.heartbeatMon != nil {
			a.heartbeatMon.RemoveAgent(a.id)
		}

		slog.Info("Leader agent stopped successfully", "agent_id", a.id)
	})

	a.emitEvent(ctx, events.EventAgentStopped, map[string]any{
		"agent_id": a.id,
	})

	a.setStatus(models.AgentStatusOffline)
	return nil
}

// getUserID returns the configured user ID, defaulting to "default_user" if empty.
func (a *leaderAgent) getUserID() string {
	if a.config != nil && a.config.UserID != "" {
		return a.config.UserID
	}
	return "default_user"
}

// parseInput coerces the input to a string. Accepts string, []byte, or fmt.Stringer.
func parseInput(input any) (string, error) {
	switch v := input.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case fmt.Stringer:
		return v.String(), nil
	default:
		return "", errors.Wrapf(coreerrors.ErrInvalidInput, "expected string, []byte, or fmt.Stringer, got %T", input)
	}
}

// initMemoryContext initializes session, records user message, builds context with
// similar tasks, and creates a task record. Returns the enriched input, sessionID, and taskID.
func (a *leaderAgent) initMemoryContext(ctx context.Context, strInput string) (enrichedInput string, sessionID string, taskID string) {
	if a.memoryManager == nil {
		return strInput, "", ""
	}

	// Ensure session exists, attempting checkpoint recovery first.
	// Read sessionID under read lock to avoid holding write lock during DB calls.
	a.mu.RLock()
	sessionID = a.sessionID
	checkpoint := a.checkpoint
	leaderID := a.id
	a.mu.RUnlock()

	if sessionID == "" {
		recovered := false
		if checkpoint != nil {
			cp, err := checkpoint.GetLatest(ctx, leaderID)
			if err != nil {
				slog.Warn("Checkpoint recovery failed, creating new session", "error", err)
			} else if cp != nil && cp.SessionID != "" {
				sessionID = cp.SessionID
				recovered = true
				slog.Info("Session recovered from checkpoint", "session_id", sessionID, "leader_id", leaderID)
			}
		}
		if !recovered {
			newSessionID, err := a.memoryManager.CreateSession(ctx, a.getUserID())
			if err != nil {
				slog.Warn("Failed to create session", "error", err)
			} else {
				sessionID = newSessionID
			}
		}
		// Take write lock only to persist the sessionID.
		if sessionID != "" {
			a.mu.Lock()
			a.sessionID = sessionID
			a.mu.Unlock()

			if checkpoint != nil {
				if err := checkpoint.Save(ctx, &LeaderCheckpoint{
					LeaderID:  leaderID,
					SessionID: sessionID,
					Status:    "active",
				}); err != nil {
					slog.Warn("Failed to save checkpoint", "error", err)
				}
			}

			a.emitEvent(ctx, events.EventSessionCreated, map[string]any{
				"session_id": sessionID,
				"user_id":    a.getUserID(),
			})
		}
	}

	// Record user message.
	if err := a.memoryManager.AddMessage(ctx, sessionID, "user", strInput); err != nil {
		slog.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
	}

	if sessionID != "" {
		a.emitEvent(ctx, events.EventMessageAdded, map[string]any{
			"session_id": sessionID,
			"role":       "user",
		})
	}

	// Build input with conversation context.
	enrichedInput = strInput
	if inputWithContext, err := a.memoryManager.BuildContext(ctx, strInput, sessionID); err != nil {
		slog.Warn("memory operation failed, proceeding without", "operation", "BuildContext", "error", err)
	} else {
		enrichedInput = inputWithContext
	}

	// Search similar tasks for additional context.
	similarTasks, err := a.memoryManager.SearchSimilarTasks(ctx, enrichedInput, 3)
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
		enrichedInput += contextStr
	}

	// Create task record for tracking and distillation.
	if tID, err := a.memoryManager.CreateTask(ctx, sessionID, a.getUserID(), enrichedInput); err != nil {
		slog.Warn("Failed to create task - proceeding without task tracking",
			"error", err, "session_id", sessionID,
			"impact", "task will not be tracked for distillation")
	} else {
		taskID = tID
		// Safe to call updateSnapshotState here: a.mu is NOT held at this point.
		// The RLock section above (line ~376) was already released, and the write lock
		// for sessionID persistence was also released before reaching this code.
		a.updateSnapshotState(taskID)

		a.emitEvent(ctx, events.EventTaskCreated, map[string]any{
			"task_id":    taskID,
			"session_id": sessionID,
		})
	}

	return enrichedInput, sessionID, taskID
}

// emitEvent appends a single event using the canonical events.Emit.
func (a *leaderAgent) emitEvent(ctx context.Context, eventType events.EventType, payload map[string]any) {
	if events.Emit(ctx, a.eventStore, a.id, eventType, payload) {
		slog.Debug("event emitted", "agent_id", a.id, "type", eventType)
	}
}

// emitCallback emits a lifecycle callback event if the emitter is set.
func (a *leaderAgent) emitCallback(ctx *callbacks.Context) {
	if a.callbacks == nil {
		return
	}
	a.callbacks.Emit(ctx)
}

// updateSnapshotState updates the snapshot-tracking fields after state changes.
// This ensures Snapshot() returns up-to-date data for resurrection.
//
// IMPORTANT: This method acquires a.mu.Lock internally. Callers MUST NOT hold
// a.mu when invoking this method, or it will deadlock. All current call sites
// have been verified to call this without holding a.mu.
//
// Args:
//
//	taskID - the task ID that was just created.
func (a *leaderAgent) updateSnapshotState(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastTaskID = taskID
	a.lastInteractionTime = time.Now()
}

// finalizeMemory updates task output, records assistant message, and triggers
// background distillation. Must be called after aggregation succeeds.
func (a *leaderAgent) finalizeMemory(ctx context.Context, sessionID, taskID string, result *models.RecommendResult) {
	if a.memoryManager == nil || result == nil {
		return
	}

	resultStr := fmt.Sprintf("Generated %d items", len(result.Items))

	// Update task output.
	if taskID != "" {
		if err := a.memoryManager.UpdateTaskOutput(ctx, taskID, resultStr); err != nil {
			slog.Warn("memory operation failed, proceeding without", "operation", "UpdateTaskOutput", "error", err)
		}
	}

	// Record assistant response.
	if err := a.memoryManager.AddMessage(ctx, sessionID, "assistant", resultStr); err != nil {
		slog.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
	}

	if sessionID != "" {
		a.emitEvent(ctx, events.EventMessageAdded, map[string]any{
			"session_id": sessionID,
			"role":       "assistant",
		})
	}

	// Emit task completed event for event sourcing.
	if taskID != "" {
		a.emitEvent(ctx, events.EventTaskCompleted, map[string]any{
			"task_id": taskID,
			"status":  "completed",
		})
	}

	// Run distillation in background goroutine with proper lifecycle management.
	// Context is created inside the goroutine to avoid race: defer cancel() in the
	// parent function would cancel the context before the goroutine starts.
	if taskID == "" {
		return
	}

	// Check if agent is stopped BEFORE adding to WaitGroup.
	// If agent is stopped, distillWg.Wait() may have already returned;
	// calling Add(1) after Wait() returns causes a panic.
	// Lock distillMu to make the stopCh check and Add(1) atomic.
	a.distillMu.Lock()
	select {
	case <-a.stopCh:
		a.distillMu.Unlock()
		slog.Debug("Distillation skipped: agent stopping", "task_id", taskID)
		return
	default:
	}
	a.distillWg.Add(1)
	a.distillMu.Unlock()
	a.distillEg.Go(func() error {
		defer a.distillWg.Done()

		// Detached context with own timeout — distillation continues
		// even if the parent request is cancelled.
		distillCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
			return nil
		}

		// Emit memory distilled event for event sourcing.
		a.mu.RLock()
		es := a.eventStore
		lid := a.id
		a.mu.RUnlock()
		if es != nil {
			if emitErr := es.Append(distillCtx, lid, []*events.Event{
				{
					Type: events.EventMemoryDistilled,
					Payload: map[string]any{
						"task_id":    taskID,
						"session_id": sessionID,
					},
				},
			}, 0); emitErr != nil {
				slog.Warn("Failed to emit memory distilled event", "error", emitErr)
			}
		}
		return nil
	})
}

// recordExperienceFeedback records bandit feedback for experiences used in tasks.
// For each task with a non-empty UsedExperienceID:
// - If task succeeded: increment usage count (positive reinforcement).
// - If task failed: decrement rank score (negative reinforcement).
// No-op if feedbackSvc is nil or no tasks used experiences.
//
// Results are matched to tasks by TaskID rather than array index to handle
// cases where the dispatcher may return results in a different order than tasks.
func (a *leaderAgent) recordExperienceFeedback(ctx context.Context, tasks []*models.Task, results []*models.TaskResult) {
	if a.feedbackSvc == nil {
		return
	}

	// Build a TaskID-to-result index for O(1) lookup instead of fragile index matching.
	resultByTaskID := make(map[string]*models.TaskResult, len(results))
	for _, r := range results {
		if r != nil {
			resultByTaskID[r.TaskID] = r
		}
	}

	for _, task := range tasks {
		if task.UsedExperienceID == "" {
			continue
		}

		var success bool
		if result, ok := resultByTaskID[task.TaskID]; ok && result != nil {
			success = result.Success
		}

		if err := a.feedbackSvc.RecordFeedback(ctx, task.UsedExperienceID, success); err != nil {
			slog.Warn("Failed to record experience feedback",
				"task_id", task.TaskID,
				"experience_id", task.UsedExperienceID,
				"success", success,
				"error", err,
			)
		}
	}
}

// Process handles user input and orchestrates the recommendation workflow with automatic memory management.
func (a *leaderAgent) Process(ctx context.Context, input any) (any, error) {
	// Ensure mutual exclusion: only one Process/ProcessStream at a time.
	a.processingMu.Lock()
	defer a.processingMu.Unlock()

	// Atomically check status and transition to Busy.
	a.mu.Lock()
	if a.status == models.AgentStatusOffline {
		a.mu.Unlock()
		if err := a.Start(ctx); err != nil {
			return nil, err
		}
		a.mu.Lock()
	}
	if a.status != models.AgentStatusReady {
		a.mu.Unlock()
		return nil, coreerrors.ErrAgentNotReady
	}
	a.status = models.AgentStatusBusy
	a.mu.Unlock()

	startTime := time.Now()

	// Emit agent start event.
	a.emitCallback(&callbacks.Context{
		Event:   callbacks.EventAgentStart,
		AgentID: a.id,
	})

	stepCount := 0
	maxSteps := a.config.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	defer func() {
		a.setStatus(models.AgentStatusReady)
		duration := time.Since(startTime)
		// Emit agent end event on exit (success or error will be handled below).
		a.emitCallback(&callbacks.Context{
			Event:    callbacks.EventAgentEnd,
			AgentID:  a.id,
			Duration: duration,
		})
	}()

	strInput, err := parseInput(input)
	if err != nil {
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	// Initialize memory context (session, messages, similar tasks, task record).
	strInput, sessionID, taskID := a.initMemoryContext(ctx, strInput)

	// Step 1: Parse profile
	stepCount++
	if stepCount > maxSteps {
		err := coreerrors.ErrMaxStepsExceeded
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	select {
	case <-a.stopCh:
		err := coreerrors.ErrAgentNotRunning
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	default:
	}

	a.emitEvent(ctx, events.EventTaskCreated, map[string]any{
		"step": "parse",
	})

	profile, err := a.parser.Parse(ctx, strInput)
	if err != nil {
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	// Step 2: Plan tasks
	stepCount++
	if stepCount > maxSteps {
		err := coreerrors.ErrMaxStepsExceeded
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	select {
	case <-a.stopCh:
		err := coreerrors.ErrAgentNotRunning
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	default:
	}

	a.emitEvent(ctx, events.EventTaskDispatched, map[string]any{
		"step": "plan",
	})

	tasks, err := a.planner.Plan(ctx, profile, strInput)
	if err != nil {
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}
	slog.Info("Leader tasks created", "module", "leader", "count", len(tasks))

	// Step 3: Dispatch tasks
	stepCount++
	if stepCount > maxSteps {
		err := coreerrors.ErrMaxStepsExceeded
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	select {
	case <-a.stopCh:
		err := coreerrors.ErrAgentNotRunning
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	default:
	}

	a.emitEvent(ctx, events.EventTaskDispatched, map[string]any{
		"step": "dispatch",
	})

	slog.Info("Leader dispatching tasks", "module", "leader")
	results, err := a.dispatcher.Dispatch(ctx, tasks)
	if err != nil {
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}
	slog.Info("Leader dispatch completed", "module", "leader", "result_count", len(results))
	for i, r := range results {
		slog.Info("Leader task result", "module", "leader", "index", i, "success", r.Success, "items", len(r.Items), "error", r.Error)
	}

	// Step 4: Aggregate results
	stepCount++
	if stepCount > maxSteps {
		err := coreerrors.ErrMaxStepsExceeded
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	select {
	case <-a.stopCh:
		err := coreerrors.ErrAgentNotRunning
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	default:
	}

	result, err := a.aggregator.Aggregate(ctx, results, tasks)
	if err != nil {
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	// Finalize memory (update task, record assistant message, distill).
	a.finalizeMemory(ctx, sessionID, taskID, result)

	// Record bandit feedback for experiences used in tasks.
	// This closes the feedback loop: successful tasks increment usage count,
	// failed tasks decrement rank score.
	a.recordExperienceFeedback(ctx, tasks, results)

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

// RestoreState restores the leader agent's state from persisted data.
// Implements base.StatefulAgent for resurrection support.
//
// Restorable fields:
//   - session_id: active session identifier
//   - last_task_id: most recently created task ID
//   - last_completed_task_id: most recently completed task ID
//   - agent_status: status string (ready/busy/offline)
//   - conversation_summary: brief summary of recent conversation
//   - last_interaction_time: RFC3339 timestamp of last user interaction
//
// Args:
//
//	state - map of persisted state fields. Nil or empty is a safe no-op.
//
// Returns:
//
//	err - always nil for RestoreState; invalid fields are silently skipped.
func (a *leaderAgent) RestoreState(state map[string]any) error {
	if state == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if sid, ok := state["session_id"].(string); ok && sid != "" {
		a.sessionID = sid
	}
	if tid, ok := state["last_task_id"].(string); ok && tid != "" {
		a.lastTaskID = tid
	}
	if ctid, ok := state["last_completed_task_id"].(string); ok && ctid != "" {
		a.lastCompletedTaskID = ctid
	}
	if statusStr, ok := state["agent_status"].(string); ok {
		if parsed, err := models.ParseAgentStatus(statusStr); err == nil {
			a.status = parsed
		}
	}
	if summary, ok := state["conversation_summary"].(string); ok {
		a.conversationSummary = summary
	}
	if ts, ok := state["last_interaction_time"].(string); ok {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			a.lastInteractionTime = t
		}
	}

	slog.Info("state restored from snapshot",
		"agent_id", a.id,
		"session_id", a.sessionID,
		"status", string(a.status),
	)
	return nil
}

// ReplayEvents replays a sequence of events to reconstruct state.
// Implements base.StatefulAgent for resurrection support.
//
// Supported event types:
//   - EventSessionCreated: restores session_id
//   - EventMessageAdded: updates last_message_role and message count
//   - EventTaskCreated: restores last_task_id
//   - EventTaskCompleted: restores last_completed_task_id
//   - EventAgentStarted/Stopped: updates agent status
//
// Args:
//
//	evts - ordered sequence of events to replay. Nil or empty is a safe no-op.
//
// Returns:
//
//	err - always nil for ReplayEvents; invalid events are silently skipped.
func (a *leaderAgent) ReplayEvents(evts []*events.Event) error {
	if len(evts) == 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var msgCount int

	for _, ev := range evts {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case events.EventSessionCreated:
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" {
				a.sessionID = sid
			}

		case events.EventMessageAdded:
			msgCount++
			if role, ok := ev.Payload["role"].(string); ok {
				a.conversationSummary = fmt.Sprintf("last_role:%s,msg_count:%d", role, msgCount)
			}

		case events.EventTaskCreated:
			if tid, ok := ev.Payload["task_id"].(string); ok && tid != "" {
				a.lastTaskID = tid
			}

		case events.EventTaskCompleted:
			// Track the most recently completed task (separate from lastTaskID which tracks "created").
			if tid, ok := ev.Payload["task_id"].(string); ok && tid != "" {
				a.lastCompletedTaskID = tid
			}

		case events.EventAgentStarted:
			a.status = models.AgentStatusReady

		case events.EventAgentStopped:
			a.status = models.AgentStatusOffline
		}
	}

	slog.Info("events replayed for state reconstruction",
		"agent_id", a.id,
		"event_count", len(evts),
		"session_id", a.sessionID,
	)
	return nil
}

// Snapshot returns a serializable snapshot of the leader agent's current state.
// Implements base.StatefulAgent for resurrection support.
//
// The snapshot includes:
//   - session_id: active session identifier
//   - agent_id: unique agent identifier
//   - status: current agent status string
//   - last_task_id: most recently created task ID (if any)
//   - last_completed_task_id: most recently completed task ID (if any)
//   - conversation_summary: brief summary of recent conversation context
//   - last_interaction_time: RFC3339 timestamp of last state change
//   - snapshot_version: schema version for forward compatibility
//
// Returns:
//
//	snapshot - map of serializable state fields. Never nil.
//	err - always nil for Snapshot; capture failures are non-fatal.
func (a *leaderAgent) Snapshot() (map[string]any, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	snap := map[string]any{
		"session_id":             a.sessionID,
		"agent_id":               a.id,
		"status":                 string(a.status),
		"last_task_id":           a.lastTaskID,
		"last_completed_task_id": a.lastCompletedTaskID,
		"conversation_summary":   a.conversationSummary,
		"snapshot_version":       1,
	}

	if !a.lastInteractionTime.IsZero() {
		snap["last_interaction_time"] = a.lastInteractionTime.Format(time.RFC3339)
	}

	return snap, nil
}

// ProcessStream handles user input and returns a stream of events.
// It follows the same workflow as Process but emits events at each phase.
func (a *leaderAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	// Ensure mutual exclusion: only one Process/ProcessStream at a time.
	a.processingMu.Lock()
	defer a.processingMu.Unlock()

	// Atomically check status and transition to Busy.
	a.mu.Lock()
	if a.status == models.AgentStatusOffline {
		a.mu.Unlock()
		if err := a.Start(ctx); err != nil {
			return nil, err
		}
		a.mu.Lock()
	}
	if a.status != models.AgentStatusReady {
		a.mu.Unlock()
		return nil, coreerrors.ErrAgentNotReady
	}
	a.status = models.AgentStatusBusy
	a.mu.Unlock()

	startTime := time.Now()

	strInput, err := parseInput(input)
	if err != nil {
		a.setStatus(models.AgentStatusReady)
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		return nil, err
	}

	// Initialize memory context (session, messages, similar tasks, task record).
	strInput, sessionID, taskID := a.initMemoryContext(ctx, strInput)

	ch := make(chan base.AgentEvent, 64)

	a.streamEg.Go(func() error {
		// Emit start event inside the goroutine so it's always paired with end.
		a.emitCallback(&callbacks.Context{
			Event:   callbacks.EventAgentStart,
			AgentID: a.id,
		})

		defer close(ch)
		defer func() {
			a.setStatus(models.AgentStatusReady)
			duration := time.Since(startTime)
			a.emitCallback(&callbacks.Context{
				Event:    callbacks.EventAgentEnd,
				AgentID:  a.id,
				Duration: duration,
			})
		}()

		// Send planning event.
		select {
		case ch <- base.AgentEvent{Type: base.EventPlanning, Source: a.id, Data: strInput}:
		case <-ctx.Done():
			return nil
		case <-a.stopCh:
			return nil
		}

		// Parse profile.
		a.emitEvent(ctx, events.EventTaskCreated, map[string]any{
			"step": "parse",
		})

		profile, err := a.parser.Parse(ctx, strInput)
		if err != nil {
			a.emitCallback(&callbacks.Context{
				Event:   callbacks.EventAgentError,
				AgentID: a.id,
				Error:   err,
			})
			select {
			case ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Err: err}:
			case <-ctx.Done():
			case <-a.stopCh:
			}
			return nil
		}

		// Plan tasks.
		a.emitEvent(ctx, events.EventTaskDispatched, map[string]any{
			"step": "plan",
		})

		tasks, err := a.planner.Plan(ctx, profile, strInput)
		if err != nil {
			a.emitCallback(&callbacks.Context{
				Event:   callbacks.EventAgentError,
				AgentID: a.id,
				Error:   err,
			})
			select {
			case ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Err: err}:
			case <-ctx.Done():
			case <-a.stopCh:
			}
			return nil
		}
		slog.Info("Leader tasks created", "module", "leader", "count", len(tasks))

		for _, task := range tasks {
			select {
			case ch <- base.AgentEvent{Type: base.EventTaskStart, Source: a.id, Data: task}:
			case <-ctx.Done():
				return nil
			case <-a.stopCh:
				return nil
			}
		}

		a.emitEvent(ctx, events.EventTaskDispatched, map[string]any{
			"step": "dispatch",
		})

		results, err := a.dispatcher.Dispatch(ctx, tasks)
		if err != nil {
			a.emitCallback(&callbacks.Context{
				Event:   callbacks.EventAgentError,
				AgentID: a.id,
				Error:   err,
			})
			for _, task := range tasks {
				select {
				case ch <- base.AgentEvent{Type: base.EventTaskComplete, Source: a.id, Data: &models.TaskResult{TaskID: task.TaskID, Success: false, Error: err.Error()}}:
				case <-ctx.Done():
					return nil
				case <-a.stopCh:
					return nil
				}
			}
			return nil
		}

		var allResults []*models.TaskResult
		for _, result := range results {
			allResults = append(allResults, result)
			select {
			case ch <- base.AgentEvent{Type: base.EventTaskComplete, Source: a.id, Data: result}:
			case <-ctx.Done():
				return nil
			case <-a.stopCh:
				return nil
			}
		}

		// Aggregate results.
		select {
		case ch <- base.AgentEvent{Type: base.EventAggregating, Source: a.id}:
		case <-ctx.Done():
			return nil
		case <-a.stopCh:
			return nil
		}

		result, err := a.aggregator.Aggregate(ctx, allResults, tasks)
		if err != nil {
			a.emitCallback(&callbacks.Context{
				Event:   callbacks.EventAgentError,
				AgentID: a.id,
				Error:   err,
			})
			select {
			case ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Err: err}:
			case <-ctx.Done():
			case <-a.stopCh:
			}
			return nil
		}

		// Finalize memory (update task, record assistant message, distill).
		a.finalizeMemory(ctx, sessionID, taskID, result)

		// Record bandit feedback for experiences used in tasks.
		a.recordExperienceFeedback(ctx, tasks, allResults)

		// Send final result.
		select {
		case ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Data: result}:
		case <-ctx.Done():
		case <-a.stopCh:
		}
		return nil
	})

	return ch, nil
}
