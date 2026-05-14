// Package client provides workflow orchestration functionality.
package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmSvc "github.com/Timwood0x10/goagent/api/service/llm"
	"github.com/Timwood0x10/goagent/internal/agents/base"
	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/workflow/engine"
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
		return nil, errors.Wrap(err, "load workflow")
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

	llmService *llmSvc.Service

	prompts *PromptsConfig

	timeout time.Duration

	maxRetries int

	category string
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

	return models.AgentStatusReady

}

// Start starts the agent.

func (e *WorkflowAgentExecutor) Start(ctx context.Context) error {

	return nil

}

// Stop stops the agent.

func (e *WorkflowAgentExecutor) Stop(ctx context.Context) error {

	return nil

}

// Process executes a workflow step.

func (e *WorkflowAgentExecutor) Process(ctx context.Context, input any) (any, error) {

	inputStr, ok := input.(string)

	if !ok {

		return nil, fmt.Errorf("input must be string")

	}

	// Build prompt based on agent type and configured prompts

	var prompt string

	// Check if we have configured prompts

	if e.prompts != nil {

		// Use recommendation template if available

		if e.prompts.Recommendation != "" {

			// Replace template variables

			prompt = e.prompts.Recommendation

			// Replace category

			if e.category != "" {

				prompt = strings.ReplaceAll(prompt, "{{.category}}", e.category)

			}

			// Replace {{.input}} with actual user input

			prompt = strings.ReplaceAll(prompt, "{{.input}}", inputStr)

			// Replace requirements with user input as fallback.
			prompt = strings.ReplaceAll(prompt, "{{.requirements}}", inputStr)

		} else if e.prompts.ProfileExtraction != "" && strings.Contains(strings.ToLower(inputStr), "extract") {

			// Use profile extraction template

			prompt = strings.ReplaceAll(e.prompts.ProfileExtraction, "{{.user_input}}", inputStr)

		}

	}

	// Fallback prompt if no template was used.
	if prompt == "" {
		prompt = fmt.Sprintf(
			"You are a professional assistant acting as %s agent.\n\nTask: %s\n\nProvide your output in JSON format.",
			e.category, inputStr,
		)
	}

	// Execute with LLM

	result, err := e.llmService.GenerateSimple(ctx, prompt)

	if err != nil {

		return nil, errors.Wrapf(err, "execute agent %s", e.agentID)

	}

	// Return a simple recommend result

	return &models.RecommendResult{

		Items: []*models.RecommendItem{

			{

				ItemID: e.agentID,

				Name: e.agentName,

				Category: e.agentType,

				Description: result,
			},
		},
	}, nil

}
