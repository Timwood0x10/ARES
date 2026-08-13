package leader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// ErrTaskNotStarted indicates a task was never attempted, typically because a
// concurrent task failure cancelled the errgroup context before execution began.
var ErrTaskNotStarted = errors.New("task not started: cancelled by concurrent task failure")

// DefaultDispatcherAgentID is the legacy sender identity used when no
// WithDispatcherAgentID option is supplied.
const DefaultDispatcherAgentID = "leader"

// MessageSender sends messages to sub-agents (for distributed deployment).
type MessageSender interface {
	Send(ctx context.Context, agentAddr string, msg *ahp.AHPMessage) error
}

// DispatcherOption configures a taskDispatcher.
type DispatcherOption func(*taskDispatcher)

// WithDispatcherAgentID sets the sender identity stamped on distributed
// task messages.
func WithDispatcherAgentID(id string) DispatcherOption {
	return func(d *taskDispatcher) {
		if id != "" {
			d.agentID = id
		}
	}
}

// WithDispatcherEventStore injects an EventStore for event-driven dispatch.
// When set, the dispatcher publishes EventSubTaskScheduled and collects
// EventSubTaskResult events instead of calling executors directly.
func WithDispatcherEventStore(store ares_events.EventStore) DispatcherOption {
	return func(d *taskDispatcher) {
		d.eventStore = store
	}
}

// taskDispatcher dispatches tasks to sub-agents via event-driven dispatch.
type taskDispatcher struct {
	mu            sync.RWMutex
	agentRegistry map[models.AgentType]string
	eventStore    ares_events.EventStore
	messageSender MessageSender
	maxParallel   int
	timeout       int
	agentID       string
}

// NewTaskDispatcher creates a new TaskDispatcher.
//
// Args:
//
//	agentRegistry - mapping from agent type to address (sub agent ID), must not be nil.
//	maxParallel - maximum number of parallel task dispatches; uses default if <= 0.
//	timeout - dispatch timeout in seconds; uses default if <= 0.
//	sender - optional message sender for distributed deployment; may be nil.
//	opts - optional configuration (WithDispatcherAgentID, WithDispatcherEventStore).
//
// Returns:
//
//	dispatcher - a new TaskDispatcher instance.
//	err - validation error if agentRegistry is nil.
func NewTaskDispatcher(agentRegistry map[models.AgentType]string, maxParallel int, timeout int, sender MessageSender, opts ...DispatcherOption) (TaskDispatcher, error) {
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
		messageSender: sender,
		maxParallel:   maxParallel,
		timeout:       timeout,
		agentID:       DefaultDispatcherAgentID,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	log.Debug("TaskDispatcher created",
		"max_parallel", maxParallel, "timeout", timeout,
		"has_sender", sender != nil, "has_event_store", d.eventStore != nil,
		"agent_id", d.agentID)
	return d, nil
}

// Dispatch dispatches tasks to sub-agents and collects results.
//
// When an EventStore is configured, tasks are published as EventSubTaskScheduled
// events and results are collected from EventSubTaskResult events. This is the
// primary production path (code_rules_v2 §5.1: single execution path).
//
// When no EventStore is configured, falls back to message sender for distributed
// deployment.
func (d *taskDispatcher) Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	if len(tasks) == 0 {
		return nil, errors.ErrInvalidInput
	}

	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, time.Duration(d.timeout)*time.Second)
	defer cancel()

	if d.eventStore != nil {
		return d.dispatchViaEvents(ctx, tasks)
	}
	return d.dispatchViaSender(ctx, tasks)
}

// dispatchViaEvents publishes task-scheduled events and collects task-result events.
func (d *taskDispatcher) dispatchViaEvents(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	results := make([]*models.TaskResult, len(tasks))
	taskIndex := make(map[string]int, len(tasks))

	// Validate tasks and build index.
	for i, task := range tasks {
		if task == nil {
			results[i] = models.NewTaskResult("", "")
			results[i].SetError("task is nil")
			continue
		}
		taskIndex[task.TaskID] = i
	}

	// Subscribe to result events BEFORE publishing to avoid race.
	resultCh, err := d.eventStore.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{ares_events.EventSubTaskResult},
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe task results: %w", err)
	}

	// Publish scheduled events for valid tasks.
	published := 0
	for i, task := range tasks {
		if task == nil {
			continue
		}

		d.mu.RLock()
		agentAddr, ok := d.agentRegistry[task.AgentType]
		d.mu.RUnlock()

		if !ok {
			results[i] = models.NewTaskResult(task.TaskID, task.AgentType)
			results[i].SetError("agent not found in registry")
			continue
		}

		if !ares_events.Emit(ctx, d.eventStore, agentAddr, ares_events.EventSubTaskScheduled, "dispatcher", map[string]any{
			"task":       task,
			"task_id":    task.TaskID,
			"agent_id":   agentAddr,
			"agent_type": string(task.AgentType),
		}) {
			results[i] = models.NewTaskResult(task.TaskID, task.AgentType)
			results[i].SetError("failed to publish task scheduled event")
			continue
		}
		published++
	}

	if published == 0 {
		return results, nil
	}

	// Collect results from event stream.
	// Each published task must be collected exactly once. A sub-agent may
	// publish a duplicate EventSubTaskResult for the same task (e.g. a retry or
	// an event-store replay), so guard against double-counting with a collected
	// set: a duplicate taskID is skipped without bumping the counter, otherwise
	// collected could reach published while some tasks' results are still nil.
	collected := 0
	collectedIDs := make(map[string]struct{}, published)
	for collected < published {
		select {
		case ev, ok := <-resultCh:
			if !ok {
				for i := 0; i < len(tasks); i++ {
					if results[i] == nil && tasks[i] != nil {
						results[i] = models.NewTaskResult(tasks[i].TaskID, tasks[i].AgentType)
						results[i].SetError("event stream closed before result received")
					}
				}
				return results, fmt.Errorf("%w: event stream closed", errors.ErrDispatchFailed)
			}
			taskID, _ := ev.Payload["task_id"].(string)
			idx, found := taskIndex[taskID]
			if !found {
				continue
			}
			if _, dup := collectedIDs[taskID]; dup {
				// Already collected this task; ignore the duplicate result.
				continue
			}
			collectedIDs[taskID] = struct{}{}
			results[idx] = extractResultFromEvent(ev)
			collected++
		case <-ctx.Done():
			for i, r := range results {
				if r == nil && i < len(tasks) && tasks[i] != nil {
					results[i] = models.NewTaskResult(tasks[i].TaskID, tasks[i].AgentType)
					results[i].SetError("task timed out: " + ctx.Err().Error())
				}
			}
			return results, fmt.Errorf("%w: %v", errors.ErrDispatchFailed, ctx.Err())
		}
	}

	return results, nil
}

// dispatchViaSender is the legacy path for distributed deployment without EventStore.
func (d *taskDispatcher) dispatchViaSender(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	results := make([]*models.TaskResult, len(tasks))
	for i, task := range tasks {
		if task == nil {
			results[i] = models.NewTaskResult("", "")
			results[i].SetError("task is nil")
			continue
		}

		result := models.NewTaskResult(task.TaskID, task.AgentType)
		d.mu.RLock()
		agentAddr, ok := d.agentRegistry[task.AgentType]
		d.mu.RUnlock()

		if !ok {
			result.SetError("agent not found in registry")
			results[i] = result
			continue
		}

		if d.messageSender != nil {
			sessionID := ""
			if task.Context != nil && len(task.Context.Dependencies) > 0 {
				sessionID = task.Context.Dependencies[0]
			}
			msg := ahp.NewTaskMessage(d.getAgentID(), agentAddr, task.TaskID, sessionID, task.Payload)
			if err := d.messageSender.Send(ctx, agentAddr, msg); err != nil {
				result.SetError("failed to send message: " + err.Error())
			} else {
				result.SetSuccess(nil, "task dispatched via message queue to "+agentAddr)
			}
		} else {
			result.SetError("no event store or message sender configured")
		}
		results[i] = result
	}

	for _, r := range results {
		if r != nil && !r.Success {
			return results, fmt.Errorf("%w: task %s failed: %s", errors.ErrDispatchFailed, r.TaskID, r.Error)
		}
	}
	return results, nil
}

// extractResultFromEvent converts an EventSubTaskResult payload to a TaskResult.
func extractResultFromEvent(ev *ares_events.Event) *models.TaskResult {
	taskID, _ := ev.Payload["task_id"].(string)
	agentType, _ := ev.Payload["agent_type"].(string)
	result := models.NewTaskResult(taskID, models.AgentType(agentType))

	if errStr, ok := ev.Payload["error"].(string); ok && errStr != "" {
		result.SetError(errStr)
		return result
	}

	success, _ := ev.Payload["success"].(bool)
	if success {
		if items, ok := ev.Payload["items"].([]*models.RecommendItem); ok {
			result.Items = items
		}
		result.SetSuccess(result.Items, "")
	}
	return result
}

// getAgentID returns the configured sender identity for distributed task
// messages.
func (d *taskDispatcher) getAgentID() string {
	if d.agentID != "" {
		return d.agentID
	}
	return DefaultDispatcherAgentID
}
