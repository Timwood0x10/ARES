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
	// UseRunner switches execution to the unified Runner (P2).
	// When true, the service uses workflow.Runner instead of the legacy
	// engine.DynamicExecutor. The Runner path is now the default; switch to
	// false to use the legacy executor for migration only.
	UseRunner bool
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

	// Route to the appropriate executor.
	if s.config.UseRunner {
		return s.executeWithRunner(ctx, wf, req)
	}

	// Build steps for MutableDAG.
	steps := s.buildEngineSteps(def)

	mutableDAG, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("create mutable DAG: %w", err)
	}

	// Create executor and run.
	executor := engine.NewDynamicExecutor(
		s.registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(s.config.MaxParallel),
	)
	if s.config.PluginBus != nil {
		executor.WithPluginBus(s.config.PluginBus)
	}

	result, err := executor.ExecuteDynamic(ctx, wf, req.Input, mutableDAG)
	if err != nil {
		slog.ErrorContext(ctx, "workflow execution failed",
			"workflow_id", req.WorkflowID,
			"error", err)
		return s.buildErrorResponse(req.WorkflowID, result, err), nil
	}

	return s.buildResponse(result), nil
}

// executeWithRunner executes a workflow using the unified Runner.
func (s *Service) executeWithRunner(ctx context.Context, wf *engine.Workflow, req *core.WorkflowRequest) (*core.WorkflowResponse, error) {
	// Compile engine workflow to WorkflowSpec IR.
	spec, err := workflow.CompileFromEngine(wf) //nolint:staticcheck // migration adapter — deprecated by design for legacy compat
	if err != nil {
		return nil, fmt.Errorf("compile workflow: %w", err)
	}

	// Create node executor with agent registry bindings.
	exec := workflow.NewFuncNodeExecutor()
	for _, step := range wf.Steps {
		sid := step.ID
		agentType := step.AgentType
		exec.Register(workflow.NodeID(sid), func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			agent, aErr := s.registry.CreateAgent(ctx, agentType, nil)
			if aErr != nil {
				return nil, fmt.Errorf("create agent %q: %w", agentType, aErr)
			}
			input, _ := view.Get("input")
			result, aErr := agent.Process(ctx, input)
			if aErr != nil {
				return nil, fmt.Errorf("agent %q: %w", agentType, aErr)
			}
			output := map[string]any{"output": result}
			return output, nil
		})
	}

	// Create and run the Runner.
	runner := workflow.NewRunner(exec, workflow.WithScheduleStrategy(workflow.ScheduleFIFO))
	result, rErr := runner.Execute(ctx, spec)
	if rErr != nil {
		slog.ErrorContext(ctx, "runner execution failed",
			"workflow_id", wf.ID,
			"error", rErr)
		return s.buildRunnerErrorResponse(wf.ID, result, rErr), nil
	}

	return s.buildRunnerResponse(result), nil
}

// buildRunnerResponse converts a workflow.Result to a core.WorkflowResponse.
func (s *Service) buildRunnerResponse(result *workflow.Result) *core.WorkflowResponse {
	if result == nil {
		return &core.WorkflowResponse{Status: core.WorkflowStatusFailed}
	}

	stepResults := make([]*core.StepResult, 0, len(result.NodeStates))
	for _, ns := range result.NodeStates {
		stepResults = append(stepResults, &core.StepResult{
			StepID:   string(ns.ID),
			Status:   mapRunnerStatus(ns.Status),
			Output:   fmt.Sprintf("%v", ns.Output),
			Error:    ns.Error,
			Duration: ns.FinishedAt.Sub(ns.StartedAt),
		})
	}

	return &core.WorkflowResponse{
		ExecutionID: result.ExecutionID,
		WorkflowID:  result.SpecID,
		Status:      mapRunnerStatus(result.Status),
		Output:      result.State,
		Steps:       stepResults,
		Error:       result.Error,
		Duration:    result.Duration,
	}
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
	steps := s.buildEngineSteps(def)

	mutableDAG, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("create mutable DAG: %w", err)
	}

	executor := engine.NewDynamicExecutor(
		s.registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(s.config.MaxParallel),
	)
	if s.config.PluginBus != nil {
		executor.WithPluginBus(s.config.PluginBus)
	}

	events := make(chan core.WorkflowEvent, 64)

	go func() {
		// Apply timeout inside the goroutine so cancel does not fire
		// when ExecuteStream returns, which would prematurely cancel
		// the running workflow.
		execCtx := ctx
		timeout := req.Timeout
		if timeout == 0 {
			timeout = s.config.RequestTimeout
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		defer close(events)

		// Emit workflow started event.
		events <- core.WorkflowEvent{
			Type:        core.WorkflowEventStarted,
			ExecutionID: "",
			WorkflowID:  req.WorkflowID,
			Status:      core.WorkflowStatusRunning,
			Timestamp:   time.Now(),
		}

		// Subscribe to graph mutation events for step tracking.
		// Use SubscribeWithID so we can Unsubscribe (closing the channel)
		// after execution completes, which unblocks the event-forwarding goroutine.
		graphSubID, graphEvents := mutableDAG.SubscribeWithID()

		// Run execution and event forwarding via errgroup.
		type execResult struct {
			result *engine.WorkflowResult
			err    error
		}
		resultCh := make(chan execResult, 1)

		g, gctx := errgroup.WithContext(execCtx)

		// Goroutine 1: run execution.
		g.Go(func() error {
			r, e := executor.ExecuteDynamic(gctx, wf, req.Input, mutableDAG)
			resultCh <- execResult{result: r, err: e}
			return nil
		})

		// Goroutine 2: forward graph events as step events.
		g.Go(func() error {
			for ev := range graphEvents {
				if ev.Success && ev.Change.Step != nil {
					select {
					case events <- core.WorkflowEvent{
						Type:       core.WorkflowEventStepStarted,
						WorkflowID: req.WorkflowID,
						StepID:     ev.Change.NodeID,
						StepName:   ev.Change.Step.Name,
						Status:     core.WorkflowStatusRunning,
						Timestamp:  ev.Change.Timestamp,
					}:
					case <-gctx.Done():
						return nil
					}
				}
			}
			return nil
		})

		// Wait for execution to complete.
		res := <-resultCh
		// Unsubscribe to close the graph event channel, which unblocks
		// the event-forwarding goroutine and allows g.Wait() to return.
		mutableDAG.Unsubscribe(graphSubID)
		if err := g.Wait(); err != nil {
			fmt.Printf("workflow: executor wait: %v\n", err)
		}

		if res.err != nil || res.result == nil {
			errMsg := ""
			if res.err != nil {
				errMsg = res.err.Error()
			}
			// gctx is already cancelled after g.Wait() — use execCtx for cancellation check
			// so the Failed event can still be emitted when execution completes normally
			// but the result is an error.
			select {
			case events <- core.WorkflowEvent{
				Type:       core.WorkflowEventFailed,
				WorkflowID: req.WorkflowID,
				Status:     core.WorkflowStatusFailed,
				Error:      errMsg,
				Timestamp:  time.Now(),
			}:
			case <-execCtx.Done():
				return
			}
			return
		}

		// Emit step completion events from results.
		for _, stepRes := range res.result.Steps {
			evType := core.WorkflowEventStepCompleted
			status := core.WorkflowStatusCompleted
			if stepRes.Status == engine.StepStatusFailed {
				evType = core.WorkflowEventStepFailed
				status = core.WorkflowStatusFailed
			}
			select {
			case events <- core.WorkflowEvent{
				Type:        evType,
				ExecutionID: res.result.ExecutionID,
				WorkflowID:  req.WorkflowID,
				StepID:      stepRes.StepID,
				StepName:    stepRes.Name,
				Status:      status,
				Output:      stepRes.Output,
				Error:       stepRes.Error,
				Timestamp:   time.Now(),
			}:
			case <-execCtx.Done():
				return
			}
		}

		// Emit workflow completed event.
		select {
		case events <- core.WorkflowEvent{
			Type:        core.WorkflowEventCompleted,
			ExecutionID: res.result.ExecutionID,
			WorkflowID:  req.WorkflowID,
			Status:      core.WorkflowStatusCompleted,
			Timestamp:   time.Now(),
		}:
		case <-execCtx.Done():
			return
		}
	}()

	return events, nil
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

// buildResponse converts an engine.WorkflowResult to a core.WorkflowResponse.
func (s *Service) buildResponse(result *engine.WorkflowResult) *core.WorkflowResponse {
	status := mapEngineStatus(result.Status)

	stepResults := make([]*core.StepResult, len(result.Steps))
	for i, sr := range result.Steps {
		stepResults[i] = &core.StepResult{
			StepID:   sr.StepID,
			Name:     sr.Name,
			Status:   mapEngineStatus(sr.Status),
			Output:   sr.Output,
			Error:    sr.Error,
			Duration: sr.Duration,
		}
	}

	return &core.WorkflowResponse{
		ExecutionID: result.ExecutionID,
		WorkflowID:  result.WorkflowID,
		Status:      status,
		Output:      result.Output,
		Steps:       stepResults,
		Error:       result.Error,
		Duration:    result.Duration,
	}
}

// buildErrorResponse builds a response for a failed execution.
func (s *Service) buildErrorResponse(workflowID string, result *engine.WorkflowResult, execErr error) *core.WorkflowResponse {
	resp := &core.WorkflowResponse{
		WorkflowID: workflowID,
		Status:     core.WorkflowStatusFailed,
		Error:      execErr.Error(),
	}
	if result != nil {
		resp.ExecutionID = result.ExecutionID
		resp.Duration = result.Duration
		resp.Steps = make([]*core.StepResult, len(result.Steps))
		for i, sr := range result.Steps {
			resp.Steps[i] = &core.StepResult{
				StepID:   sr.StepID,
				Name:     sr.Name,
				Status:   mapEngineStatus(sr.Status),
				Output:   sr.Output,
				Error:    sr.Error,
				Duration: sr.Duration,
			}
		}
	}
	return resp
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
