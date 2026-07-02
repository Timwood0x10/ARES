package tabs

import (
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

const maxExecutions = 500

// WorkflowExecution tracks a single task execution lifecycle.
type WorkflowExecution struct {
	TaskID      string         `json:"task_id"`
	Name        string         `json:"name"`
	AgentID     string         `json:"agent_id"`
	Status      dag.NodeStatus `json:"status"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Error       string         `json:"error,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// WorkflowTabSnapshot is the snapshot payload returned by WorkflowTab.Snapshot.
type WorkflowTabSnapshot struct {
	Executions []WorkflowExecution `json:"executions"`
	Total      int                 `json:"total"`
}

// WorkflowTab implements the Tab interface for the Workflow tab.
// It tracks task lifecycle events (created, completed, failed).
type WorkflowTab struct {
	mu         sync.RWMutex
	executions map[string]*WorkflowExecution
	order      []string // insertion order for stable iteration
}

// NewWorkflowTab creates a new WorkflowTab instance.
func NewWorkflowTab() *WorkflowTab {
	return &WorkflowTab{
		executions: make(map[string]*WorkflowExecution),
		order:      make([]string, 0, maxExecutions),
	}
}

// Name returns the tab identifier.
func (t *WorkflowTab) Name() string { return "workflow" }

// Label returns the human-readable tab name.
func (t *WorkflowTab) Label() string { return "Workflow" }

// HandleEvent processes task lifecycle events.
func (t *WorkflowTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case ares_events.EventTaskCreated:
		t.handleTaskCreated(evt)
	case ares_events.EventTaskCompleted:
		t.handleTaskCompleted(evt)
	case ares_events.EventTaskFailed:
		t.handleTaskFailed(evt)
	}
}

// Snapshot returns the current workflow execution state.
func (t *WorkflowTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	executions := make([]WorkflowExecution, 0, len(t.order))
	for _, id := range t.order {
		if ex, ok := t.executions[id]; ok {
			executions = append(executions, *ex)
		}
	}
	return WorkflowTabSnapshot{
		Executions: executions,
		Total:      len(executions),
	}
}

func (t *WorkflowTab) handleTaskCreated(evt *ares_events.Event) {
	taskID := getString(evt.Payload, "task_id")
	if taskID == "" {
		taskID = evt.ID
	}
	ex := &WorkflowExecution{
		TaskID:    taskID,
		Name:      getString(evt.Payload, "name"),
		AgentID:   getString(evt.Payload, "agent_id"),
		Status:    dag.StatusRunning,
		StartedAt: evt.Timestamp,
		Details:   evt.Payload,
	}
	if ex.AgentID == "" {
		ex.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.executions[taskID]; !exists {
		// Enforce cap by evicting oldest.
		if len(t.order) >= maxExecutions {
			oldest := t.order[0]
			t.order = t.order[1:]
			delete(t.executions, oldest)
		}
		t.order = append(t.order, taskID)
	}
	t.executions[taskID] = ex
}

func (t *WorkflowTab) handleTaskCompleted(evt *ares_events.Event) {
	taskID := getString(evt.Payload, "task_id")
	if taskID == "" {
		taskID = evt.ID
	}
	now := evt.Timestamp
	t.mu.Lock()
	defer t.mu.Unlock()
	if ex, ok := t.executions[taskID]; ok {
		ex.Status = dag.StatusCompleted
		ex.CompletedAt = &now
	}
}

func (t *WorkflowTab) handleTaskFailed(evt *ares_events.Event) {
	taskID := getString(evt.Payload, "task_id")
	if taskID == "" {
		taskID = evt.ID
	}
	now := evt.Timestamp
	t.mu.Lock()
	defer t.mu.Unlock()
	if ex, ok := t.executions[taskID]; ok {
		ex.Status = dag.StatusFailed
		ex.CompletedAt = &now
		ex.Error = getString(evt.Payload, "error")
	}
}

// Trim retains at most maxLen executions, discarding the oldest.
func (t *WorkflowTab) Trim(maxLen int) {
	if maxLen <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.order) > maxLen {
		oldest := t.order[0]
		t.order = t.order[1:]
		delete(t.executions, oldest)
	}
}
