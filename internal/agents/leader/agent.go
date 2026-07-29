package leader

import (
	"context"
	"fmt"
	"time"

	stderrors "errors"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"

	"golang.org/x/sync/errgroup"
)

// err - validation error if required dependencies are nil.
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
) (Agent, error) {
	if id == "" {
		return nil, errors.New("leader agent: id cannot be empty")
	}
	if parser == nil {
		return nil, errors.New("leader agent: parser cannot be nil")
	}
	if planner == nil {
		return nil, errors.New("leader agent: planner cannot be nil")
	}
	if dispatcher == nil {
		return nil, errors.New("leader agent: dispatcher cannot be nil")
	}
	if aggregator == nil {
		return nil, errors.New("leader agent: aggregator cannot be nil")
	}
	if memMgr == nil {
		return nil, errors.New("leader agent: memory manager cannot be nil")
	}
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

	return a, nil
}

// DefaultLeaderAgentConfig returns default configuration.

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

// ensureInitialized lazily initializes lifecycle fields so that Process and
// ProcessStream never panic on nil errgroup even when called without a prior
// Start (e.g. after RestoreState/ReplayEvents set a non-Offline status, P0-5).
// Safe to call multiple times — subsequent calls are no-ops.
func (a *leaderAgent) ensureInitialized() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopCh != nil {
		return
	}
	a.stopCh = make(chan struct{})
	a.distillEg = &errgroup.Group{}
	a.streamEg = &errgroup.Group{}
}

// Start starts the leader agent.
func (a *leaderAgent) Start(ctx context.Context) (startErr error) {
	a.mu.Lock()
	if a.status != models.AgentStatusOffline {
		a.mu.Unlock()
		return errors.ErrAgentAlreadyStarted
	}
	a.status = models.AgentStatusStarting
	// Initialize lifecycle channels and errgroups inside the lock so that a
	// concurrent Stop() observes a non-nil stopCh. Creating stopCh after the
	// lock is released lets Stop close a nil channel and panic.
	// Only initialize if not already set up by ensureInitialized for the
	// current lifecycle. Re-creating them would leak the previous channels
	// and errgroups (LD-3). After a prior Stop, stopCh is closed but non-nil,
	// so detect that and create fresh fields.
	createFields := true
	if a.stopCh != nil {
		select {
		case <-a.stopCh:
			// closed by a previous Stop: need fresh fields
		default:
			createFields = false // open: already initialized by ensureInitialized
		}
	}
	if createFields {
		a.stopCh = make(chan struct{})
		a.distillEg = &errgroup.Group{}
		a.streamEg = &errgroup.Group{}
	}
	a.mu.Unlock()

	// Reset status to Offline if startup fails for any reason.
	defer func() {
		if startErr != nil {
			a.setStatus(models.AgentStatusOffline)
		}
	}()

	// Validate and initialize dependencies
	if a.parser == nil {
		return errors.ErrProfileParserNotInitialized
	}
	if a.planner == nil {
		return errors.ErrTaskPlannerNotInitialized
	}
	if a.dispatcher == nil {
		return errors.ErrDispatchNotInitialized
	}
	if a.aggregator == nil {
		return errors.ErrResultAggNotInitialized
	}

	// Initialize heartbeat monitor if provided
	if a.heartbeatMon != nil {
		// Start heartbeat monitoring for this agent
		// The heartbeat monitor will track agent health and availability
		a.heartbeatMon.RecordHeartbeat(a.id)

		// In a production environment, you would start a background goroutine
		// to periodically send heartbeats and monitor agent health
		log.Info("Heartbeat monitor initialized", "agent_id", a.id)
	}

	// Initialize message queue if provided
	if a.messageQueue != nil {
		// Message queue is ready to use for inter-agent communication
		// The queue enables the leader agent to:
		// - Send messages to sub-agents
		// - Receive messages from sub-agents
		// - Coordinate distributed task execution

		log.Info("Message queue initialized", "agent_id", a.id)
	}

	// Emit agent started event.
	a.emitEvent(ctx, ares_events.EventAgentStarted, map[string]any{
		"agent_id": a.id,
		"type":     string(a.agentType),
	})

	log.Info("Leader agent started successfully", "agent_id", a.id)
	a.setStatus(models.AgentStatusReady)
	return nil
}

// Stop stops the leader agent and cleans up resources.
//
// Args:
//
//	ctx - context for cancellation during shutdown.
//
// Returns:
//
//	err - joined errors from distillation/streaming goroutines, or status error.
func (a *leaderAgent) Stop(ctx context.Context) (retErr error) {
	// Serialize with concurrent Process/ProcessStream to prevent races on
	// distillEg/streamEg lifecycle (P0-6).
	a.processingMu.Lock()
	defer a.processingMu.Unlock()

	a.mu.Lock()
	if a.status == models.AgentStatusOffline {
		a.mu.Unlock()
		return errors.ErrAgentNotRunning
	}
	a.status = models.AgentStatusStopping
	a.mu.Unlock()

	a.cleanupOnce.Do(func() {
		// Signal all goroutines to stop under a.mu, which is the lock used for
		// both creation (Start) and reads (checkAgentRunning, ProcessStream).
		// Previously this used distillMu, creating a data race (P0-6).
		a.mu.Lock()
		close(a.stopCh)
		a.mu.Unlock()

		// Wait for background goroutines to complete and collect their errors.
		a.distillWg.Wait()

		var errs []error
		if a.distillEg != nil {
			if err := a.distillEg.Wait(); err != nil {
				log.Warn("Errors from distillation goroutines during shutdown",
					"error", err)
				errs = append(errs, fmt.Errorf("distillation: %w", err))
			}
		}
		if a.streamEg != nil {
			if err := a.streamEg.Wait(); err != nil {
				log.Warn("Errors from streaming goroutines during shutdown",
					"error", err)
				errs = append(errs, fmt.Errorf("streaming: %w", err))
			}
		}
		if len(errs) > 0 {
			retErr = stderrors.Join(errs...)
		}

		// Cleanup heartbeat monitor if provided.
		if a.heartbeatMon != nil {
			a.heartbeatMon.RemoveAgent(a.id)
		}

		log.Info("Leader agent stopped successfully", "agent_id", a.id)
	})

	a.emitEvent(ctx, ares_events.EventAgentStopped, map[string]any{
		"agent_id": a.id,
	})

	a.setStatus(models.AgentStatusOffline)
	return retErr
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
		return "", errors.Wrapf(errors.ErrInvalidInput, "expected string, []byte, or fmt.Stringer, got %T", input)
	}
}

// initMemoryContext initializes session, records user message, builds context with
// similar tasks, and creates a task record. Returns the enriched input, sessionID, and taskID.

// Process handles user input and orchestrates the recommendation workflow with automatic memory management.
func (a *leaderAgent) fail(err error) (any, error) {
	a.emitCallback(&ares_callbacks.Context{
		Event:   ares_callbacks.EventAgentError,
		AgentID: a.id,
		Error:   err,
	})
	return nil, err
}

func (a *leaderAgent) checkStepLimit(stepCount, maxSteps int) error {
	if stepCount > maxSteps {
		return errors.ErrMaxStepsExceeded
	}
	return nil
}

func (a *leaderAgent) checkAgentRunning() error {
	// Hold a.mu.RLock to safely read the stopCh field, which is written under
	// a.mu in Start/ensureInitialized/Stop. The chan receive itself is safe
	// without a lock (closing is synchronized), but the field read is not (LD-5).
	a.mu.RLock()
	defer a.mu.RUnlock()
	select {
	case <-a.stopCh:
		return errors.ErrAgentNotRunning
	default:
	}
	return nil
}

func (a *leaderAgent) Process(ctx context.Context, input any) (any, error) {
	// Ensure lifecycle fields are initialized (P0-5).
	a.ensureInitialized()

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
		return nil, errors.ErrAgentNotReady
	}
	a.status = models.AgentStatusBusy
	a.mu.Unlock()

	startTime := time.Now()

	// Emit agent start event.
	a.emitCallback(&ares_callbacks.Context{
		Event:   ares_callbacks.EventAgentStart,
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
		a.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventAgentEnd,
			AgentID:  a.id,
			Duration: duration,
		})
	}()

	strInput, err := parseInput(input)
	if err != nil {
		return a.fail(err)
	}

	// Initialize memory context (session, messages, similar tasks, task record).
	strInput, sessionID, taskID := a.initMemoryContext(ctx, strInput)

	// Step 1: Parse profile
	stepCount++
	if err := a.checkStepLimit(stepCount, maxSteps); err != nil {
		return a.fail(err)
	}
	if err := a.checkAgentRunning(); err != nil {
		return a.fail(err)
	}
	a.emitEvent(ctx, ares_events.EventTaskCreated, map[string]any{"step": "parse", "task_id": taskID})
	profile, err := a.parser.Parse(ctx, strInput)
	if err != nil {
		return a.fail(err)
	}

	// Step 2: Plan tasks
	stepCount++
	if err := a.checkStepLimit(stepCount, maxSteps); err != nil {
		return a.fail(err)
	}
	if err := a.checkAgentRunning(); err != nil {
		return a.fail(err)
	}
	a.emitEvent(ctx, ares_events.EventTaskDispatched, map[string]any{"step": "plan", "task_id": taskID})
	tasks, err := a.planner.Plan(ctx, profile, strInput)
	if err != nil {
		return a.fail(err)
	}
	log.Info("Leader tasks created", "module", "leader", "count", len(tasks))

	// Step 3: Dispatch tasks
	stepCount++
	if err := a.checkStepLimit(stepCount, maxSteps); err != nil {
		return a.fail(err)
	}
	if err := a.checkAgentRunning(); err != nil {
		return a.fail(err)
	}
	a.emitEvent(ctx, ares_events.EventTaskDispatched, map[string]any{"step": "dispatch", "task_id": taskID})
	log.Info("Leader dispatching tasks", "module", "leader")
	results, err := a.dispatcher.Dispatch(ctx, tasks)
	if err != nil {
		return a.fail(err)
	}
	log.Info("Leader dispatch completed", "module", "leader", "result_count", len(results))
	for i, r := range results {
		log.Info("Leader task result", "module", "leader", "index", i, "success", r.Success, "items", len(r.Items), "error", r.Error)
	}

	// Step 4: Aggregate results
	stepCount++
	if err := a.checkStepLimit(stepCount, maxSteps); err != nil {
		return a.fail(err)
	}
	if err := a.checkAgentRunning(); err != nil {
		return a.fail(err)
	}
	result, err := a.aggregator.Aggregate(ctx, results, tasks)
	if err != nil {
		return a.fail(err)
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
		return errors.ErrQueueNotInitialized
	}
	return a.messageQueue.Enqueue(ctx, msg)
}

// ReceiveMessage receives a message from the message queue.
func (a *leaderAgent) ReceiveMessage(ctx context.Context) (*ahp.AHPMessage, error) {
	if a.messageQueue == nil {
		return nil, errors.ErrQueueNotInitialized
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

	log.Info("state restored from snapshot",
		"agent_id", a.id,
		"session_id", a.sessionID,
		"status", string(a.status),
	)
	return nil
}

// ReplayEvents replays a sequence of ares_events to reconstruct state.
// Implements base.StatefulAgent for resurrection support.
//
// Supported event types:
//   - EventSessionCreated: restores session_id
//   - EventMessageAdded: updates last_message_role and message count
//   - EventTaskCreated: restores last_task_id
//   - EventTaskDispatched: updates last_task_id (dispatch progression)
//   - EventTaskCompleted: restores last_completed_task_id
//   - EventAgentStarted/Stopped: updates agent status
//
// Args:
//
//	evts - ordered sequence of ares_events to replay. Nil or empty is a safe no-op.
//
// Returns:
//
//	err - always nil for ReplayEvents; invalid ares_events are silently skipped.
func (a *leaderAgent) ReplayEvents(evts []*ares_events.Event) error {
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
		case ares_events.EventSessionCreated:
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" {
				a.sessionID = sid
			}

		case ares_events.EventMessageAdded:
			msgCount++
			if role, ok := ev.Payload["role"].(string); ok {
				a.conversationSummary = fmt.Sprintf("last_role:%s,msg_count:%d", role, msgCount)
			}

		case ares_events.EventTaskCreated:
			if tid, ok := ev.Payload["task_id"].(string); ok && tid != "" {
				a.lastTaskID = tid
			}

		case ares_events.EventTaskDispatched:
			// A dispatched task has progressed past creation; update lastTaskID
			// so recovery can resume from the dispatched task (LD-2).
			if tid, ok := ev.Payload["task_id"].(string); ok && tid != "" {
				a.lastTaskID = tid
			}

		case ares_events.EventTaskCompleted:
			// Track the most recently completed task (separate from lastTaskID which tracks "created").
			if tid, ok := ev.Payload["task_id"].(string); ok && tid != "" {
				a.lastCompletedTaskID = tid
			}

		case ares_events.EventAgentStarted:
			a.status = models.AgentStatusReady

		case ares_events.EventAgentStopped:
			a.status = models.AgentStatusOffline
		}
	}

	log.Info("ares_events replayed for state reconstruction",
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

// ProcessStream handles user input and returns a stream of ares_events.
// It follows the same workflow as Process but emits ares_events at each phase.
//
//nolint:gocyclo // Complex stream processing with multiple agent phases
func (a *leaderAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	// Ensure lifecycle fields are initialized (P0-5).
	a.ensureInitialized()

	// Ensure mutual exclusion: only one Process/ProcessStream at a time.
	// The lock is held for the lifetime of the streaming goroutine to prevent
	// concurrent Process/ProcessStream calls from racing on status/memory/feedback.
	// It is released by a deferred unlock inside the goroutine. Early-return
	// paths below unlock explicitly before returning.
	a.processingMu.Lock()

	// Atomically check status and transition to Busy.
	a.mu.Lock()
	if a.status == models.AgentStatusOffline {
		a.mu.Unlock()
		if err := a.Start(ctx); err != nil {
			a.processingMu.Unlock()
			return nil, err
		}
		a.mu.Lock()
	}
	if a.status != models.AgentStatusReady {
		a.mu.Unlock()
		a.processingMu.Unlock()
		return nil, errors.ErrAgentNotReady
	}
	a.status = models.AgentStatusBusy
	a.mu.Unlock()

	startTime := time.Now()

	strInput, err := parseInput(input)
	if err != nil {
		a.setStatus(models.AgentStatusReady)
		a.emitCallback(&ares_callbacks.Context{
			Event:   ares_callbacks.EventAgentError,
			AgentID: a.id,
			Error:   err,
		})
		a.processingMu.Unlock()
		return nil, err
	}

	// Initialize memory context (session, messages, similar tasks, task record).
	strInput, sessionID, taskID := a.initMemoryContext(ctx, strInput)

	ch := make(chan base.AgentEvent, DefaultEventChanSize)

	a.streamEg.Go(func() error {
		// Release the processing lock when the stream goroutine exits so that
		// mutual exclusion covers the full streaming lifetime.
		defer a.processingMu.Unlock()

		// Emit start event inside the goroutine so it's always paired with end.
		a.emitCallback(&ares_callbacks.Context{
			Event:   ares_callbacks.EventAgentStart,
			AgentID: a.id,
		})

		defer close(ch)
		defer func() {
			a.setStatus(models.AgentStatusReady)
			duration := time.Since(startTime)
			a.emitCallback(&ares_callbacks.Context{
				Event:    ares_callbacks.EventAgentEnd,
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
		a.emitEvent(ctx, ares_events.EventTaskCreated, map[string]any{
			"step":    "parse",
			"task_id": taskID,
		})

		profile, err := a.parser.Parse(ctx, strInput)
		if err != nil {
			a.emitCallback(&ares_callbacks.Context{
				Event:   ares_callbacks.EventAgentError,
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
		a.emitEvent(ctx, ares_events.EventTaskDispatched, map[string]any{
			"step":    "plan",
			"task_id": taskID,
		})

		tasks, err := a.planner.Plan(ctx, profile, strInput)
		if err != nil {
			a.emitCallback(&ares_callbacks.Context{
				Event:   ares_callbacks.EventAgentError,
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
		log.Info("Leader tasks created", "module", "leader", "count", len(tasks))

		for _, task := range tasks {
			select {
			case ch <- base.AgentEvent{Type: base.EventTaskStart, Source: a.id, Data: task}:
			case <-ctx.Done():
				return nil
			case <-a.stopCh:
				return nil
			}
		}

		a.emitEvent(ctx, ares_events.EventTaskDispatched, map[string]any{
			"step":    "dispatch",
			"task_id": taskID,
		})

		results, err := a.dispatcher.Dispatch(ctx, tasks)
		if err != nil {
			a.emitCallback(&ares_callbacks.Context{
				Event:   ares_callbacks.EventAgentError,
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
			a.emitCallback(&ares_callbacks.Context{
				Event:   ares_callbacks.EventAgentError,
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

func (a *leaderAgent) emitEvent(ctx context.Context, eventType ares_events.EventType, payload map[string]any) {
	if ares_events.Emit(ctx, a.eventStore, a.id, eventType, "leader", payload) {
		log.Debug("event emitted", "agent_id", a.id, "type", eventType)
	}
}

func (a *leaderAgent) emitCallback(ctx *ares_callbacks.Context) {
	if a.ares_callbacks == nil {
		return
	}
	a.ares_callbacks.Emit(ctx)
}
