package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// ---------------------------------------------------------------------------
// Error variables
// ---------------------------------------------------------------------------

func TestErrorVariables(t *testing.T) {
	assert.NotNil(t, ErrInvalidConfig)
	assert.NotNil(t, ErrInvalidRequest)
	assert.NotNil(t, ErrInvalidWorkflow)
	assert.NotNil(t, ErrWorkflowExists)
	assert.NotNil(t, ErrWorkflowNotFound)

	assert.Equal(t, "invalid configuration", ErrInvalidConfig.Error())
	assert.Equal(t, "invalid request", ErrInvalidRequest.Error())
	assert.Equal(t, "invalid workflow definition", ErrInvalidWorkflow.Error())
	assert.Equal(t, "workflow already registered", ErrWorkflowExists.Error())
	assert.Equal(t, "workflow not found", ErrWorkflowNotFound.Error())
}

// ---------------------------------------------------------------------------
// NewService
// ---------------------------------------------------------------------------

func TestNewService_NilConfig(t *testing.T) {
	svc, err := NewService(nil)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

func TestNewService_NilAgentRegistry(t *testing.T) {
	svc, err := NewService(&Config{AgentRegistry: nil})
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

func TestNewService_ValidDefaults(t *testing.T) {
	reg := engine.NewAgentRegistry()
	svc, err := NewService(&Config{AgentRegistry: reg})
	require.NoError(t, err)
	require.NotNil(t, svc)

	assert.Equal(t, 5*time.Minute, svc.config.RequestTimeout)
	assert.Equal(t, 10, svc.config.MaxParallel)
	assert.Same(t, reg, svc.registry)
	assert.NotNil(t, svc.workflows)
	assert.Empty(t, svc.workflows)
}

func TestNewService_ExplicitValues(t *testing.T) {
	reg := engine.NewAgentRegistry()
	svc, err := NewService(&Config{
		AgentRegistry:  reg,
		RequestTimeout: 30 * time.Second,
		MaxParallel:    5,
	})
	require.NoError(t, err)
	require.NotNil(t, svc)

	assert.Equal(t, 30*time.Second, svc.config.RequestTimeout)
	assert.Equal(t, 5, svc.config.MaxParallel)
}

// ---------------------------------------------------------------------------
// RegisterWorkflow
// ---------------------------------------------------------------------------

func TestRegisterWorkflow_Nil(t *testing.T) {
	svc := mustNewService(t)
	err := svc.RegisterWorkflow(nil)
	assert.ErrorIs(t, err, ErrInvalidWorkflow)
}

func TestRegisterWorkflow_EmptyID(t *testing.T) {
	svc := mustNewService(t)
	err := svc.RegisterWorkflow(&core.WorkflowDefinition{ID: ""})
	assert.ErrorIs(t, err, ErrInvalidWorkflow)
}

func TestRegisterWorkflow_Duplicate(t *testing.T) {
	svc := mustNewService(t)
	def := &core.WorkflowDefinition{ID: "wf1", Name: "Workflow 1"}

	err := svc.RegisterWorkflow(def)
	require.NoError(t, err)

	err = svc.RegisterWorkflow(def)
	assert.ErrorIs(t, err, ErrWorkflowExists)
}

func TestRegisterWorkflow_Success(t *testing.T) {
	svc := mustNewService(t)
	def := &core.WorkflowDefinition{
		ID:          "wf-success",
		Name:        "Success Workflow",
		Description: "A test workflow",
		Version:     TestVersion,
		Steps: []*core.StepDef{
			{ID: "step1", Name: TestStepName, AgentType: "test_agent", Input: "hello"},
		},
		Variables: map[string]string{"key": "val"},
		Metadata:  map[string]string{"env": "test"},
	}

	err := svc.RegisterWorkflow(def)
	require.NoError(t, err)

	// Verify it is stored.
	retrieved, err := svc.GetWorkflow(context.Background(), "wf-success")
	require.NoError(t, err)
	assert.Equal(t, def.ID, retrieved.ID)
	assert.Equal(t, def.Name, retrieved.Name)
	assert.Equal(t, def.Description, retrieved.Description)
	assert.Equal(t, def.Version, retrieved.Version)
	assert.Equal(t, len(def.Steps), len(retrieved.Steps))
	assert.Equal(t, def.Variables, retrieved.Variables)
	assert.Equal(t, def.Metadata, retrieved.Metadata)
}

// ---------------------------------------------------------------------------
// GetWorkflow
// ---------------------------------------------------------------------------

func TestGetWorkflow_EmptyID(t *testing.T) {
	svc := mustNewService(t)
	def, err := svc.GetWorkflow(context.Background(), "")
	assert.Nil(t, def)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestGetWorkflow_NotFound(t *testing.T) {
	svc := mustNewService(t)
	def, err := svc.GetWorkflow(context.Background(), "nonexistent")
	assert.Nil(t, def)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
}

func TestGetWorkflow_Existing(t *testing.T) {
	svc := mustNewService(t)
	def := &core.WorkflowDefinition{ID: "get-test", Name: "Get Test"}
	require.NoError(t, svc.RegisterWorkflow(def))

	retrieved, err := svc.GetWorkflow(context.Background(), "get-test")
	require.NoError(t, err)
	assert.Equal(t, "get-test", retrieved.ID)
	assert.Equal(t, "Get Test", retrieved.Name)
}

// ---------------------------------------------------------------------------
// ListWorkflows
// ---------------------------------------------------------------------------

func TestListWorkflows_Empty(t *testing.T) {
	svc := mustNewService(t)
	summaries, err := svc.ListWorkflows(context.Background())
	require.NoError(t, err)
	assert.Empty(t, summaries)
}

func TestListWorkflows_WithWorkflows(t *testing.T) {
	svc := mustNewService(t)

	now := time.Now()
	def1 := &core.WorkflowDefinition{
		ID:          "wf-a",
		Name:        "Workflow A",
		Description: "First workflow",
		Steps:       []*core.StepDef{{ID: "s1"}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	def2 := &core.WorkflowDefinition{
		ID:          "wf-b",
		Name:        "Workflow B",
		Description: "Second workflow",
		Steps:       []*core.StepDef{{ID: "s1"}, {ID: "s2"}},
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now,
	}

	require.NoError(t, svc.RegisterWorkflow(def1))
	require.NoError(t, svc.RegisterWorkflow(def2))

	summaries, err := svc.ListWorkflows(context.Background())
	require.NoError(t, err)
	assert.Len(t, summaries, 2)

	// Build a lookup map.
	byID := make(map[string]*core.WorkflowSummary)
	for _, s := range summaries {
		byID[s.ID] = s
	}

	s1, ok := byID["wf-a"]
	require.True(t, ok)
	assert.Equal(t, "Workflow A", s1.Name)
	assert.Equal(t, "First workflow", s1.Description)
	assert.Equal(t, 1, s1.StepCount)
	assert.Equal(t, now.Unix(), s1.CreatedAt.Unix())
	assert.Equal(t, now.Unix(), s1.UpdatedAt.Unix())

	s2, ok := byID["wf-b"]
	require.True(t, ok)
	assert.Equal(t, "Workflow B", s2.Name)
	assert.Equal(t, "Second workflow", s2.Description)
	assert.Equal(t, 2, s2.StepCount)
}

// ---------------------------------------------------------------------------
// Execute — validation layer only (full execution requires engine runtime)
// ---------------------------------------------------------------------------

func TestExecute_NilRequest(t *testing.T) {
	svc := mustNewService(t)
	resp, err := svc.Execute(context.Background(), nil)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestExecute_EmptyWorkflowID(t *testing.T) {
	svc := mustNewService(t)
	resp, err := svc.Execute(context.Background(), &core.WorkflowRequest{WorkflowID: ""})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestExecute_WorkflowNotFound(t *testing.T) {
	svc := mustNewService(t)
	resp, err := svc.Execute(context.Background(), &core.WorkflowRequest{WorkflowID: "missing"})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
}

// ---------------------------------------------------------------------------
// ExecuteStream — validation layer only
// ---------------------------------------------------------------------------

func TestExecuteStream_NilRequest(t *testing.T) {
	svc := mustNewService(t)
	ch, err := svc.ExecuteStream(context.Background(), nil)
	assert.Nil(t, ch)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestExecuteStream_EmptyWorkflowID(t *testing.T) {
	svc := mustNewService(t)
	ch, err := svc.ExecuteStream(context.Background(), &core.WorkflowRequest{WorkflowID: ""})
	assert.Nil(t, ch)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestExecuteStream_WorkflowNotFound(t *testing.T) {
	svc := mustNewService(t)
	ch, err := svc.ExecuteStream(context.Background(), &core.WorkflowRequest{WorkflowID: "missing"})
	assert.Nil(t, ch)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
}

// ---------------------------------------------------------------------------
// Internal methods — buildEngineWorkflow / buildEngineSteps (indirect)
// ---------------------------------------------------------------------------

func TestBuildEngineRoundTrip(t *testing.T) {
	svc := mustNewService(t)

	def := &core.WorkflowDefinition{
		ID:      "roundtrip",
		Name:    "Round Trip",
		Version: "2.0",
		Steps: []*core.StepDef{
			{ID: "a", Name: "Step A", AgentType: "agent_a", Input: "in_a", DependsOn: nil, Timeout: time.Second},
			{ID: "b", Name: "Step B", AgentType: "agent_b", Input: "in_b", DependsOn: []string{"a"}, Timeout: 0},
		},
		Variables: map[string]string{"var1": "val1"},
		Metadata:  map[string]string{"key": "val"},
	}
	require.NoError(t, svc.RegisterWorkflow(def))

	// buildEngineWorkflow
	wf := svc.buildEngineWorkflow(def, map[string]string{"override": "ov"})
	require.NotNil(t, wf)
	assert.Equal(t, def.ID, wf.ID)
	assert.Equal(t, def.Name, wf.Name)
	assert.Equal(t, def.Version, wf.Version)
	assert.Equal(t, "val1", wf.Variables["var1"])
	assert.Equal(t, "ov", wf.Variables["override"])
	assert.Equal(t, def.Metadata, wf.Metadata)

	// buildEngineSteps
	require.Len(t, wf.Steps, 2)

	assert.Equal(t, "a", wf.Steps[0].ID)
	assert.Equal(t, "Step A", wf.Steps[0].Name)
	assert.Equal(t, "agent_a", wf.Steps[0].AgentType)
	assert.Equal(t, "in_a", wf.Steps[0].Input)
	assert.Empty(t, wf.Steps[0].DependsOn)
	assert.Equal(t, time.Second, wf.Steps[0].Timeout)

	assert.Equal(t, "b", wf.Steps[1].ID)
	assert.Equal(t, "Step B", wf.Steps[1].Name)
	assert.Equal(t, "agent_b", wf.Steps[1].AgentType)
	assert.Equal(t, "in_b", wf.Steps[1].Input)
	assert.Equal(t, []string{"a"}, wf.Steps[1].DependsOn)
	assert.Equal(t, time.Duration(0), wf.Steps[1].Timeout)
}

// ---------------------------------------------------------------------------
// mapEngineStatus
// ---------------------------------------------------------------------------

func TestMapEngineStatus_WorkflowStatus(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected core.WorkflowStatus
	}{
		{engine.WorkflowStatusPending, core.WorkflowStatusPending},
		{engine.WorkflowStatusRunning, core.WorkflowStatusRunning},
		{engine.WorkflowStatusCompleted, core.WorkflowStatusCompleted},
		{engine.WorkflowStatusFailed, core.WorkflowStatusFailed},
		{engine.WorkflowStatusCancelled, core.WorkflowStatusCancelled},
	}

	for _, tc := range tests {
		t.Run(string(tc.expected), func(t *testing.T) {
			result := mapEngineStatus(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMapEngineStatus_StepStatus(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected core.WorkflowStatus
	}{
		{engine.StepStatusPending, core.WorkflowStatusPending},
		{engine.StepStatusRunning, core.WorkflowStatusRunning},
		{engine.StepStatusCompleted, core.WorkflowStatusCompleted},
		{engine.StepStatusFailed, core.WorkflowStatusFailed},
		{engine.StepStatusSkipped, core.WorkflowStatusCancelled},
	}

	for _, tc := range tests {
		t.Run(string(tc.expected), func(t *testing.T) {
			result := mapEngineStatus(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMapEngineStatus_Unknown(t *testing.T) {
	// An unrecognized type should fall through to the default (Pending).
	result := mapEngineStatus("bogus_value")
	assert.Equal(t, core.WorkflowStatusPending, result)

	// An unknown workflow status value.
	result = mapEngineStatus(engine.WorkflowStatus("unknown"))
	assert.Equal(t, core.WorkflowStatusPending, result)

	// An unknown step status value.
	result = mapEngineStatus(engine.StepStatus("unknown"))
	assert.Equal(t, core.WorkflowStatusPending, result)

	// A completely unrelated type.
	type custom int
	result = mapEngineStatus(custom(42))
	assert.Equal(t, core.WorkflowStatusPending, result)
}

// ---------------------------------------------------------------------------
// Concurrency safety
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	svc := mustNewService(t)

	// Register one workflow upfront.
	require.NoError(t, svc.RegisterWorkflow(&core.WorkflowDefinition{ID: "base"}))

	done := make(chan struct{})
	const goroutines = 20

	for range goroutines {
		go func() {
			// Register (may fail with ErrWorkflowExists — that's fine)
			_ = svc.RegisterWorkflow(&core.WorkflowDefinition{ID: "base"})

			// GetWorkflow
			_, _ = svc.GetWorkflow(context.Background(), "base")
			_, _ = svc.GetWorkflow(context.Background(), "nonexistent")

			// ListWorkflows
			_, _ = svc.ListWorkflows(context.Background())
			done <- struct{}{}
		}()
	}

	for range goroutines {
		<-done
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustNewService(tb testing.TB) *Service {
	tb.Helper()
	reg := engine.NewAgentRegistry()
	svc, err := NewService(&Config{AgentRegistry: reg})
	require.NoError(tb, err)
	return svc
}
