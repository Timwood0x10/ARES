// Package llm provides the public API for LLM operations.
package llm

import (
	"context"

	"github.com/Timwood0x10/ares/api/core"
	internal "github.com/Timwood0x10/ares/internal/llmservice"
)

// Config holds configuration for the LLM service.
// This is a public type that wraps the internal Config to avoid
// leaking internal package types into the public API.
type Config struct {
	// BaseConfig is the base configuration (timeout, retries, etc.).
	BaseConfig *core.BaseConfig
	// LLMConfig is the LLM provider configuration.
	LLMConfig *core.LLMConfig
	// Repo is the LLM repository (optional, for logging/audit).
	Repo core.LLMRepository
	// EmbeddingClient is the embedding service client (optional).
	EmbeddingClient any
}

// toInternal converts the public Config to the internal Config type.
func (c *Config) toInternal() *internal.Config {
	if c == nil {
		return nil
	}
	return &internal.Config{
		BaseConfig:      c.BaseConfig,
		LLMConfig:       c.LLMConfig,
		Repo:            c.Repo,
		EmbeddingClient: c.EmbeddingClient,
	}
}

// Service wraps internal/llmservice.Service for public consumption.
type Service struct {
	inner *internal.Service
}

// NewService creates a new LLM service with the given config.
func NewService(cfg *Config) (*Service, error) {
	s, err := internal.NewService(cfg.toInternal())
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
