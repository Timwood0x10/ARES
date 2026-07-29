// Package client provides workflow orchestration functionality.
package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/agents/base"
	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
	gerr "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/workflow"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// WorkflowClient provides workflow orchestration capabilities.
type WorkflowClient struct {
	client   *Client
	loader   *engine.FileLoader
	registry *engine.AgentRegistry
}

// NewWorkflowClient creates a new workflow client.
// Args:
// client - underlying ARES client.
// Returns workflow client or error.
func NewWorkflowClient(client *Client) (*WorkflowClient, error) {
	loader := engine.NewYAMLFileLoader()

	registry := engine.NewAgentRegistry()

	return &WorkflowClient{
		client:   client,
		loader:   loader,
		registry: registry,
	}, nil
}

// LoadWorkflow loads a workflow from a YAML file.
// Args:
// ctx - operation context.
// path - path to workflow YAML file.
// Returns loaded workflow or error.
func (w *WorkflowClient) LoadWorkflow(ctx context.Context, path string) (*engine.Workflow, error) {
	return w.loader.Load(ctx, path)
}

// Execute executes a legacy workflow definition through the unified Runner.
// Args:
// ctx - operation context.
// workflowDef - workflow definition.
// input - initial input data.
// Returns workflow result or error.
func (w *WorkflowClient) Execute(
	ctx context.Context,
	workflowDef *engine.Workflow,
	input string,
) (*engine.WorkflowResult, error) {
	if workflowDef == nil {
		return nil, fmt.Errorf("workflow definition must not be nil")
	}
	if w.client.configFile != nil {
		w.registerAgents()
	}
	compiled, err := workflow.CompileFromEngineWithBindings(workflowDef)
	if err != nil {
		return nil, fmt.Errorf("compile workflow %q: %w", workflowDef.ID, err)
	}
	bound, err := workflow.BindCompiledWorkflow(compiled)
	if err != nil {
		return nil, fmt.Errorf("bind workflow %q: %w", workflowDef.ID, err)
	}
	executor, err := workflow.NewEngineNodeExecutor(w.registry, workflowDef.Steps)
	if err != nil {
		return nil, fmt.Errorf("build workflow %q node executor: %w", workflowDef.ID, err)
	}
	runner := workflow.NewRunner(
		executor,
		workflow.WithInitialInput(input),
		workflow.WithInitialVariables(workflowDef.Variables),
	)
	result, execErr := runner.ExecuteBound(ctx, bound)
	return convertRunnerWorkflowResult(workflowDef, result), execErr
}

// ExecuteFromFile loads and executes a workflow from a file.
// Args:
// ctx - operation context.
// path - path to workflow YAML file.
// input - initial input data.
// Returns workflow result or error.
func (w *WorkflowClient) ExecuteFromFile(ctx context.Context, path, input string) (*engine.WorkflowResult, error) {
	workflow, err := w.LoadWorkflow(ctx, path)
	if err != nil {
		return nil, gerr.Wrap(err, "load workflow")
	}

	return w.Execute(ctx, workflow, input)
}

func convertRunnerWorkflowResult(
	workflowDef *engine.Workflow,
	result *workflow.Result,
) *engine.WorkflowResult {
	if result == nil {
		return nil
	}
	stepDefinitions := make(map[string]*engine.Step, len(workflowDef.Steps))
	for _, step := range workflowDef.Steps {
		if step != nil {
			stepDefinitions[step.ID] = step
		}
	}
	stepResults := make([]*engine.StepResult, 0, len(result.NodeStates))
	for _, nodeState := range result.NodeStates {
		definition := stepDefinitions[string(nodeState.ID)]
		stepResults = append(stepResults, convertRunnerStepResult(definition, nodeState))
	}
	outputs := make(map[string]interface{}, len(stepResults))
	for _, stepResult := range stepResults {
		outputs[stepResult.StepID] = stepResult.Output
	}
	return &engine.WorkflowResult{
		ExecutionID: result.ExecutionID,
		WorkflowID:  result.SpecID,
		Status:      runnerWorkflowStatus(result.Status),
		Output:      outputs,
		Error:       result.Error,
		Duration:    result.Duration,
		Steps:       stepResults,
	}
}

func convertRunnerStepResult(
	definition *engine.Step,
	state *workflow.NodeStatusValue,
) *engine.StepResult {
	result := &engine.StepResult{
		StepID:   string(state.ID),
		Status:   runnerStepStatus(state.Status),
		Output:   runnerNodeOutput(state.Output),
		Error:    state.Error,
		Duration: state.FinishedAt.Sub(state.StartedAt),
	}
	if definition != nil {
		result.Name = definition.Name
		result.Metadata = definition.Metadata
	}
	return result
}

func runnerNodeOutput(output map[string]any) string {
	value, exists := output["output"]
	if !exists {
		return ""
	}
	return fmt.Sprint(value)
}

func runnerWorkflowStatus(status workflow.NodeStatus) engine.WorkflowStatus {
	switch status {
	case workflow.NodeStatusCompleted:
		return engine.WorkflowStatusCompleted
	case workflow.NodeStatusCancelled:
		return engine.WorkflowStatusCancelled
	case workflow.NodeStatusFailed:
		return engine.WorkflowStatusFailed
	default:
		return engine.WorkflowStatusFailed
	}
}

func runnerStepStatus(status workflow.NodeStatus) engine.StepStatus {
	switch status {
	case workflow.NodeStatusCompleted:
		return engine.StepStatusCompleted
	case workflow.NodeStatusNotSelected, workflow.NodeStatusUnreachable:
		return engine.StepStatusSkipped
	case workflow.NodeStatusPending, workflow.NodeStatusReady:
		return engine.StepStatusPending
	case workflow.NodeStatusRunning:
		return engine.StepStatusRunning
	default:
		return engine.StepStatusFailed
	}
}

// registerAgents registers agents from client configuration.
func (w *WorkflowClient) registerAgents() {
	if w.client.configFile == nil {
		return
	}

	for _, agentConfig := range w.client.configFile.Agents.Sub {
		agentConfig := agentConfig
		if _, exists := w.registry.GetFactory(agentConfig.Type); exists {
			continue
		}
		err := w.registry.Register(agentConfig.Type, func(ctx context.Context, config interface{}) (base.Agent, error) {
			return &WorkflowAgentExecutor{
				agentID:    agentConfig.ID,
				agentName:  agentConfig.Name,
				agentType:  agentConfig.Type,
				category:   agentConfig.Category,
				llmService: w.client.llmService,
				prompts:    &w.client.configFile.Prompts,
				timeout:    time.Duration(agentConfig.Timeout) * time.Second,
				maxRetries: agentConfig.MaxRetries,
			}, nil
		})
		if err != nil {
			continue
		}
	}
}

// WorkflowAgentExecutor executes workflow steps using LLM service.

type WorkflowAgentExecutor struct {
	agentID string

	agentName string

	agentType string

	llmService core.LLMService

	prompts *PromptsConfig

	timeout time.Duration

	maxRetries int

	category string

	mu      sync.RWMutex
	started bool
}

// ID returns the agent ID.
func (e *WorkflowAgentExecutor) ID() string {
	return e.agentID
}

// Type returns the agent type.
func (e *WorkflowAgentExecutor) Type() models.AgentType {
	return models.AgentType(e.agentType)
}

// Status returns the agent status.

func (e *WorkflowAgentExecutor) Status() models.AgentStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.started {
		return models.AgentStatusReady
	}
	return models.AgentStatusOffline
}

// Start starts the agent.
func (e *WorkflowAgentExecutor) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return coreerrors.ErrAgentAlreadyStarted
	}
	if e.llmService == nil {
		return fmt.Errorf("llmService is not configured for agent %s", e.agentID)
	}
	e.started = true
	return nil
}

// Stop stops the agent.
func (e *WorkflowAgentExecutor) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return coreerrors.ErrAgentNotRunning
	}
	e.started = false
	return nil
}

// Process executes a workflow step.

func (e *WorkflowAgentExecutor) Process(ctx context.Context, input any) (any, error) {
	if e.llmService == nil {
		return nil, fmt.Errorf("llmService is not configured for agent %s", e.agentID)
	}

	inputStr, ok := input.(string)
	if !ok {
		return nil, fmt.Errorf("input must be string")
	}

	var prompt string
	if e.prompts != nil {
		if e.prompts.Recommendation != "" {
			prompt = e.prompts.Recommendation
			if e.category != "" {
				prompt = strings.ReplaceAll(prompt, "{{.category}}", e.category)
			}
			prompt = strings.ReplaceAll(prompt, "{{.input}}", inputStr)
			prompt = strings.ReplaceAll(prompt, "{{.requirements}}", inputStr)
		} else if e.prompts.ProfileExtraction != "" && strings.Contains(strings.ToLower(inputStr), "extract") {
			prompt = strings.ReplaceAll(e.prompts.ProfileExtraction, "{{.user_input}}", inputStr)
		}
	}
	if prompt == "" {
		prompt = fmt.Sprintf(
			"You are a professional assistant acting as %s agent.\n\nTask: %s\n\nProvide your output in JSON format.",
			e.category, inputStr,
		)
	}

	retries := e.maxRetries
	if retries <= 0 {
		retries = 1
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		callCtx := ctx
		var cancel context.CancelFunc
		if e.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, e.timeout)
		}

		result, err := e.llmService.GenerateSimple(callCtx, prompt)
		if cancel != nil {
			cancel()
		}
		if err == nil {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{
						ItemID:      e.agentID,
						Name:        e.agentName,
						Category:    e.agentType,
						Description: result,
					},
				},
			}, nil
		}
		lastErr = err
	}

	return nil, gerr.Wrapf(lastErr, "execute agent %s after %d retries", e.agentID, retries)
}

// ProcessStream executes a workflow step and returns a stream of events.
func (e *WorkflowAgentExecutor) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	events := make(chan base.AgentEvent, 64)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		defer close(events)
		if !sendAgentEvent(groupCtx, events, base.AgentEvent{
			Type:   base.EventTaskStart,
			Source: e.agentID,
			Data:   input,
		}) {
			return groupCtx.Err()
		}
		result, err := e.Process(groupCtx, input)
		if err != nil {
			sendAgentEvent(groupCtx, events, base.AgentEvent{
				Type:   base.EventComplete,
				Source: e.agentID,
				Err:    err,
			})
			return nil
		}
		if !sendAgentEvent(groupCtx, events, base.AgentEvent{
			Type:   base.EventTaskComplete,
			Source: e.agentID,
			Data:   result,
		}) {
			return groupCtx.Err()
		}
		sendAgentEvent(groupCtx, events, base.AgentEvent{
			Type:   base.EventComplete,
			Source: e.agentID,
		})
		return nil
	})
	return events, nil
}

func sendAgentEvent(ctx context.Context, events chan<- base.AgentEvent, event base.AgentEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}
