// Package llm provides the public API for LLM operations.
package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/llm"
)

// Config holds configuration for the LLM service.
// This is a public type that wraps the internal Config to avoid
// leaking internal package types into the public API.
type Config struct {
	// BaseConfig is the base configuration (timeout, retries, etc.).
	BaseConfig *core.BaseConfig
	// LLMConfig is the LLM provider configuration.
	LLMConfig *core.LLMConfig
	// Fallbacks is a list of fallback LLM configs for failover.
	// When non-empty, a FailoverClient is created instead of a single Client.
	Fallbacks []*core.LLMConfig
}

// llmClient is the minimal surface the Service needs from the underlying
// internal/llm client (or failover client).
type llmClient interface {
	Generate(ctx context.Context, prompt string) (string, error)
	Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool, params map[string]any) (*core.GenerateResponse, error)
	IsEnabled() bool
	GetProvider() string
	GetModel() string
	Close()
}

// buildClient constructs the underlying internal/llm client (or failover
// client when fallbacks are configured) from the public Config.
func (c *Config) buildClient() (llmClient, error) {
	if c == nil || c.LLMConfig == nil {
		return nil, fmt.Errorf("llm: config or LLMConfig is nil")
	}
	timeout := c.LLMConfig.Timeout
	if timeout <= 0 && c.BaseConfig != nil && c.BaseConfig.RequestTimeout > 0 {
		timeout = int(c.BaseConfig.RequestTimeout.Seconds())
	}
	primary := &llm.Config{
		Provider:        string(c.LLMConfig.Provider),
		APIKey:          c.LLMConfig.APIKey,
		BaseURL:         c.LLMConfig.BaseURL,
		Model:           c.LLMConfig.Model,
		Timeout:         timeout,
		MaxTokens:       c.LLMConfig.MaxTokens,
		MaxPromptLength: c.LLMConfig.MaxPromptLength,
	}
	if len(c.Fallbacks) == 0 {
		return llm.NewClient(primary)
	}
	configs := []*llm.Config{primary}
	for _, f := range c.Fallbacks {
		configs = append(configs, &llm.Config{
			Provider:        string(f.Provider),
			APIKey:          f.APIKey,
			BaseURL:         f.BaseURL,
			Model:           f.Model,
			Timeout:         f.Timeout,
			MaxTokens:       f.MaxTokens,
			MaxPromptLength: f.MaxPromptLength,
		})
	}
	return llm.NewFailoverClient(configs, 30*time.Second, 0, 0)
}

// Service wraps the internal/llm client for public consumption.
//
// TODO(tech-debt): this previously delegated to internal/llmservice, a facade
// that added failover, observability, callbacks, repo audit, and an embedding
// client on top of internal/llm. internal/llmservice was removed as dead/SDK-only
// debt; this Service now wraps internal/llm directly. Embedding generation and
// the repo-audit path are not yet re-added — re-introduce them here if SDK
// consumers need them (see GenerateEmbedding).
type Service struct {
	cfg    *Config
	client llmClient
}

// NewService creates a new LLM service with the given config.
func NewService(cfg *Config) (*Service, error) {
	client, err := cfg.buildClient()
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, client: client}, nil
}

// Generate routes the request to the underlying chat client.
func (s *Service) Generate(ctx context.Context, request *core.GenerateRequest) (*core.GenerateResponse, error) {
	params := map[string]any{}
	if request.Temperature != nil {
		params["temperature"] = *request.Temperature
	}
	if request.MaxTokens != nil {
		params["max_tokens"] = *request.MaxTokens
	}
	return s.client.Chat(ctx, request.Messages, request.Tools, params)
}

// GenerateSimple delegates to the underlying client's Generate.
func (s *Service) GenerateSimple(ctx context.Context, prompt string) (string, error) {
	return s.client.Generate(ctx, prompt)
}

// GenerateEmbedding is not supported after internal/llmservice was removed.
//
// TODO(tech-debt): internal/llm does not provide an embedding client.
// Re-add embedding support (delegate to an injected client or a provider) if
// SDK consumers require GenerateEmbedding.
func (s *Service) GenerateEmbedding(_ context.Context, _ *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	return nil, fmt.Errorf("llm: GenerateEmbedding not implemented (llmservice removed; see TODO)")
}

// GetConfig returns the current LLM configuration.
func (s *Service) GetConfig() *core.LLMConfig {
	return s.cfg.LLMConfig
}

// IsEnabled checks if the LLM service is properly configured and available.
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

// Close releases resources held by the LLM service.
func (s *Service) Close() {
	s.client.Close()
}
