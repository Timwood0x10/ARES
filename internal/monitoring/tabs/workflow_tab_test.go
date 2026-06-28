package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/stretchr/testify/assert"
)

func TestWorkflowTab_Interface(t *testing.T) {
	var tab Tab = NewWorkflowTab()
	assert.Equal(t, "workflow", tab.Name())
	assert.Equal(t, "Workflow", tab.Label())
}

func TestWorkflowTab_TaskLifecycle(t *testing.T) {
	tab := NewWorkflowTab()
	now := time.Now()

	// Create a task.
	tab.HandleEvent(&ares_events.Event{
		ID:   "t1",
		Type: ares_events.EventTaskCreated,
		Payload: map[string]any{
			"task_id":  "task-1",
			"name":     "analyze data",
			"agent_id": "a1",
		},
		Timestamp: now,
	})
	snap := tab.Snapshot().(WorkflowTabSnapshot)
	assert.Len(t, snap.Executions, 1)
	assert.Equal(t, dag.StatusRunning, snap.Executions[0].Status)

	// Complete the task.
	tab.HandleEvent(&ares_events.Event{
		ID:   "t2",
		Type: ares_events.EventTaskCompleted,
		Payload: map[string]any{
			"task_id": "task-1",
		},
		Timestamp: now.Add(time.Second),
	})
	snap = tab.Snapshot().(WorkflowTabSnapshot)
	assert.Equal(t, dag.StatusCompleted, snap.Executions[0].Status)
	assert.NotNil(t, snap.Executions[0].CompletedAt)
}

func TestWorkflowTab_TaskFailed(t *testing.T) {
	tab := NewWorkflowTab()
	now := time.Now()

	tab.HandleEvent(&ares_events.Event{
		ID:        "t1",
		Type:      ares_events.EventTaskCreated,
		Payload:   map[string]any{"task_id": "task-1", "agent_id": "a1"},
		Timestamp: now,
	})
	tab.HandleEvent(&ares_events.Event{
		ID:   "t2",
		Type: ares_events.EventTaskFailed,
		Payload: map[string]any{
			"task_id": "task-1",
			"error":   "out of memory",
		},
		Timestamp: now.Add(time.Second),
	})
	snap := tab.Snapshot().(WorkflowTabSnapshot)
	assert.Equal(t, dag.StatusFailed, snap.Executions[0].Status)
	assert.Equal(t, "out of memory", snap.Executions[0].Error)
}

func TestWorkflowTab_IgnoresIrrelevantEvents(t *testing.T) {
	tab := NewWorkflowTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventLLMCall,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(WorkflowTabSnapshot)
	assert.Empty(t, snap.Executions)
}

func TestWorkflowTab_NilEvent(t *testing.T) {
	tab := NewWorkflowTab()
	tab.HandleEvent(nil)
}

func TestWorkflowTab_Capacity(t *testing.T) {
	tab := NewWorkflowTab()
	for i := 0; i < maxExecutions+10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("t%d", i),
			Type:      ares_events.EventTaskCreated,
			Payload:   map[string]any{"task_id": fmt.Sprintf("task-%d", i)},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(WorkflowTabSnapshot)
	assert.Equal(t, maxExecutions, len(snap.Executions))
}

func TestWorkflowTab_CompleteBeforeCreate(t *testing.T) {
	tab := NewWorkflowTab()
	// Completing a task that was never created should not panic.
	tab.HandleEvent(&ares_events.Event{
		ID:        "t1",
		Type:      ares_events.EventTaskCompleted,
		Payload:   map[string]any{"task_id": "nonexistent"},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(WorkflowTabSnapshot)
	assert.Empty(t, snap.Executions)
}

func TestWorkflowTab_Trim(t *testing.T) {
	tab := NewWorkflowTab()
	for i := 0; i < 15; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("t%d", i),
			Type:      ares_events.EventTaskCreated,
			Payload:   map[string]any{"task_id": fmt.Sprintf("task-%d", i)},
			Timestamp: time.Now(),
		})
	}
	assert.Equal(t, 15, len(tab.order))

	tab.Trim(5)
	assert.Equal(t, 5, len(tab.order))
	assert.Equal(t, "task-10", tab.order[0])
	// Verify evicted executions are deleted.
	_, ok := tab.executions["task-0"]
	assert.False(t, ok)
}
