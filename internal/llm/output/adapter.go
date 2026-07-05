package output

import (
	"context"

	"github.com/Timwood0x10/ares/internal/core/models"
)

const (
	defaultModel = "gpt-3.5-turbo"
)

// LLMAdapter defines the interface for LLM providers.
type LLMAdapter interface {
	// Generate generates text from prompt.
	Generate(ctx context.Context, prompt string) (string, error)
	// GenerateStructured generates structured output.
	GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error)
	// GenerateStream generates text as a stream of chunks.
	// Returns a channel of StreamChunk. The channel is closed when streaming completes.
	GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error)
	// GetModel returns the model name.
	GetModel() string
}

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	// Content is the text content of this chunk. May be empty for final chunk.
	Content string
	// Done indicates this is the final chunk. When true, Err should be checked.
	Done bool
	// Err contains any error that occurred during streaming. Non-nil only on final chunk.
	Err error
}

// Config holds LLM configuration.
type Config struct {
	Provider        string
	Model           string
	BaseURL         string
	APIKey          string
	MaxTokens       int
	MaxPromptLength int
	Temperature     float64
	Timeout         int
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		Model:       defaultModel,
		BaseURL:     "https://api.openai.com/v1",
		MaxTokens:   2048,
		Temperature: 0.7,
		Timeout:     60,
	}
}
