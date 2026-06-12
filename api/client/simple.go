// Package client provides simple, fool-proof API for GoAgent.
package client

import (
	"context"
	"fmt"

	"goagentx/api/core"
	"goagentx/internal/errors"
)

// SimpleClient provides the simplest possible API for GoAgent.
// Just configure and call!
type SimpleClient struct {
	client *Client
	config *ConfigFile
}

// NewSimpleClient creates a simple client from config file.
// This is the easiest way to use GoAgent - just load config and go!
// Args:
// configPath - path to config file (empty string for default).
// Returns simple client or error.
//
// Example:
//
//	client, err := client.NewSimpleClient("config.yaml")
//	if err != nil {
//	    slog.Error(err)
//	}
//	defer client.Close()
//
//	// Execute a task
//	result, err := client.Execute("user query here")
//	if err != nil {
//	    slog.Error(err)
//	}
//	fmt.Println(result)
func NewSimpleClient(configPath string) (*SimpleClient, error) {
	// Load config
	loader := NewConfigLoader()
	config, err := loader.Load(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "load config")
	}

	// Create underlying client
	underlyingClient, err := NewClientFromConfigPath(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	return &SimpleClient{
		client: underlyingClient,
		config: config,
	}, nil
}

// Execute executes a user query using the configured agents.
// This is the main entry point - just pass your question and get the answer!
// Args:
// ctx - context for the operation.
// query - user's question or task description.
// Returns result text or error.
//
// Example:
//
//	result, err := client.Execute(ctx, "Find me some casual shirts for daily commute, budget 500-1000")
//	if err != nil {
//	    slog.Error(err)
//	}
//	fmt.Println(result)
func (s *SimpleClient) Execute(ctx context.Context, query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("query cannot be empty")
	}

	// Create prompt from user query
	prompt := fmt.Sprintf("User query: %s\n\nPlease provide a helpful response.", query)

	// Use LLM to generate response
	llmService, err := s.client.LLM()
	if err != nil {
		return "", errors.Wrap(err, "get LLM service")
	}

	response, err := llmService.GenerateSimple(ctx, prompt)
	if err != nil {
		return "", errors.Wrap(err, "generate response")
	}

	return response, nil
}

// ExecuteWithAgent executes a query using a specific agent.
// Args:
// ctx - context for the operation.
// agentID - the ID of the agent to use.
// query - user's question or task description.
// Returns result text or error.
//
// Example:
//
//	result, err := client.ExecuteWithAgent(ctx, "agent-top", "Recommend some casual shirts")
//	if err != nil {
//	    slog.Error(err)
//	}
//	fmt.Println(result)
func (s *SimpleClient) ExecuteWithAgent(ctx context.Context, agentID, query string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agent ID cannot be empty")
	}
	if query == "" {
		return "", fmt.Errorf("query cannot be empty")
	}

	// Create prompt with agent context
	prompt := fmt.Sprintf("You are agent: %s\nUser query: %s\n\nPlease provide a helpful response.", agentID, query)

	// Use LLM to generate response
	llmService, err := s.client.LLM()
	if err != nil {
		return "", errors.Wrap(err, "get LLM service")
	}

	response, err := llmService.GenerateSimple(ctx, prompt)
	if err != nil {
		return "", errors.Wrap(err, "generate response")
	}

	return response, nil
}

// Chat conducts a multi-turn conversation.
// Args:
// ctx - context for the operation.
// messages - list of messages in the conversation.
// Returns assistant's response or error.
//
// Example:
//
//	messages := []core.Message{
//	    {Role: core.MessageRoleUser, Content: "I want to buy clothes"},
//	    {Role: core.MessageRoleAssistant, Content: "What style do you prefer?"},
//	    {Role: core.MessageRoleUser, Content: "Casual style"},
//	}
//	response, err := client.Chat(ctx, messages)
//	if err != nil {
//	    slog.Error(err)
//	}
//	fmt.Println(response)
func (s *SimpleClient) Chat(ctx context.Context, messages []*core.Message) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("messages cannot be empty")
	}

	// Build conversation history
	var conversation string
	for _, msg := range messages {
		conversation += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
	}

	// Generate response
	llmService, err := s.client.LLM()
	if err != nil {
		return "", errors.Wrap(err, "get LLM service")
	}

	prompt := fmt.Sprintf("Conversation history:\n%s\n\nPlease respond to the last user message.", conversation)
	response, err := llmService.GenerateSimple(ctx, prompt)
	if err != nil {
		return "", errors.Wrap(err, "generate response")
	}

	return response, nil
}

// Close closes the client and cleans up resources.
// Args:
// ctx - context for the operation.
// Returns error if cleanup fails.
func (s *SimpleClient) Close(ctx context.Context) error {
	return s.client.Close(ctx)
}

// GetConfig returns the loaded configuration.
// Returns configuration file structure.
func (s *SimpleClient) GetConfig() *ConfigFile {
	return s.config
}
