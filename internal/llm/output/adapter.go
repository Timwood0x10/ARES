package output

import (
	"context"

	"github.com/Timwood0x10/goagent/internal/core/models"
)

// LLMAdapter defines the interface for LLM providers.
type LLMAdapter interface {
	// Generate generates text from prompt.
	Generate(ctx context.Context, prompt string) (string, error)
	// GenerateStructured generates structured output.
	GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error)
	// GetModel returns the model name.
	GetModel() string
}

// Config holds LLM configuration.
type Config struct {
	Provider    string
	Model       string
	BaseURL     string
	APIKey      string
	MaxTokens   int
	Temperature float64
	Timeout     int
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		Model:       "gpt-3.5-turbo",
		BaseURL:     "https://api.openai.com/v1",
		MaxTokens:   2048,
		Temperature: 0.7,
		Timeout:     60,
	}
}
