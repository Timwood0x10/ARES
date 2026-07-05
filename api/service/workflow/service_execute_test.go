package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// ---------------------------------------------------------------------------
// Test constants
// ---------------------------------------------------------------------------

const (
	// TestAgentType is the agent type used for successful test agents.
	TestAgentType = "test-agent"
	// FailingAgentType is the agent type used for failing test agents.
	FailingAgentType = "failing-agent"
	// TestVersion is the workflow version used in tests.
	TestVersion = "1.0"
	// TestStepName is the step name used in tests.
	TestStepName = "Step 1"
	// TestStartInput is the input value used for workflow start.
	TestStartInput = "start"
	// TestDataInput is the input data value used in tests.
	TestDataInput = "data"
)

// ---------------------------------------------------------------------------
// Mock agent for end-to-end workflow tests
// ---------------------------------------------------------------------------

// mockWorkflowAgent implements base.Agent for workflow execution tests.
// It returns a predetermined *models.RecommendResult from Process, which is
// the type AgentExecutor.Execute expects. When err is non-nil, Process
// returns the error instead, simulating a failing agent.
type mockWorkflowAgent struct {
	id        string
	agentType string
	result    *models.RecommendResult
	err       error
}

func (m *mockWorkflowAgent) ID() string                      { return m.id }
func (m *mockWorkflowAgent) Type() models.AgentType          { return models.AgentType(m.agentType) }
func (m *mockWorkflowAgent) Status() models.AgentStatus      { return models.AgentStatusReady }
func (m *mockWorkflowAgent) Start(ctx context.Context) error { return nil }
func (m *mockWorkflowAgent) Stop(ctx context.Context) error  { return nil }

// Process returns the predetermined result or error.
func (m *mockWorkflowAgent) Process(ctx context.Context, input any) (any, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

// ProcessStream wraps Process into a single-event channel.
func (m *mockWorkflowAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := m.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

// newSuccessAgentFactory returns an AgentFactory whose agents produce a
// RecommendResult with one item whose Description equals the given output.
func newSuccessAgentFactory(output string) engine.AgentFactory {
	return func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &mockWorkflowAgent{
			id:        "mock-success",
			agentType: TestAgentType,
			result: &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item-1", Description: output},
				},
			},
		}, nil
	}
}

// newFailingAgentFactory returns an AgentFactory whose agents return err
// from Process.
func newFailingAgentFactory(err error) engine.AgentFactory {
	return func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &mockWorkflowAgent{
			id:        "mock-fail",
			agentType: FailingAgentType,
			err:       err,
		}, nil
	}
}

// newTestServiceWithRegistry creates a Service with a fresh AgentRegistry
// and returns both so tests can register agent factories.
func newTestServiceWithRegistry(t *testing.T) (*Service, *engine.AgentRegistry) {
	t.Helper()
	reg := engine.NewAgentRegistry()
	svc, err := NewService(&Config{
		AgentRegistry:  reg,
		RequestTimeout: 30 * time.Second,
		MaxParallel:    5,
	})
	require.NoError(t, err)
	return svc, reg
}

// ---------------------------------------------------------------------------
// Execute — full execution path with registered agents
// ---------------------------------------------------------------------------

// TestExecute_SingleStep_Success verifies the full Execute path: a workflow
// with one step backed by a mock agent completes successfully and the step
// output matches the agent's result.
func TestExecute_SingleStep_Success(t *testing.T) {
	svc, reg := newTestServiceWithRegistry(t)
	if err := reg.Register(TestAgentType, newSuccessAgentFactory("hello-from-agent")); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	def := &core.WorkflowDefinition{
		ID:      "wf-single",
		Name:    "Single Step Workflow",
		Version: TestVersion,
		Steps: []*core.StepDef{
			{ID: "step1", Name: TestStepName, AgentType: TestAgentType, Input: "input-data"},
		},
	}
	require.NoError(t, svc.RegisterWorkflow(def))

	resp, err := svc.Execute(context.Background(), &core.WorkflowRequest{
		WorkflowID: "wf-single",
		Input:      "request-input",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, core.WorkflowStatusCompleted, resp.Status)
	assert.NotEmpty(t, resp.ExecutionID)
	assert.Empty(t, resp.Error)
	assert.Len(t, resp.Steps, 1)

	step := resp.Steps[0]
	assert.Equal(t, "step1", step.StepID)
	assert.Equal(t, core.WorkflowStatusCompleted, step.Status)
	assert.Equal(t, "hello-from-agent", step.Output)
	assert.Empty(t, step.Error)
	assert.Greater(t, step.Duration, time.Duration(0))

	// The workflow-level output map should contain the step output.
	assert.Contains(t, resp.Output, "step1")
}

// TestExecute_MultiStep_SequentialSuccess verifies that a workflow with two
// sequential steps (step2 depends on step1) executes both in order and both
// complete successfully.
func TestExecute_MultiStep_SequentialSuccess(t *testing.T) {
	svc, reg := newTestServiceWithRegistry(t)
	if err := reg.Register("agent-a", newSuccessAgentFactory("output-a")); err != nil {
		t.Fatalf("register agent-a: %v", err)
	}
	if err := reg.Register("agent-b", newSuccessAgentFactory("output-b")); err != nil {
		t.Fatalf("register agent-b: %v", err)
	}

	def := &core.WorkflowDefinition{
		ID:      "wf-multi",
		Name:    "Multi Step Workflow",
		Version: TestVersion,
		Steps: []*core.StepDef{
			{ID: "step1", Name: "First Step", AgentType: "agent-a", Input: "initial"},
			{ID: "step2", Name: "Second Step", AgentType: "agent-b", DependsOn: []string{"step1"}},
		},
	}
	require.NoError(t, svc.RegisterWorkflow(def))

	resp, err := svc.Execute(context.Background(), &core.WorkflowRequest{
		WorkflowID: "wf-multi",
		Input:      TestStartInput,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, core.WorkflowStatusCompleted, resp.Status)
	assert.Len(t, resp.Steps, 2)

	// Build a lookup by step ID since result order may vary.
	byID := make(map[string]*core.StepResult, len(resp.Steps))
	for _, s := range resp.Steps {
		byID[s.StepID] = s
	}

	s1, ok := byID["step1"]
	require.True(t, ok)
	assert.Equal(t, core.WorkflowStatusCompleted, s1.Status)
	assert.Equal(t, "output-a", s1.Output)

	s2, ok := byID["step2"]
	require.True(t, ok)
	assert.Equal(t, core.WorkflowStatusCompleted, s2.Status)
	assert.Equal(t, "output-b", s2.Output)
}

// TestExecute_StepFailure_ReturnsFailedResponse verifies that when a step's
// agent returns an error, Execute returns a response (not a Go error) with
// Failed status and the error message is propagated.
func TestExecute_StepFailure_ReturnsFailedResponse(t *testing.T) {
	svc, reg := newTestServiceWithRegistry(t)
	agentErr := errors.New("agent processing failed")
	if err := reg.Register(FailingAgentType, newFailingAgentFactory(agentErr)); err != nil {
		t.Fatalf("register failing agent: %v", err)
	}

	def := &core.WorkflowDefinition{
		ID:      "wf-fail",
		Name:    "Failing Workflow",
		Version: TestVersion,
		Steps: []*core.StepDef{
			{ID: "step1", Name: TestStepName, AgentType: FailingAgentType, Input: TestDataInput},
		},
	}
	require.NoError(t, svc.RegisterWorkflow(def))

	resp, err := svc.Execute(context.Background(), &core.WorkflowRequest{
		WorkflowID: "wf-fail",
		Input:      TestStartInput,
	})
	// Execute returns nil error even on step failure; the error is in the response.
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, core.WorkflowStatusFailed, resp.Status)
	assert.Contains(t, resp.Error, "agent processing failed")
	assert.Len(t, resp.Steps, 1)

	step := resp.Steps[0]
	assert.Equal(t, "step1", step.StepID)
	assert.Equal(t, core.WorkflowStatusFailed, step.Status)
	assert.Contains(t, step.Error, "agent processing failed")
}

// ---------------------------------------------------------------------------
// ExecuteStream — streaming execution with event draining
// ---------------------------------------------------------------------------

// TestExecuteStream_DrainsEvents_TerminalCompleted verifies that
// ExecuteStream emits events that can be fully drained from the channel,
// and the terminal event is WorkflowEventCompleted for a successful workflow.
func TestExecuteStream_DrainsEvents_TerminalCompleted(t *testing.T) {
	svc, reg := newTestServiceWithRegistry(t)
	if err := reg.Register(TestAgentType, newSuccessAgentFactory("stream-output")); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	def := &core.WorkflowDefinition{
		ID:      "wf-stream-ok",
		Name:    "Stream Success Workflow",
		Version: TestVersion,
		Steps: []*core.StepDef{
			{ID: "step1", Name: TestStepName, AgentType: TestAgentType, Input: TestDataInput},
		},
	}
	require.NoError(t, svc.RegisterWorkflow(def))

	ch, err := svc.ExecuteStream(context.Background(), &core.WorkflowRequest{
		WorkflowID: "wf-stream-ok",
		Input:      TestStartInput,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Drain all events until the channel is closed.
	var events []core.WorkflowEvent
eventLoop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break eventLoop
			}
			events = append(events, ev)
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for events to drain")
		}
	}

	// At minimum we expect a Started event and a Completed terminal event.
	require.GreaterOrEqual(t, len(events), 2, "expected at least started + completed events")

	// First event should be Started.
	assert.Equal(t, core.WorkflowEventStarted, events[0].Type)
	assert.Equal(t, "wf-stream-ok", events[0].WorkflowID)

	// Terminal event should be Completed.
	terminal := events[len(events)-1]
	assert.Equal(t, core.WorkflowEventCompleted, terminal.Type)
	assert.Equal(t, core.WorkflowStatusCompleted, terminal.Status)
	assert.Equal(t, "wf-stream-ok", terminal.WorkflowID)
	assert.NotEmpty(t, terminal.ExecutionID)

	// Verify at least one StepCompleted event was emitted.
	var stepCompletedCount int
	for _, ev := range events {
		if ev.Type == core.WorkflowEventStepCompleted {
			stepCompletedCount++
			assert.Equal(t, "step1", ev.StepID)
			assert.Equal(t, "stream-output", ev.Output)
		}
	}
	assert.GreaterOrEqual(t, stepCompletedCount, 1, "expected at least one StepCompleted event")
}

// TestExecuteStream_StepFailure_TerminalFailed verifies that when a step
// fails during streaming execution, the terminal event is WorkflowEventFailed
// and the error message is propagated in the event.
func TestExecuteStream_StepFailure_TerminalFailed(t *testing.T) {
	svc, reg := newTestServiceWithRegistry(t)
	agentErr := errors.New("stream agent error")
	if err := reg.Register(FailingAgentType, newFailingAgentFactory(agentErr)); err != nil {
		t.Fatalf("register failing agent: %v", err)
	}

	def := &core.WorkflowDefinition{
		ID:      "wf-stream-fail",
		Name:    "Stream Fail Workflow",
		Version: TestVersion,
		Steps: []*core.StepDef{
			{ID: "step1", Name: TestStepName, AgentType: FailingAgentType, Input: TestDataInput},
		},
	}
	require.NoError(t, svc.RegisterWorkflow(def))

	ch, err := svc.ExecuteStream(context.Background(), &core.WorkflowRequest{
		WorkflowID: "wf-stream-fail",
		Input:      TestStartInput,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Drain all events until the channel is closed.
	var events []core.WorkflowEvent
eventLoop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break eventLoop
			}
			events = append(events, ev)
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for events to drain")
		}
	}

	// At minimum we expect a Started event and a Failed terminal event.
	require.GreaterOrEqual(t, len(events), 2, "expected at least started + failed events")

	// First event should be Started.
	assert.Equal(t, core.WorkflowEventStarted, events[0].Type)

	// Terminal event should be Failed.
	terminal := events[len(events)-1]
	assert.Equal(t, core.WorkflowEventFailed, terminal.Type)
	assert.Equal(t, core.WorkflowStatusFailed, terminal.Status)
	assert.Contains(t, terminal.Error, "stream agent error")

	// When ExecuteDynamic returns an error (step failure), ExecuteStream
	// takes the error branch and emits WorkflowEventFailed directly without
	// per-step events. Verify the error message is propagated.
	assert.Contains(t, terminal.Error, "step1", "error should reference the failed step ID")
}
