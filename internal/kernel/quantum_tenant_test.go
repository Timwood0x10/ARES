package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/tenantctx"
)

// capturingExecutor records the ctx tenant and the models.Task tenant it was
// invoked with, then completes the task.
type capturingExecutor struct {
	id         string
	ctxTenant  string
	taskTenant string
}

func (e *capturingExecutor) ID() string { return e.id }
func (e *capturingExecutor) Type() models.AgentType {
	return models.AgentType("echo")
}
func (e *capturingExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	e.ctxTenant = tenantctx.From(ctx)
	e.taskTenant = task.TenantID
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "captured")
	return &sub.StepOutcome{Done: true, Result: res}, nil
}

// TestQuantumCarriesTaskTenant pins the read/write coherence carrier: the
// scheduler stamps the task's checkpoint-envelope tenant into the quantum's
// context, so every cognition, tool call and knowledge query downstream
// resolves tenantctx per request instead of a process-global. This is the
// mechanism that replaced the akgActiveNamespace last-write-wins binding.
func TestQuantumCarriesTaskTenant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	env := taskfabric.NewCheckpointEnvelope(map[string]any{"input": "q"})
	env.TenantID = "tenant-acme"
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID:          "t1",
		Capability:  "echo",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		Checkpoint:  env,
	}))

	exec := &capturingExecutor{id: "agent-1"}
	tracker := NewLoadTracker()
	sched := New(fabric, map[string]CapabilityExecutor{"agent-1": exec}, tracker)
	sched.PollInterval = 2 * time.Millisecond
	go sched.Run(ctx)
	defer cancel()

	require.Eventually(t, func() bool {
		tk, err := fabric.Task("t1")
		return err == nil && tk.State == taskfabric.StateCompleted
	}, 5*time.Second, 5*time.Millisecond, "task must complete")

	require.Equal(t, "tenant-acme", exec.ctxTenant,
		"the quantum's context must carry the task's envelope tenant (tenantctx)")
	require.Equal(t, "tenant-acme", exec.taskTenant,
		"ToModelTask must restore the envelope tenant onto models.Task")
}
