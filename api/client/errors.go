// Package client provides error definitions for API client.
package client

import "errors"

var (
	// ErrInvalidConfig is returned when config is nil or invalid.
	ErrInvalidConfig = errors.New("invalid config")

	// ErrAgentNotConfigured is returned when agent service is not configured.
	ErrAgentNotConfigured = errors.New("agent service not configured")

	// ErrMemoryNotConfigured is returned when memory service is not configured.
	ErrMemoryNotConfigured = errors.New("memory service not configured")

	// ErrRetrievalNotConfigured is returned when retrieval service is not configured.
	ErrRetrievalNotConfigured = errors.New("retrieval service not configured")

	// ErrLLMNotConfigured is returned when LLM service is not configured.
	ErrLLMNotConfigured = errors.New("LLM service not configured")

	// ErrWorkflowNotConfigured is returned when workflow service is not configured.
	ErrWorkflowNotConfigured = errors.New("workflow service not configured")
)
