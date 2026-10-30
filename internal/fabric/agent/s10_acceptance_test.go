package agentfabric

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// promptRecordingChat plays a fixed conversation and keeps every prompt it was shown, which is what
// this file needs: whether the resource rule reached the model, not only which tools it saw.
type promptRecordingChat struct {
	responses []*llmcore.GenerateResponse
	calls     int
	prompts   [][]*llmcore.LLMMessage
}

func (c *promptRecordingChat) Chat(_ context.Context, prompt []*llmcore.LLMMessage, _ []llmcore.Tool, _ map[string]any) (*llmcore.GenerateResponse, error) {
	c.prompts = append(c.prompts, prompt)
	idx := c.calls
	c.calls++
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	return &llmcore.GenerateResponse{Content: "done"}, nil
}

func toolCall(name, args string) llmcore.ToolCall {
	return llmcore.ToolCall{
		ID:       "tc-" + name,
		Type:     "function",
		Function: llmcore.FunctionCall{Name: name, Arguments: args},
	}
}

// l1DAGWithPolicy builds an L1 catalogue node for grep carrying the operator declarations.
func l1DAGWithPolicy(t *testing.T, prior string, maxAttempts int, timeout string) *engine.MutableDAG {
	t.Helper()
	metadata := map[string]string{
		l1MetaEnabled: "true",
		l1MetaBudget:  "0",
		l1MetaPrior:   prior,
	}
	if maxAttempts > 0 {
		metadata[l1MetaMaxAttempts] = strconv.Itoa(maxAttempts)
	}
	if timeout != "" {
		metadata[l1MetaTaskTimeout] = timeout
	}
	dag, err := engine.NewMutableDAG([]*engine.Step{{
		ID:        resources.ToolClassID("grep", resources.ToolArgShape(grepSchema(t))),
		Name:      "grep",
		AgentType: "tool/grep",
		Metadata:  metadata,
	}})
	require.NoError(t, err)
	return dag
}

// TestS10_PriorSteersTheModelAndTheGrowthBindsToTheTool is acceptance item 1: the model must SEE
// the resource rule, act on it, and the resulting node must be a real tool node — compiled into a
// task bound to that tool, carrying the policy the catalogue declared.
//
// The prior reaching the prompt was already covered; what was missing is that the call it produces
// survives the whole way into an executable task. A policy that only decorates a prompt would look
// identical from the prompt alone.
func TestS10_PriorSteersTheModelAndTheGrowthBindsToTheTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := planprojection.NewCompileCoordinator(fabric, nil)
	reg := NewSessionRegistry()
	sessionID := "sess-s10-prior"
	compileCoord := func(_ context.Context, dag *engine.MutableDAG) (stop func()) {
		return coord.SubscribeGraphEvents(ctx, dag)
	}

	g, err := reg.InitSession(sessionID, "find the symbol", nil, compileCoord)
	require.NoError(t, err)
	rootStep := g.DAG().StepIndex()[g.Root()]
	_, err = fabric.CompileNode(ctx, planprojection.ProjectStep(rootStep))
	require.NoError(t, err)
	driveTaskToCompleted(t, ctx, fabric, g.Root(), "find the symbol")

	chat := &promptRecordingChat{responses: []*llmcore.GenerateResponse{
		{ToolCalls: []llmcore.ToolCall{toolCall("grep", `{"query":"symbol"}`)}},
	}}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		L1DAG:      l1DAGWithPolicy(t, "always search with grep first", 2, "45s"),
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{"input": "find the symbol", planMetadataKey: sessionID}

	_, err = planner.ExecuteStep(ctx, planTask)
	require.NoError(t, err)

	// 1. The model was told what the resource is for.
	require.NotEmpty(t, chat.prompts, "the planner must have called the model")
	found := false
	for _, msg := range chat.prompts[0] {
		if msg.Role == roleSystem && strings.Contains(msg.Content, "always search with grep first") {
			found = true
		}
	}
	assert.True(t, found, "the catalogue prior must be visible to the model")

	// 2. Its call grew a real tool node.
	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	assert.True(t, g.HasNode(grepID), "the call the model made must become a node")

	// 3. That node compiled into a task bound to grep, carrying the declared policy.
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, taskErr := fabric.Task(grepID)
		if taskErr == nil {
			assert.Equal(t, "tool/grep", task.Capability, "the task must execute as the tool, not as the planner")
			assert.Equal(t, 2, task.RetryPolicy.MaxRetries, "and carry the catalogue's max_attempts")
			assert.False(t, task.Deadline.IsZero(), "and its task_timeout")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the grep node never compiled: %v", taskErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestS10_FabricatedToolNameGrowsNoNode is acceptance item 2's last case: the model names a tool that
// does not exist. Nothing may be grown for it — a node for an unregistered capability would compile
// into a task no executor claims, which is worse than refusing the call.
func TestS10_FabricatedToolNameGrowsNoNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	sessionID := "sess-s10-fabricated"
	_, err := reg.InitSession(sessionID, "prompt", nil, func(context.Context, *engine.MutableDAG) func() {
		return func() {}
	})
	require.NoError(t, err)

	chat := &promptRecordingChat{responses: []*llmcore.GenerateResponse{
		{ToolCalls: []llmcore.ToolCall{toolCall("rgx-not-a-tool", `{"query":"x"}`)}},
	}}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		L1DAG:      buildPriorL1DAG(t, ""),
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{"input": "prompt", planMetadataKey: sessionID}

	_, err = planner.ExecuteStep(ctx, planTask)
	require.NoError(t, err)

	assert.False(t, regHasToolNode(t, reg, sessionID, "rgx-not-a-tool"),
		"a tool the registry does not describe must never become a node")
}

// TestS10_ToolBudgetIsCountedPerSession is acceptance item 6: the catalogue is shared, the budget is
// not. A budget of one must allow one node in EACH session, and a later catalogue change must not
// rewrite nodes that already exist.
func TestS10_ToolBudgetIsCountedPerSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	l1 := l1DAGWithPolicy(t, "grep first", 0, "")

	// budget=1 for the shared catalogue entry.
	nodeID := resources.ToolClassID("grep", resources.ToolArgShape(grepSchema(t)))
	require.NoError(t, l1.SetNodeMetadata(nodeID, map[string]string{
		l1MetaEnabled: "true",
		l1MetaBudget:  "1",
		l1MetaPrior:   "grep first",
	}))

	for _, sessionID := range []string{"sess-budget-a", "sess-budget-b"} {
		sessionID := sessionID
		_, err := reg.InitSession(sessionID, "prompt", nil, func(context.Context, *engine.MutableDAG) func() {
			return func() {}
		})
		require.NoError(t, err)

		chat := &promptRecordingChat{responses: []*llmcore.GenerateResponse{
			{ToolCalls: []llmcore.ToolCall{toolCall("grep", `{"query":"x"}`)}},
		}}
		planner, err := NewPlannerCognition(PlannerDeps{
			ChatClient: chat,
			ToolBinder: &plannerTestBinder{},
			Sessions:   reg,
			Fabric:     fabric,
			L1DAG:      l1,
			Logger:     slog.Default(),
		})
		require.NoError(t, err)

		planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
		planTask.SessionID = sessionID
		planTask.Payload = map[string]any{"input": "prompt", planMetadataKey: sessionID}
		_, err = planner.ExecuteStep(ctx, planTask)
		require.NoError(t, err)

		assert.True(t, regHasToolNode(t, reg, sessionID, "grep"),
			"session %s must get its own budget of one", sessionID)
	}
}

// regHasToolNode reports whether the session's L2 graph contains a node for the tool.
func regHasToolNode(t *testing.T, reg *SessionRegistry, sessionID, tool string) bool {
	t.Helper()
	g, err := reg.GetSession(sessionID)
	if err != nil {
		return false
	}
	for _, step := range g.DAG().StepIndex() {
		if step != nil && step.AgentType == "tool/"+tool {
			return true
		}
	}
	return false
}
