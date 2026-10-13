package agentfabric

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// rationaleChat answers the first quantum with a tool call AND a stated
// rationale, then records every later prompt so a test can assert what the
// successor quantum was actually shown. Later rounds answer directly: the read
// side runs before the tool-growth branch, so the prompt is captured either way.
type rationaleChat struct {
	mu        sync.Mutex
	calls     int
	rationale string
	prompts   [][]*llmcore.LLMMessage
}

func (c *rationaleChat) Chat(_ context.Context, msgs []*llmcore.LLMMessage, _ []llmcore.Tool, _ map[string]any) (*llmcore.GenerateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.prompts = append(c.prompts, msgs)
	if c.calls == 1 {
		return &llmcore.GenerateResponse{
			Content: c.rationale,
			ToolCalls: []llmcore.ToolCall{{
				ID:       "tc-1",
				Type:     "function",
				Function: llmcore.FunctionCall{Name: "grep", Arguments: `{"query":"first"}`},
			}},
		}, nil
	}
	return &llmcore.GenerateResponse{Content: "done"}, nil
}

// promptText is the nth (1-based) prompt flattened for substring assertions.
func (c *rationaleChat) promptText(n int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= 0 || n > len(c.prompts) {
		return ""
	}
	var b strings.Builder
	for _, m := range c.prompts[n-1] {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// TestRationaleFromBounded pins the write-side bound. The cap is a product
// decision (whatever is injected into a prompt is paid for in tokens on every
// later quantum), so an oversized rationale must not reach the durable envelope
// in the first place — the test asserts on rune count, which is what the model
// and the token accounting both see.
func TestRationaleFromBounded(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", 1000)
	tests := []struct {
		name string
		resp *llmcore.GenerateResponse
		want func(t *testing.T, got string)
	}{
		{
			name: "nil response has no rationale",
			resp: nil,
			want: func(t *testing.T, got string) { assert.Empty(t, got) },
		},
		{
			name: "whitespace-only content is not a rationale",
			resp: &llmcore.GenerateResponse{Content: "   \n\t "},
			want: func(t *testing.T, got string) { assert.Empty(t, got) },
		},
		{
			name: "short content passes through trimmed",
			resp: &llmcore.GenerateResponse{Content: "  because the parser is shared  "},
			want: func(t *testing.T, got string) { assert.Equal(t, "because the parser is shared", got) },
		},
		{
			name: "oversized content is cut to the bound with an ellipsis",
			resp: &llmcore.GenerateResponse{Content: oversized},
			want: func(t *testing.T, got string) {
				require.NotEmpty(t, got)
				assert.LessOrEqual(t, len([]rune(got)), rationaleMaxRunes,
					"the stored rationale must respect the rune bound")
				assert.True(t, strings.HasSuffix(got, "..."),
					"a cut rationale must say it was cut, got %q", got[len(got)-8:])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.want(t, rationaleFrom(tt.resp))
		})
	}
}

// TestRationaleTravelsToSuccessorQuantum is the cognition-persistence contract
// (P0-4/A, defect A6): the reasoning of one quantum must reach the next one, so
// a session continued by a REPLACEMENT agent — the default outcome of an agent
// death — can still see why the work went the way it did. Both halves are
// asserted here because either one alone is useless: the deciding quantum must
// stamp the node it grows for the next round, and the successor must read it
// from its own payload.
func TestRationaleTravelsToSuccessorQuantum(t *testing.T) {
	const stated = "I grep first because the question is about the parser."

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := planprojection.NewCompileCoordinator(fabric, nil)
	reg := NewSessionRegistry()
	compileCoord := func(_ context.Context, dag *engine.MutableDAG) (stop func()) {
		return coord.SubscribeGraphEvents(ctx, dag)
	}

	const sessionID = "rationale-handoff"
	g, err := reg.InitSession(sessionID, "test prompt", nil, compileCoord)
	require.NoError(t, err)

	rootStep := g.DAG().StepIndex()[g.Root()]
	_, err = fabric.CompileNode(ctx, planprojection.ProjectStep(rootStep))
	require.NoError(t, err)
	driveTaskToCompleted(t, ctx, fabric, g.Root(), "test prompt")

	chat := &rationaleChat{rationale: stated}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	// Round 1 decides and grows the node the next round will run on.
	planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{"input": "test prompt", planMetadataKey: sessionID}
	out, err := planner.ExecuteStep(ctx, planTask)
	require.NoError(t, err)
	require.True(t, out.Done)

	nextPlanID := SessionNodeID(sessionID, 1, "plan", 0)
	waitForTaskExists(t, fabric, nextPlanID, 2*time.Second)
	projected, err := fabric.Task(nextPlanID)
	require.NoError(t, err)
	dc, err := taskfabric.DecodeCheckpoint(projected.Checkpoint)
	require.NoError(t, err)
	assert.Equal(t, stated, dc.Payload["arg."+rationaleKey],
		"the deciding quantum must stamp its rationale onto the node it grows for the next round")

	// Round 2 runs on that node. The scheduler hands a task its own envelope
	// payload (ToModelTask), which is what this builds — so the assertion below
	// covers the same path production takes, not a test-only shortcut.
	planTask2 := models.NewTask(nextPlanID, models.AgentType("ares/plan"), nil)
	planTask2.SessionID = sessionID
	planTask2.Payload = dc.Payload
	_, err = planner.ExecuteStep(ctx, planTask2)
	require.NoError(t, err)

	assert.Contains(t, chat.promptText(2), predecessorRationaleHeader+stated,
		"the successor quantum must be shown the previous quantum's rationale")
}

// TestSuccessorWithoutRationaleGetsNoMessage is the other half of the contract:
// when the provider emitted no content alongside its tool calls (the common
// case), NOTHING is injected — no placeholder, no invented reasoning. Absence
// must stay absent, or the successor would be reasoning about fiction.
func TestSuccessorWithoutRationaleGetsNoMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := planprojection.NewCompileCoordinator(fabric, nil)
	reg := NewSessionRegistry()
	compileCoord := func(_ context.Context, dag *engine.MutableDAG) (stop func()) {
		return coord.SubscribeGraphEvents(ctx, dag)
	}

	const sessionID = "rationale-absent"
	g, err := reg.InitSession(sessionID, "test prompt", nil, compileCoord)
	require.NoError(t, err)

	rootStep := g.DAG().StepIndex()[g.Root()]
	_, err = fabric.CompileNode(ctx, planprojection.ProjectStep(rootStep))
	require.NoError(t, err)
	driveTaskToCompleted(t, ctx, fabric, g.Root(), "test prompt")

	chat := &rationaleChat{} // no rationale on round 1
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{"input": "test prompt", planMetadataKey: sessionID}
	_, err = planner.ExecuteStep(ctx, planTask)
	require.NoError(t, err)

	nextPlanID := SessionNodeID(sessionID, 1, "plan", 0)
	waitForTaskExists(t, fabric, nextPlanID, 2*time.Second)
	projected, err := fabric.Task(nextPlanID)
	require.NoError(t, err)
	dc, err := taskfabric.DecodeCheckpoint(projected.Checkpoint)
	require.NoError(t, err)
	assert.NotContains(t, dc.Payload, "arg."+rationaleKey,
		"a quantum with no stated rationale must not stamp one")

	planTask2 := models.NewTask(nextPlanID, models.AgentType("ares/plan"), nil)
	planTask2.SessionID = sessionID
	planTask2.Payload = dc.Payload
	_, err = planner.ExecuteStep(ctx, planTask2)
	require.NoError(t, err)

	assert.NotContains(t, chat.promptText(2), predecessorRationaleHeader,
		"an absent rationale must inject no message at all")
}
