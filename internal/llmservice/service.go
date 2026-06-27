// Package llm provides LLM service implementation.
package llmservice

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/llm"
)

// LLMClient is the interface satisfied by both *llm.Client and *llm.FailoverClient.
type LLMClient interface {
	Generate(ctx context.Context, prompt string) (string, error)
	GenerateStream(ctx context.Context, prompt string) (<-chan llm.StreamChunk, error)
	IsEnabled() bool
	GetProvider() string
	GetModel() string
	Close()
}

// Service provides LLM operations.
type Service struct {
	client          LLMClient
	repo            core.LLMRepository
	config          *core.BaseConfig
	llmConfig       *core.LLMConfig
	embeddingClient any // Can be *embedding.EmbeddingClient or nil
}

// Config represents service configuration.
type Config struct {
	// BaseConfig is the base configuration.
	BaseConfig *core.BaseConfig
	// LLMConfig is the LLM configuration.
	LLMConfig *core.LLMConfig
	// Fallbacks is a list of fallback LLM configs for failover.
	// When non-empty, a FailoverClient is created instead of a single Client.
	Fallbacks []*llm.Config
	// Repo is the LLM repository (optional, for logging/audit).
	Repo core.LLMRepository
	// EmbeddingClient is the embedding service client (optional).
	EmbeddingClient any
	// Tracer is an optional ares_observability tracer for LLM call tracing.
	Tracer ares_observability.Tracer
	// CallbackRegistry is an optional callback registry for lifecycle event emission.
	// When set, Generate and GenerateStream calls will emit events to this registry.
	CallbackRegistry *ares_callbacks.Registry
}

// NewService creates a new LLM service instance.
// Args:
// config - service configuration.
// Returns new LLM service instance or error.
func NewService(config *Config) (*Service, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}

	if config.LLMConfig == nil {
		return nil, ErrInvalidLLMConfig
	}

	if config.BaseConfig == nil {
		config.BaseConfig = &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		}
	}

	// Create internal LLM client (with optional failover)
	internalConfig := &llm.Config{
		Provider: string(config.LLMConfig.Provider),
		APIKey:   config.LLMConfig.APIKey,
		BaseURL:  config.LLMConfig.BaseURL,
		Model:    config.LLMConfig.Model,
		Timeout:  config.LLMConfig.Timeout,
	}

	var client LLMClient
	if len(config.Fallbacks) > 0 {
		configs := append([]*llm.Config{internalConfig}, config.Fallbacks...)
		fc, err := llm.NewFailoverClient(configs, 0, 0, 0)
		if err != nil {
			return nil, errors.Wrap(err, "create failover LLM client")
		}
		if config.Tracer != nil {
			fc.SetTracer(config.Tracer)
		}
		client = fc
	} else {
		c, err := llm.NewClient(internalConfig)
		if err != nil {
			return nil, errors.Wrap(err, "create LLM client")
		}
		if config.Tracer != nil {
			c.SetTracer(config.Tracer)
		}
		if config.CallbackRegistry != nil {
			llm.WithCallbacks(config.CallbackRegistry)(c)
		}
		client = c
	}

	return &Service{
		client:          client,
		repo:            config.Repo,
		config:          config.BaseConfig,
		llmConfig:       config.LLMConfig,
		embeddingClient: config.EmbeddingClient,
	}, nil
}

// Generate generates text from the given messages.
// Args:
// ctx - operation context.
// request - the generation request.
// Returns the generation response or error.
func (s *Service) Generate(ctx context.Context, request *core.GenerateRequest) (*core.GenerateResponse, error) {
	if request == nil {
		return nil, ErrInvalidConfig
	}

	if len(request.Messages) == 0 {
		return nil, ErrInvalidMessages
	}

	// Build prompt from messages
	prompt := s.buildPrompt(request.Messages)

	// Generate text
	content, err := s.client.Generate(ctx, prompt)
	if err != nil {
		return nil, errors.Wrap(err, "generate text")
	}

	response := &core.GenerateResponse{
		Content:      content,
		FinishReason: "stop",
		Usage: core.TokenUsage{
			PromptTokens:     s.calculateTokens(prompt),
			CompletionTokens: s.calculateTokens(content),
			TotalTokens:      0, // Will be calculated below
		},
		Model: s.getModel(),
	}

	response.Usage.TotalTokens = response.Usage.PromptTokens + response.Usage.CompletionTokens

	// Log generation if repository is available
	if s.repo != nil {
		if err := s.repo.LogGeneration(ctx, request, response); err != nil {
			// Log error but don't fail the request
			slog.Warn("failed to log generation", "error", err)
		}
	}

	return response, nil
}

// GenerateSimple generates text from a simple prompt.
// Args:
// ctx - operation context.
// prompt - the prompt text.
// Returns the generated text or error.
func (s *Service) GenerateSimple(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", ErrInvalidPrompt
	}

	content, err := s.client.Generate(ctx, prompt)
	if err != nil {
		return "", errors.Wrap(err, "generate text")
	}

	return content, nil
}

// GenerateEmbedding generates an embedding for the given text.
// Args:
// ctx - operation context.
// request - the embedding request.
// Returns the embedding response or error.
func (s *Service) GenerateEmbedding(ctx context.Context, request *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	if request == nil {
		return nil, ErrInvalidConfig
	}

	if request.Input == "" {
		return nil, ErrInvalidInput
	}

	// Try to use embedding client if available
	var embedding []float32
	var embeddingModel string

	if s.embeddingClient != nil {
		// Use type assertion to check if it's an embedding client
		if embedder, ok := s.embeddingClient.(interface {
			Embed(ctx context.Context, text string) ([]float64, error)
		}); ok {
			// Generate embedding using the embedding service
			embeddingFloat64, err := embedder.Embed(ctx, request.Input)
			if err != nil {
				return nil, errors.Wrap(err, "generate embedding")
			}

			// Convert float64 to float32
			embedding = make([]float32, len(embeddingFloat64))
			for i, v := range embeddingFloat64 {
				embedding[i] = float32(v)
			}

			// Get model name from embedding client if available
			if modelGetter, ok := s.embeddingClient.(interface {
				GetModel() string
			}); ok {
				embeddingModel = modelGetter.GetModel()
			}
		} else {
			// Embedding client type not recognized, return error
			return nil, fmt.Errorf("embedding client type not supported")
		}
	} else {
		// No embedding client available, return error
		return nil, fmt.Errorf("embedding service not configured")
	}

	response := &core.EmbeddingResponse{
		Embedding: embedding,
		Model:     embeddingModel,
		Usage: core.TokenUsage{
			PromptTokens: s.calculateTokens(request.Input),
			TotalTokens:  s.calculateTokens(request.Input),
		},
	}

	return response, nil
}

// GetConfig returns the current LLM configuration.
// Returns the LLM configuration.
func (s *Service) GetConfig() *core.LLMConfig {
	return s.llmConfig
}

// IsEnabled checks if the LLM service is properly configured and available.
// Returns true if enabled, false otherwise.
func (s *Service) IsEnabled() bool {
	return s.client.IsEnabled()
}

// GetProvider returns the current LLM provider.
// Returns the provider type.
func (s *Service) GetProvider() core.LLMProvider {
	if s.llmConfig != nil {
		return s.llmConfig.Provider
	}
	return ""
}

// GetModel returns the current model name.
// Returns the model name.
func (s *Service) GetModel() string {
	if s.llmConfig != nil {
		return s.llmConfig.Model
	}
	return ""
}

// buildPrompt builds a prompt from messages.
func (s *Service) buildPrompt(messages []*core.LLMMessage) string {
	prompt := ""
	for _, msg := range messages {
		prompt += fmt.Sprintf("[%s]: %s\n", msg.Role, msg.Content)
	}
	return prompt
}

// Close releases resources held by the LLM service.
func (s *Service) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// getModel returns the model name to use.
func (s *Service) getModel() string {
	if s.llmConfig != nil && s.llmConfig.Model != "" {
		return s.llmConfig.Model
	}
	return "default"
}

// calculateTokens estimates the number of tokens in a text string.
// Uses a simple heuristic: approximately 4 characters per token for English text.
// This is a rough estimate; actual tokenization depends on the model's tokenizer.
func (s *Service) calculateTokens(text string) int {
	if text == "" {
		return 0
	}

	// Count runes (Unicode code points) instead of bytes for better accuracy
	runeCount := len([]rune(text))

	// Heuristic: ~4 characters per token for average text
	// Adjust based on content type
	estimatedTokens := runeCount / 4

	// Ensure at least 1 token if there's content
	if estimatedTokens == 0 && runeCount > 0 {
		estimatedTokens = 1
	}

	return estimatedTokens
}
