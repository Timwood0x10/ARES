// Package llm provides the public API bridge for LLM services.
// It wraps internal/llm.Client and implements api/core.LLMService.
package llm

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/api/core"
	internal "github.com/Timwood0x10/ares/internal/llm"
)

// Service wraps internal/llm.Client and implements core.LLMService.
type Service struct {
	client *internal.Client
	config *core.LLMConfig
}

// New creates a new LLM service from an internal client.
// Args:
//
//	client - the internal LLM client.
//
// Returns:
//
//	service - the initialized LLM service.
//	err - error if client is nil.
func New(client *internal.Client) (*Service, error) {
	if client == nil {
		return nil, fmt.Errorf("llm: client is nil")
	}
	return &Service{
		client: client,
		config: &core.LLMConfig{
			Provider: core.LLMProvider(client.GetProvider()),
			Model:    client.GetModel(),
		},
	}, nil
}

// Generate generates text from the given messages.
func (s *Service) Generate(ctx context.Context, request *core.GenerateRequest) (*core.GenerateResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("llm: request is nil")
	}

	messages := make([]*core.LLMMessage, len(request.Messages))
	copy(messages, request.Messages)

	tools := request.Tools
	if tools == nil {
		tools = []core.Tool{}
	}

	resp, err := s.client.Chat(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("llm: generate: %w", err)
	}

	return resp, nil
}

// GenerateSimple generates text from a simple prompt string.
func (s *Service) GenerateSimple(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("llm: prompt is empty")
	}

	resp, err := s.client.Generate(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("llm: generate simple: %w", err)
	}

	return resp, nil
}

// GenerateEmbedding returns an error as embeddings are not supported via this service.
func (s *Service) GenerateEmbedding(_ context.Context, _ *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	return nil, fmt.Errorf("llm: embeddings not supported by this service")
}

// GetConfig returns the current LLM configuration.
func (s *Service) GetConfig() *core.LLMConfig {
	return s.config
}

// IsEnabled checks if the LLM client is configured and available.
func (s *Service) IsEnabled() bool {
	return s.client.IsEnabled()
}

// GetProvider returns the current LLM provider.
func (s *Service) GetProvider() core.LLMProvider {
	return core.LLMProvider(s.client.GetProvider())
}

// GetModel returns the current model name.
func (s *Service) GetModel() string {
	return s.client.GetModel()
}
