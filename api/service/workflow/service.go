// Package workflow provides workflow orchestration service implementation.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/api/core"
	apiworkflow "github.com/Timwood0x10/ares/api/workflow"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/workflow"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// Service implements core.WorkflowService.
type Service struct {
	registry  *engine.AgentRegistry
	workflows map[string]*core.WorkflowDefinition
	mu        sync.RWMutex
	config    *Config
}

// Config represents service configuration.
type Config struct {
	// AgentRegistry is the agent type registry for step execution.
	// Use api/workflow.NewAgentRegistry() to create an empty registry,
	// then Register() custom agent factories.
	AgentRegistry *apiworkflow.AgentRegistry
	// RequestTimeout is the default workflow execution timeout.
	RequestTimeout time.Duration
	// MaxParallel is the maximum number of parallel steps.
	MaxParallel int
	// PluginBus is the optional plugin bus for workflow lifecycle hooks,
	// routing, checkpointing, and event emission. If nil, the executor
	// runs without plugins (backward compatible).
	PluginBus *ares_runtime.PluginBus
	// UseRunner is retained for source compatibility.
	//
	// Deprecated: all service execution paths use the unified Runner.
	UseRunner bool
	// CheckpointStore persists atomic Runner snapshots for crash recovery.
	CheckpointStore ares_runtime.CheckpointStore
}

// NewService creates a new workflow service instance.
// Args:
// config - service configuration.
// Returns new workflow service instance or error.
func NewService(config *Config) (*Service, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}
	if config.AgentRegistry == nil {
		return nil, ErrInvalidConfig
	}

	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Minute
	}
	if config.MaxParallel == 0 {
		config.MaxParallel = 10
	}

	return &Service{
		registry:  config.AgentRegistry,
		workflows: make(map[string]*core.WorkflowDefinition),
		config:    config,
	}, nil
}

// RegisterWorkflow registers a workflow definition for later execution.
// Args:
// def - the workflow definition to register.
// Returns error if the definition is invalid or already registered.
func (s *Service) RegisterWorkflow(def *core.WorkflowDefinition) error {
	if def == nil {
		return ErrInvalidWorkflow
	}
	if def.ID == "" {
		return ErrInvalidWorkflow
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.workflows[def.ID]; exists {
		return ErrWorkflowExists
	}

	s.workflows[def.ID] = def
	return nil
}

// Execute runs a workflow synchronously and returns the result.
// Args:
// ctx - operation context.
// req - execution request.
// Returns workflow response or error.
func (s *Service) Execute(ctx context.Context, req *core.WorkflowRequest) (*core.WorkflowResponse, error) {
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if req.WorkflowID == "" {
		return nil, ErrInvalidRequest
	}

	// Look up workflow definition.
	def, err := s.getWorkflowDef(req.WorkflowID)
	if err != nil {
		return nil, err
	}

	// Apply timeout.
	timeout := req.Timeout
	if timeout == 0 {
		timeout = s.config.RequestTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Build engine workflow from definition.
	wf := s.buildEngineWorkflow(def, req.Variables)

	return s.executeWithRunner(ctx, wf, req)
}

// executeWithRunner executes a workflow using the unified Runner.
func (s *Service) executeWithRunner(ctx context.Context, wf *engine.Workflow, req *core.WorkflowRequest) (*core.WorkflowResponse, error) {
	runner, bound, err := s.buildBoundRunner(wf, req)
	if err != nil {
		return nil, err
	}
	result, execErr := runner.ExecuteBound(ctx, bound)
	if execErr != nil {
		slog.ErrorContext(ctx, "runner execution failed", "workflow_id", wf.ID, "error", execErr)
		return s.buildRunnerErrorResponse(wf.ID, result, execErr), nil
	}
	return s.buildRunnerResponse(result), nil
}

func (s *Service) buildBoundRunner(wf *engine.Workflow, req *core.WorkflowRequest, extra ...workflow.RunnerOption) (*workflow.Runner, *workflow.BoundWorkflow, error) {
	compiled, err := workflow.CompileFromEngineWithBindings(wf)
	if err != nil {
		return nil, nil, fmt.Errorf("compile workflow: %w", err)
	}
	bound, err := workflow.BindCompiledWorkflow(compiled)
	if err != nil {
		return nil, nil, fmt.Errorf("bind workflow: %w", err)
	}
	executor, err := workflow.NewEngineNodeExecutor(s.registry, wf.Steps)
	if err != nil {
		return nil, nil, fmt.Errorf("build engine node executor: %w", err)
	}
	options := []workflow.RunnerOption{
		workflow.WithScheduleStrategy(workflow.ScheduleFIFO),
		workflow.WithInitialInput(req.Input),
		workflow.WithInitialVariables(req.Variables),
	}
	if s.config.PluginBus != nil {
		options = append(options, workflow.WithPluginBus(s.config.PluginBus))
	}
	if s.config.CheckpointStore != nil {
		options = append(options, workflow.WithCheckpointStore(s.config.CheckpointStore))
	}
	options = append(options, extra...)
	return workflow.NewRunner(executor, options...), bound, nil
}

// executeStreamWithRunner runs a workflow with the unified Runner and streams events.
func (s *Service) executeStreamWithRunner(ctx context.Context, req *core.WorkflowRequest, wf *engine.Workflow) (<-chan core.WorkflowEvent, error) {
	events := make(chan core.WorkflowEvent, 64)
	runner, bound, err := s.buildBoundRunner(wf, req, workflow.WithEventSink(&serviceRunnerEventSink{events: events}))
	if err != nil {
		return nil, err
	}
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		defer close(events)
		_, execErr := runner.ExecuteBound(groupCtx, bound)
		return execErr
	})
	return events, nil
}

// buildRunnerResponse converts a workflow.Result to a core.WorkflowResponse.
func (s *Service) buildRunnerResponse(result *workflow.Result) *core.WorkflowResponse {
	if result == nil {
		return &core.WorkflowResponse{Status: core.WorkflowStatusFailed}
	}

	stepResults := make([]*core.StepResult, 0, len(result.NodeStates))
	outputs := make(map[string]any, len(result.NodeStates))
	for _, ns := range result.NodeStates {
		output := runnerNodeOutput(ns.Output)
		stepResults = append(stepResults, &core.StepResult{
			StepID:   string(ns.ID),
			Status:   mapRunnerStatus(ns.Status),
			Output:   output,
			Error:    ns.Error,
			Duration: ns.FinishedAt.Sub(ns.StartedAt),
		})
		outputs[string(ns.ID)] = output
	}

	return &core.WorkflowResponse{
		ExecutionID: result.ExecutionID,
		WorkflowID:  result.SpecID,
		Status:      mapRunnerStatus(result.Status),
		Output:      outputs,
		Steps:       stepResults,
		Error:       result.Error,
		Duration:    result.Duration,
	}
}

func runnerNodeOutput(output map[string]any) string {
	if value, exists := output["output"]; exists {
		return fmt.Sprint(value)
	}
	return fmt.Sprint(output)
}

// buildRunnerErrorResponse builds a response for a failed runner execution.
func (s *Service) buildRunnerErrorResponse(workflowID string, result *workflow.Result, execErr error) *core.WorkflowResponse {
	resp := &core.WorkflowResponse{
		WorkflowID: workflowID,
		Status:     core.WorkflowStatusFailed,
		Error:      execErr.Error(),
	}
	if result != nil {
		resp.ExecutionID = result.ExecutionID
		resp.Duration = result.Duration
		resp.Steps = make([]*core.StepResult, 0, len(result.NodeStates))
		for _, ns := range result.NodeStates {
			resp.Steps = append(resp.Steps, &core.StepResult{
				StepID:   string(ns.ID),
				Status:   mapRunnerStatus(ns.Status),
				Error:    ns.Error,
				Duration: ns.FinishedAt.Sub(ns.StartedAt),
			})
		}
	}
	return resp
}

// mapRunnerStatus maps workflow.NodeStatus to core.WorkflowStatus.
func mapRunnerStatus(status workflow.NodeStatus) core.WorkflowStatus {
	switch status {
	case workflow.NodeStatusPending:
		return core.WorkflowStatusPending
	case workflow.NodeStatusReady, workflow.NodeStatusRunning:
		return core.WorkflowStatusRunning
	case workflow.NodeStatusCompleted:
		return core.WorkflowStatusCompleted
	case workflow.NodeStatusFailed:
		return core.WorkflowStatusFailed
	case workflow.NodeStatusCancelled:
		return core.WorkflowStatusCancelled
	case workflow.NodeStatusInterrupted:
		return core.WorkflowStatusPending
	case workflow.NodeStatusNotSelected, workflow.NodeStatusUnreachable, workflow.NodeStatusBlocked:
		return core.WorkflowStatusCancelled
	default:
		return core.WorkflowStatusPending
	}
}

// ExecuteStream runs a workflow and streams progress events.
// Args:
// ctx - operation context.
// req - execution request.
// Returns a channel of workflow events or error.
func (s *Service) ExecuteStream(ctx context.Context, req *core.WorkflowRequest) (<-chan core.WorkflowEvent, error) {
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if req.WorkflowID == "" {
		return nil, ErrInvalidRequest
	}

	def, err := s.getWorkflowDef(req.WorkflowID)
	if err != nil {
		return nil, err
	}

	wf := s.buildEngineWorkflow(def, req.Variables)
	return s.executeStreamWithRunner(ctx, req, wf)
}

// ListWorkflows returns all registered workflow definitions.
// Args:
// ctx - operation context.
// Returns workflow summaries or error.
func (s *Service) ListWorkflows(ctx context.Context) ([]*core.WorkflowSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	summaries := make([]*core.WorkflowSummary, 0, len(s.workflows))
	for _, def := range s.workflows {
		summaries = append(summaries, &core.WorkflowSummary{
			ID:          def.ID,
			Name:        def.Name,
			Description: def.Description,
			StepCount:   len(def.Steps),
			CreatedAt:   def.CreatedAt,
			UpdatedAt:   def.UpdatedAt,
		})
	}
	return summaries, nil
}

// GetWorkflow returns a workflow definition by ID.
// Args:
// ctx - operation context.
// id - workflow identifier.
// Returns the workflow definition or error.
func (s *Service) GetWorkflow(ctx context.Context, id string) (*core.WorkflowDefinition, error) {
	if id == "" {
		return nil, ErrInvalidRequest
	}
	return s.getWorkflowDef(id)
}

// getWorkflowDef retrieves a workflow definition from the internal registry.
func (s *Service) getWorkflowDef(id string) (*core.WorkflowDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	def, exists := s.workflows[id]
	if !exists {
		return nil, ErrWorkflowNotFound
	}
	return def, nil
}

// buildEngineWorkflow converts a WorkflowDefinition to an engine.Workflow.
func (s *Service) buildEngineWorkflow(def *core.WorkflowDefinition, overrides map[string]string) *engine.Workflow {
	variables := make(map[string]string)
	for k, v := range def.Variables {
		variables[k] = v
	}
	for k, v := range overrides {
		variables[k] = v
	}

	engineSteps := s.buildEngineSteps(def)

	return &engine.Workflow{
		ID:        def.ID,
		Name:      def.Name,
		Version:   def.Version,
		Steps:     engineSteps,
		Variables: variables,
		Metadata:  def.Metadata,
		CreatedAt: def.CreatedAt,
		UpdatedAt: def.UpdatedAt,
	}
}

// buildEngineSteps converts StepDef slice to engine.Step slice.
func (s *Service) buildEngineSteps(def *core.WorkflowDefinition) []*engine.Step {
	steps := make([]*engine.Step, len(def.Steps))
	for i, sd := range def.Steps {
		steps[i] = &engine.Step{
			ID:        sd.ID,
			Name:      sd.Name,
			AgentType: sd.AgentType,
			Input:     sd.Input,
			DependsOn: sd.DependsOn,
			Timeout:   sd.Timeout,
		}
	}
	return steps
}

// mapEngineStatus maps engine.WorkflowStatus or engine.StepStatus to core.WorkflowStatus.
//
// Deprecated: use mapRunnerStatus for the unified Runner path.
func mapEngineStatus(status interface{}) core.WorkflowStatus {
	switch v := status.(type) {
	case engine.WorkflowStatus:
		switch v {
		case engine.WorkflowStatusPending:
			return core.WorkflowStatusPending
		case engine.WorkflowStatusRunning:
			return core.WorkflowStatusRunning
		case engine.WorkflowStatusCompleted:
			return core.WorkflowStatusCompleted
		case engine.WorkflowStatusFailed:
			return core.WorkflowStatusFailed
		case engine.WorkflowStatusCancelled:
			return core.WorkflowStatusCancelled
		}
	case engine.StepStatus:
		switch v {
		case engine.StepStatusPending:
			return core.WorkflowStatusPending
		case engine.StepStatusRunning:
			return core.WorkflowStatusRunning
		case engine.StepStatusCompleted:
			return core.WorkflowStatusCompleted
		case engine.StepStatusFailed:
			return core.WorkflowStatusFailed
		case engine.StepStatusSkipped:
			return core.WorkflowStatusCancelled
		}
	}
	return core.WorkflowStatusPending
}

// Service errors.
var (
	ErrInvalidConfig    = errors.New("invalid configuration")
	ErrInvalidRequest   = errors.New("invalid request")
	ErrInvalidWorkflow  = errors.New("invalid workflow definition")
	ErrWorkflowExists   = errors.New("workflow already registered")
	ErrWorkflowNotFound = errors.New("workflow not found")
)
