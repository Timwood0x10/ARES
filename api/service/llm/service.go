// Package llm provides the public API for LLM operations.
package llm

import (
	"context"

	"github.com/Timwood0x10/ares/api/core"
	internal "github.com/Timwood0x10/ares/internal/llmservice"
)

// Config re-exports internal's LLM service config.
type Config = internal.Config

// Service wraps internal/llmservice.Service for public consumption.
type Service struct {
	inner *internal.Service
}

// NewService creates a new LLM service with the given config.
func NewService(cfg *Config) (*Service, error) {
	s, err := internal.NewService(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{inner: s}, nil
}

// Generate delegates to the inner service.
func (s *Service) Generate(ctx context.Context, request *core.GenerateRequest) (*core.GenerateResponse, error) {
	return s.inner.Generate(ctx, request)
}

// GenerateSimple delegates to the inner service.
func (s *Service) GenerateSimple(ctx context.Context, prompt string) (string, error) {
	return s.inner.GenerateSimple(ctx, prompt)
}

// GenerateEmbedding delegates to the inner service.
func (s *Service) GenerateEmbedding(ctx context.Context, request *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	return s.inner.GenerateEmbedding(ctx, request)
}

// GetConfig returns the current LLM configuration.
func (s *Service) GetConfig() *core.LLMConfig {
	return s.inner.GetConfig()
}

// IsEnabled checks if the LLM service is properly configured and available.
func (s *Service) IsEnabled() bool {
	return s.inner.IsEnabled()
}

// GetProvider returns the current LLM provider.
func (s *Service) GetProvider() core.LLMProvider {
	return s.inner.GetProvider()
}

// GetModel returns the current model name.
func (s *Service) GetModel() string {
	return s.inner.GetModel()
}

// Close releases resources held by the LLM service.
func (s *Service) Close() {
	s.inner.Close()
}
