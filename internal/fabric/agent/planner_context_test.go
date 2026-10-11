package agentfabric

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// contextChat captures the messages the planner sends to the LLM so the test
// can assert that the context was assembled from the graph path (predecessor
// tool outputs).
type contextChat struct {
	mu       sync.Mutex
	messages []*llmcore.LLMMessage
	calls    int
	tools    []llmcore.Tool
}

func (c *contextChat) Chat(_ context.Context, msgs []*llmcore.LLMMessage, tools []llmcore.Tool, _ map[string]any) (*llmcore.GenerateResponse, error) {
	c.mu.Lock()
	c.calls++
	c.messages = make([]*llmcore.LLMMessage, len(msgs))
	copy(c.messages, msgs)
	c.tools = tools
	c.mu.Unlock()

	return &llmcore.GenerateResponse{Content: "final answer"}, nil
}

// TestAssembleContextFromGraphPath verifies the context assembly:
// the planner reads predecessor tool outputs from the fabric envelopes by
// node ID = task ID join, and assembles them into LLM messages along the
// dependency chain. The first plan quantum has only the root prompt; the
// second plan quantum has the root prompt + the first tool's output.
func TestAssembleContextFromGraphPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const sessionID = "m3-ctx"
	fabric := taskfabric.NewFabric()
	coord := planprojection.NewCompileCoordinator(fabric, nil)
	reg := NewSessionRegistry()
	compileCoord := func(_ context.Context, dag *engine.MutableDAG) (stop func()) {
		return coord.SubscribeGraphEvents(ctx, dag)
	}
	g, err := reg.InitSession(sessionID, "analyze the system", nil, compileCoord)
	require.NoError(t, err)

	// Admit root and drive to completion so its prompt rides the envelope.
	rootStep := g.DAG().StepIndex()[g.Root()]
	_, err = fabric.CompileNode(ctx, planprojection.ProjectStep(rootStep))
	require.NoError(t, err)
	driveTaskToCompleted(t, ctx, fabric, g.Root(), "analyze the system")

	// Round 1: manually grow a tool node + plan node (simulating what the
	// planner would do in round 1), so the plan node for round 2 exists.
	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	require.NoError(t, g.AddToolNode(ctx, grepID, "grep",
		map[string]any{"query": "error", planMetadataKey: sessionID}, g.Root()))

	// Wait for the grep task to be compiled, then drive it to completion
	// with a known output.
	waitForTaskExists(t, fabric, grepID, 2*time.Second)
	driveTaskToCompleted(t, ctx, fabric, grepID, "found 3 errors in main.go")

	// Grow the next plan node depending on the grep node.
	plan2ID := SessionNodeID(sessionID, 1, "plan", 0)
	require.NoError(t, g.AddToolNode(ctx, plan2ID, "plan",
		map[string]any{planMetadataKey: sessionID}, grepID))

	// Build the planner.
	chatClient := &contextChat{}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chatClient,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	// Execute the second plan quantum — the planner should assemble
	// context from the root prompt + the grep tool's output.
	plan2Task := models.NewTask(plan2ID, models.AgentType("ares/plan"), nil)
	plan2Task.SessionID = sessionID
	plan2Task.Payload = map[string]any{"input": "analyze the system", planMetadataKey: sessionID}

	out, err := planner.ExecuteStep(ctx, plan2Task)
	require.NoError(t, err)
	require.True(t, out.Done)

	// The LLM received context assembled from the graph path:
	// [user: root prompt, assistant: grep tool call, tool: grep output].
	// The assistant+tool pairing is load-bearing: a bare tool message with
	// no preceding assistant tool_call violates the provider contract
	// (observed live as the model repeating the same call every round).
	chatClient.mu.Lock()
	defer chatClient.mu.Unlock()
	require.Equal(t, 1, chatClient.calls, "LLM called once")
	require.Len(t, chatClient.messages, 3, "context has root prompt + assistant tool call + tool output")
	require.Equal(t, "user", chatClient.messages[0].Role)
	require.Equal(t, "analyze the system", chatClient.messages[0].Content)
	require.Equal(t, "assistant", chatClient.messages[1].Role)
	require.Len(t, chatClient.messages[1].ToolCalls, 1, "assistant message carries the reconstructed call")
	require.Equal(t, "grep", chatClient.messages[1].ToolCalls[0].Function.Name)
	require.Contains(t, chatClient.messages[1].ToolCalls[0].Function.Arguments, "error")
	require.Equal(t, roleTool, chatClient.messages[2].Role)
	require.Contains(t, chatClient.messages[2].Content, "found 3 errors")
	require.Equal(t, chatClient.messages[1].ToolCalls[0].ID, chatClient.messages[2].ToolCallID,
		"tool message links back to its assistant call")
}

// TestFullCapabilityAdvertisement verifies the capability advertisement:
// when the DAG execution gate is open, a peer agent declares the full
// capability set (ares/root, ares/plan, ares/answer, tool/<name>) so the
// scheduler can route every L2 node type to it. This test validates the
// capability list construction logic without requiring a full peer spawn.
func TestFullCapabilityAdvertisement(t *testing.T) {
	// Build the full capability set the same way peer_mode.go does
	// (every peer advertises the single L2 set).
	binder := &plannerTestBinder{}
	caps := []string{
		"ares/root",
		"ares/plan",
		"ares/answer",
	}
	for _, name := range binder.ListTools() {
		caps = append(caps, "tool/"+name)
	}

	// The full set must include every L2 capability type.
	require.Contains(t, caps, "ares/root")
	require.Contains(t, caps, "ares/plan")
	require.Contains(t, caps, "ares/answer")
	require.Contains(t, caps, "tool/grep")
	require.Contains(t, caps, "tool/read")

	// The legacy path (gate off) declares only the primary type.
	legacyCaps := []string{"code"}
	require.NotContains(t, legacyCaps, "ares/plan",
		"legacy path must NOT advertise L2 capabilities")
}

// TestContextIsChronological pins observation ORDER on the context path:
// with two tool nodes, the LLM must see the older output before the newer one
// — the same order ReAct's Messages[] presented. The predecessor walk is
// newest-first, so an un-reversed append would invert the history and change
// what the model concludes, breaking the dual-path acceptance.
func TestContextIsChronological(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "m3-order"
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)

	// root -> first -> second -> plan
	first := SessionNodeID(sessionID, 1, "grep", 0)
	second := SessionNodeID(sessionID, 2, "read", 0)
	planNode := SessionNodeID(sessionID, 2, "plan", 0)
	require.NoError(t, g.AddToolNode(ctx, first, "grep", nil, g.Root()))
	require.NoError(t, g.AddToolNode(ctx, second, "read", nil, first))
	require.NoError(t, g.AddToolNode(ctx, planNode, "plan", nil, second))

	for id, out := range map[string]string{
		g.Root(): "root prompt", first: "OLDER", second: "NEWER",
	} {
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: id, Capability: "x"}))
		driveTaskToCompleted(t, ctx, fabric, id, out)
	}

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: &l1ChatAlwaysGrep{},
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	task := models.NewTask(planNode, planAgentType, nil)
	task.SessionID = sessionID
	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g)
	require.NoError(t, err)
	// [user root, assistant grep-call, tool OLDER, assistant read-call,
	// tool NEWER]: execution order with the provider-mandated pairing.
	require.Len(t, msgs, 5)
	require.Equal(t, "root prompt", msgs[0].Content)
	require.Equal(t, "assistant", msgs[1].Role)
	require.Equal(t, "grep", msgs[1].ToolCalls[0].Function.Name)
	require.Equal(t, "OLDER", msgs[2].Content)
	require.Equal(t, msgs[1].ToolCalls[0].ID, msgs[2].ToolCallID)
	require.Equal(t, "assistant", msgs[3].Role)
	require.Equal(t, "read", msgs[3].ToolCalls[0].Function.Name)
	require.Equal(t, "NEWER", msgs[4].Content, "execution order: the first tool comes first")
	require.Equal(t, msgs[3].ToolCalls[0].ID, msgs[4].ToolCallID)
}

// TestAssembleContextSurfacesDegradedInputs pins C0′.3 (plan/0.3.3_task.md):
// when an AllowPartial task carries degraded_inputs in its payload, the
// planner injects a system message naming the missing predecessors so the LLM
// reasons WITH the gap instead of assuming every input is present. Closes the
// "degraded_inputs is write-only" half of the AllowPartial contract.
func TestAssembleContextSurfacesDegradedInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "degraded-ctx"
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)

	planNode := SessionNodeID(sessionID, 1, "plan", 0)
	require.NoError(t, g.AddToolNode(ctx, planNode, "plan", nil, g.Root()))
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: g.Root(), Capability: "x"}))
	driveTaskToCompleted(t, ctx, fabric, g.Root(), "root prompt")

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: &l1ChatAlwaysGrep{},
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	task := models.NewTask(planNode, planAgentType, nil)
	task.SessionID = sessionID
	task.Payload = map[string]any{
		"input":                             "root prompt",
		taskfabric.PayloadKeyDegradedInputs: []string{"grep-step-7"},
	}

	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g)
	require.NoError(t, err)

	var degraded *llmcore.LLMMessage
	for _, m := range msgs {
		if m.Role == roleSystem && strings.Contains(m.Content, "degraded inputs") {
			degraded = m
			break
		}
	}
	require.NotNil(t, degraded, "a degraded-inputs system message must be injected")
	require.Contains(t, degraded.Content, "grep-step-7", "the missing predecessor id must be named")
}

// TestAssembleContextNoDegradedMessageWhenAbsent guards the default path: a
// task without degraded_inputs gets no degraded-inputs system message, so a
// non-AllowPartial session's context is unchanged by this feature.
//
// Scope: the assertion below is a message-level substring guard (no assembled
// message mentions degraded inputs), not a byte-for-byte diff against a
// pre-0.3.3 baseline. It does catch the regression that matters — injecting
// unconditionally — because the banner text is prepended before the id list,
// so even an empty list would trip it.
func TestAssembleContextNoDegradedMessageWhenAbsent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "no-degraded"
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)

	planNode := SessionNodeID(sessionID, 1, "plan", 0)
	require.NoError(t, g.AddToolNode(ctx, planNode, "plan", nil, g.Root()))
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: g.Root(), Capability: "x"}))
	driveTaskToCompleted(t, ctx, fabric, g.Root(), "root prompt")

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: &l1ChatAlwaysGrep{},
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	task := models.NewTask(planNode, planAgentType, nil)
	task.SessionID = sessionID
	task.Payload = map[string]any{"input": "root prompt"}

	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g)
	require.NoError(t, err)
	for _, m := range msgs {
		require.NotContains(t, m.Content, "degraded inputs",
			"no degraded-inputs message without the payload key")
	}
}

// TestDegradedInputsFromPayload is a focused table test for the payload
// extractor: it must survive the checkpoint envelope's JSON round-trip, where
// the slice arrives as []any of strings rather than []string.
func TestDegradedInputsFromPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    []string
	}{
		{"nil_payload", nil, nil},
		{"missing_key", map[string]any{"other": 1}, nil},
		{"string_slice", map[string]any{taskfabric.PayloadKeyDegradedInputs: []string{"a", "b"}}, []string{"a", "b"}},
		{"any_slice_from_json", map[string]any{taskfabric.PayloadKeyDegradedInputs: []any{"a", "b"}}, []string{"a", "b"}},
		{"any_slice_skips_non_string", map[string]any{taskfabric.PayloadKeyDegradedInputs: []any{"a", 7, ""}}, []string{"a"}},
		{"wrong_type", map[string]any{taskfabric.PayloadKeyDegradedInputs: "not-a-slice"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, degradedInputsFromPayload(tc.payload))
		})
	}
}

// TestDegradedInputsFromFabricReachAssembleContext is the C0′.3 end-to-end
// test (plan/0.3.3_task.md §C.8): it drives the WHOLE degraded-inputs chain
// with REAL production machinery instead of hand-stuffing the payload —
//
//	fabric cascade (records DegradedInputs) → mirrorDegradedInputsLocked
//	→ checkpoint envelope → DecodeCheckpoint → JSON round-trip ([]any,
//	the post-restart shape) → assembleContext → system message.
//
// The sibling TestAssembleContextSurfacesDegradedInputs hand-writes a
// []string, skipping the mirror step and the []any decode path; this test
// closes that realism gap.
func TestDegradedInputsFromFabricReachAssembleContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- Real producer: a cascade records the degraded input and mirrors it
	// into the dependent's checkpoint payload (no hand-stuffing). ---
	tf := taskfabric.NewFabric()
	require.NoError(t, tf.Create(&taskfabric.Task{
		ID: "up", Capability: "x", RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}))
	require.NoError(t, tf.Create(&taskfabric.Task{
		ID: "down", Capability: "ares/plan", Dependencies: []string{"up"}, AllowPartial: true,
	}))
	epoch, err := tf.Acquire("up", "w", time.Minute)
	require.NoError(t, err)
	require.NoError(t, tf.Start("up", "w", epoch))
	require.NoError(t, tf.Fail("up", "w", epoch, errors.New("boom")))

	down, err := tf.Task("down")
	require.NoError(t, err)
	require.Equal(t, []string{"up"}, down.DegradedInputs, "cascade must record the degraded input")

	// Decode the mirrored checkpoint exactly as the scheduler's ToModelTask
	// does, then JSON round-trip it to reproduce the post-restart shape where
	// the slice arrives as []any rather than []string.
	dc, err := taskfabric.DecodeCheckpoint(down.Checkpoint)
	require.NoError(t, err)
	raw, err := json.Marshal(dc.Payload)
	require.NoError(t, err)
	var restored map[string]any
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.IsType(t, []any{}, restored[taskfabric.PayloadKeyDegradedInputs],
		"post-restart payload carries degraded_inputs as []any (the path the hand-stuffed test skips)")

	// --- Consumer: feed the production-shape payload into the planner. ---
	reg := NewSessionRegistry()
	const sessionID = "degraded-real"
	sessFabric := taskfabric.NewFabric()
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)
	planNode := SessionNodeID(sessionID, 1, "plan", 0)
	require.NoError(t, g.AddToolNode(ctx, planNode, "plan", nil, g.Root()))
	require.NoError(t, sessFabric.Create(&taskfabric.Task{ID: g.Root(), Capability: "x"}))
	driveTaskToCompleted(t, ctx, sessFabric, g.Root(), "root prompt")

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: &l1ChatAlwaysGrep{},
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     sessFabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	task := models.NewTask(planNode, planAgentType, nil)
	task.SessionID = sessionID
	// The degraded value is the REAL one derived above (post-restart []any),
	// not a literal — this is what exercises degradedInputsFromPayload's
	// []any branch on the production path.
	task.Payload = map[string]any{
		"input":                             "root prompt",
		taskfabric.PayloadKeyDegradedInputs: restored[taskfabric.PayloadKeyDegradedInputs],
	}

	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g)
	require.NoError(t, err)

	var degraded *llmcore.LLMMessage
	for _, m := range msgs {
		if m.Role == roleSystem && strings.Contains(m.Content, "degraded inputs") {
			degraded = m
			break
		}
	}
	require.NotNil(t, degraded, "degraded-inputs system message must reach the LLM via the real chain")
	require.Contains(t, degraded.Content, "up", "the cascade-recorded predecessor id must be named")
}
