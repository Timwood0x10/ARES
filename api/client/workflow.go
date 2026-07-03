// Package client provides workflow orchestration functionality.
package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/agents/base"
	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
	gerr "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// WorkflowClient provides workflow orchestration capabilities.
type WorkflowClient struct {
	client   *Client
	executor *engine.Executor
	loader   *engine.FileLoader
	registry *engine.AgentRegistry
}

// NewWorkflowClient creates a new workflow client.
// Args:
// client - underlying GoAgent client.
// Returns workflow client or error.
func NewWorkflowClient(client *Client) (*WorkflowClient, error) {
	loader := engine.NewYAMLFileLoader()

	// Create executor with agent registry
	registry := engine.NewAgentRegistry()
	executor := engine.NewExecutor(registry)

	return &WorkflowClient{
		client:   client,
		executor: executor,
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

// Execute executes a workflow with the given input.
// Args:
// ctx - operation context.
// workflow - workflow definition.
// input - initial input data.
// Returns workflow result or error.
func (w *WorkflowClient) Execute(ctx context.Context, workflow *engine.Workflow, input string) (*engine.WorkflowResult, error) {
	// Register agents from client config
	if w.client.configFile != nil {
		w.registerAgents(ctx)
	}

	// Execute workflow
	return w.executor.Execute(ctx, workflow, input)
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

// registerAgents registers agents from client configuration.
func (w *WorkflowClient) registerAgents(ctx context.Context) {
	if w.client.configFile == nil {
		return
	}

	// Register each sub-agent
	for _, agentConfig := range w.client.configFile.Agents.Sub {
		agentType := agentConfig.Type
		if err := w.registry.Register(agentType, func(ctx context.Context, config interface{}) (base.Agent, error) {
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
		}); err != nil {
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
	ch := make(chan base.AgentEvent, 64)

	go func() {
		defer close(ch)

		// Send task start event
		select {
		case ch <- base.AgentEvent{Type: base.EventTaskStart, Source: e.agentID, Data: input}:
		case <-ctx.Done():
			return
		}

		// Execute the task
		result, err := e.Process(ctx, input)
		if err != nil {
			select {
			case ch <- base.AgentEvent{Type: base.EventComplete, Source: e.agentID, Err: err}:
			case <-ctx.Done():
			}
			return
		}

		// Send task complete event with result data
		select {
		case ch <- base.AgentEvent{Type: base.EventTaskComplete, Source: e.agentID, Data: result}:
		case <-ctx.Done():
			return
		}

		// Send final completion event (no data — result already in EventTaskComplete)
		select {
		case ch <- base.AgentEvent{Type: base.EventComplete, Source: e.agentID}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
}
