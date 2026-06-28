package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

func TestWorkflowTab_Interface(t *testing.T) {
	var tab Tab = NewWorkflowTab()
	if tab.Name() != "workflow" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "workflow")
	}
	if tab.Label() != "Workflow" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "Workflow")
	}
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
	if len(snap.Executions) != 1 {
		t.Fatalf("got %d executions, want 1", len(snap.Executions))
	}
	if snap.Executions[0].Status != dag.StatusRunning {
		t.Errorf("Status = %q, want %q", snap.Executions[0].Status, dag.StatusRunning)
	}

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
	if snap.Executions[0].Status != dag.StatusCompleted {
		t.Errorf("Status = %q, want %q", snap.Executions[0].Status, dag.StatusCompleted)
	}
	if snap.Executions[0].CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
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
	if snap.Executions[0].Status != dag.StatusFailed {
		t.Errorf("Status = %q, want %q", snap.Executions[0].Status, dag.StatusFailed)
	}
	if snap.Executions[0].Error != "out of memory" {
		t.Errorf("Error = %q, want %q", snap.Executions[0].Error, "out of memory")
	}
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
	if len(snap.Executions) != 0 {
		t.Error("non-workflow events should be ignored")
	}
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
	if len(snap.Executions) != maxExecutions {
		t.Errorf("execution count = %d, want %d", len(snap.Executions), maxExecutions)
	}
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
	if len(snap.Executions) != 0 {
		t.Errorf("got %d executions, want 0", len(snap.Executions))
	}
}
