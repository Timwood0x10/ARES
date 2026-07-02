package leader

import (
	"context"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	apperrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"

	"golang.org/x/sync/errgroup"
)

// ErrTaskNotStarted indicates a task was never attempted, typically because a
// concurrent task failure cancelled the errgroup context before execution began.
var ErrTaskNotStarted = errors.New("task not started: cancelled by concurrent task failure")

// TaskExecutorFunc is a function type for executing tasks directly.
type TaskExecutorFunc func(ctx context.Context, task *models.Task) (*models.TaskResult, error)

// MessageSender sends messages to sub-agents (for distributed deployment).
type MessageSender interface {
	Send(ctx context.Context, agentAddr string, msg *ahp.AHPMessage) error
}

// taskDispatcher dispatches tasks to sub-agents.
type taskDispatcher struct {
	mu            sync.RWMutex
	agentRegistry map[models.AgentType]string
	executorFuncs map[models.AgentType]TaskExecutorFunc
	messageSender MessageSender
	maxParallel   int
	timeout       int
}

// NewTaskDispatcher creates a new TaskDispatcher.
//
// Args:
//
//	agentRegistry - mapping from agent type to address, must not be nil.
//	maxParallel - maximum number of parallel task dispatches; uses default if <= 0.
//	timeout - dispatch timeout in seconds; uses default if <= 0.
//	sender - optional message sender for distributed deployment; may be nil for local-only mode.
//
// Returns:
//
//	dispatcher - a new TaskDispatcher instance.
//	err - validation error if agentRegistry is nil.
func NewTaskDispatcher(agentRegistry map[models.AgentType]string, maxParallel int, timeout int, sender MessageSender) (TaskDispatcher, error) {
	if agentRegistry == nil {
		return nil, errors.New("task dispatcher: agent registry cannot be nil")
	}
	if maxParallel <= 0 {
		maxParallel = DefaultMaxParallel
	}
	if timeout <= 0 {
		timeout = DefaultDispatcherTimeoutSeconds
	}
	d := &taskDispatcher{
		agentRegistry: agentRegistry,
		executorFuncs: make(map[models.AgentType]TaskExecutorFunc),
		messageSender: sender,
		maxParallel:   maxParallel,
		timeout:       timeout,
	}
	log.Debug("TaskDispatcher created",
		"max_parallel", maxParallel, "timeout", timeout,
		"has_sender", sender != nil)
	return d, nil
}

// RegisterExecutor registers an executor function for a specific agent type.
func (d *taskDispatcher) RegisterExecutor(agentType models.AgentType, fn func(ctx context.Context, task *models.Task) (*models.TaskResult, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.executorFuncs[agentType] = fn
}

// Dispatch dispatches tasks to sub-agents in parallel.
func (d *taskDispatcher) Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	if len(tasks) == 0 {
		return nil, apperrors.ErrInvalidInput
	}

	g, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, d.maxParallel)
	var resultsMu sync.Mutex

	results := make([]*models.TaskResult, len(tasks))

	for i, task := range tasks {
		task := task
		g.Go(func() error {
			// Handle nil task elements to prevent panic.
			if task == nil {
				resultsMu.Lock()
				results[i] = models.NewTaskResult("", "")
				results[i].SetError("task is nil")
				resultsMu.Unlock()
				return fmt.Errorf("task %d is nil", i)
			}

			result := models.NewTaskResult(task.TaskID, task.AgentType)

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				result.SetError("task cancelled: " + ctx.Err().Error())
				resultsMu.Lock()
				results[i] = result
				resultsMu.Unlock()
				return ctx.Err()
			}
			defer func() { <-sem }()

			execResult := d.executeTask(ctx, task)
			resultsMu.Lock()
			results[i] = execResult
			resultsMu.Unlock()
			if !execResult.Success {
				return fmt.Errorf("task %s failed: %s", task.TaskID, execResult.Error)
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		for i, r := range results {
			if r == nil && i < len(tasks) && tasks[i] != nil {
				results[i] = models.NewTaskResult(tasks[i].TaskID, tasks[i].AgentType)
				results[i].SetError(ErrTaskNotStarted.Error())
			}
		}
		return results, fmt.Errorf("%w: %v", apperrors.ErrDispatchFailed, err)
	}

	return results, nil
}

func (d *taskDispatcher) executeTask(ctx context.Context, task *models.Task) *models.TaskResult {
	result := models.NewTaskResult(task.TaskID, task.AgentType)

	// Get agent address from registry
	d.mu.RLock()
	agentAddr, ok := d.agentRegistry[task.AgentType]
	fn, hasExecutor := d.executorFuncs[task.AgentType]
	d.mu.RUnlock()

	if !ok {
		result.SetError("agent not found in registry")
		return result
	}

	log.Debug("Executing task", "task_id", task.TaskID, "agent_type", task.AgentType, "agent_addr", agentAddr)

	// Check if we have a direct executor registered
	if hasExecutor {
		log.Debug("Calling executor", "agent_type", task.AgentType)
		execResult, err := fn(ctx, task)
		if err != nil {
			log.Error("Executor error", "agent_type", task.AgentType, "error", err)
			result.SetError(err.Error())
			return result
		}
		if execResult == nil {
			result.SetError("executor returned nil result")
			return result
		}
		log.Debug("Executor returned", "agent_type", task.AgentType, "item_count", len(execResult.Items), "success", execResult.Success)
		return execResult
	}

	// If no local executor, use message sender (for distributed deployment)
	if d.messageSender != nil {
		sessionID := ""
		if task.Context != nil && len(task.Context.Dependencies) > 0 {
			sessionID = task.Context.Dependencies[0]
		}
		msg := ahp.NewTaskMessage(d.getAgentID(), agentAddr, task.TaskID, sessionID, task.Payload)
		if err := d.messageSender.Send(ctx, agentAddr, msg); err != nil {
			result.SetError("failed to send message: " + err.Error())
			return result
		}
		result.SetSuccess(nil, "task dispatched via message queue to "+agentAddr)
		return result
	}

	// No executor and no message sender - return error
	result.SetError("no executor or message sender registered for agent type: " + string(task.AgentType))
	return result
}

func (d *taskDispatcher) getAgentID() string {
	return "leader"
}
