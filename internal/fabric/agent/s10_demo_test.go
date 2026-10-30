package agentfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestS10_DeclarationReachesTheTask is the demonstration the plan asks for, in one place: what the
// L1 catalogue declares about a tool must end up as the retry budget and the absolute deadline of the
// task the planner creates.
//
// The links are individually tested; this one asserts the JOIN, because a break anywhere in the
// chain (catalogue → read → step → projection → compile) looks identical from the outside: "the
// configuration was ignored".
func TestS10_DeclarationReachesTheTask(t *testing.T) {
	schema := resources.ToolSchema{Name: "rg"}
	nodeID := resources.ToolClassID("rg", resources.ToolArgShape(schema))

	// What the operator declared, as it appears on the catalogue node.
	dag, err := engine.NewMutableDAG([]*engine.Step{{
		ID:        nodeID,
		Name:      "rg",
		AgentType: "tool/rg",
		Metadata: map[string]string{
			l1MetaMaxAttempts: "1", // one attempt: never retry
			l1MetaTaskTimeout: "30s",
		},
	}})
	require.NoError(t, err)

	c := &plannerCognition{binder: &tagBinder{schemas: []resources.ToolSchema{schema}}, l1: dag}

	// The planner grows a tool node for this call.
	g, err := NewL2Graph("root", "find the symbol", nil)
	require.NoError(t, err)
	require.NoError(t, g.AddToolNodeWithPolicy(context.Background(), "n1", "rg",
		map[string]any{}, "root", c.toolStepPolicy("rg")))

	// Step → plan step → task, using the production projection and compiler.
	plan := planprojection.ProjectStep(stepByID(t, g, "n1"))
	rootPlan := planprojection.ProjectStep(stepByID(t, g, "root"))
	fabric := taskfabric.NewFabric()
	// The batch has to be self-contained: a compiled step may only depend on steps in the same call.
	_, err = fabric.CompilePlan(context.Background(), []taskfabric.PlanStep{rootPlan, plan})
	require.NoError(t, err)

	task, err := fabric.Task(plan.ID)
	require.NoError(t, err)

	assert.Zero(t, task.RetryPolicy.MaxRetries,
		"max_attempts: 1 must reach the task as no retries, not as the plan default")
	assert.False(t, task.CanRetry(), "and the task must actually stop after the first failure")

	// The deadline is resolved at compile time from the relative budget. Asserting the window rather
	// than an exact instant keeps the test independent of the fabric clock implementation.
	assert.False(t, task.Deadline.IsZero(), "task_timeout must produce an absolute deadline")
	assert.WithinDuration(t, time.Now().Add(30*time.Second), task.Deadline, 10*time.Second)
}
