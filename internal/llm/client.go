// Package llm provides LLM client functionality for various providers.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/errors"
)

// HTTPError represents an HTTP request error.
type HTTPError struct {
	StatusCode int
	Message    string
}

// Error returns the error message.
func (e *HTTPError) Error() string {
	return e.Message
}

// ProviderType represents the LLM provider type.
type ProviderType string

const (
	ProviderOpenRouter ProviderType = "openrouter"
	ProviderOllama     ProviderType = "ollama"

	// DefaultOllamaBaseURL is the default base URL for Ollama provider.
	DefaultOllamaBaseURL = "http://localhost:11434"

	// DefaultOpenRouterBaseURL is the default base URL for OpenRouter provider.
	DefaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

	// DefaultOllamaModel is the default model for Ollama provider.
	DefaultOllamaModel = "llama3.2"

	// DefaultOpenRouterModel is the default model for OpenRouter provider.
	DefaultOpenRouterModel = "openai/gpt-3.5-turbo"
)

// Config holds LLM client configuration.
type Config struct {
	Provider string            `yaml:"provider"`
	APIKey   string            `yaml:"api_key"`
	BaseURL  string            `yaml:"base_url"`
	Model    string            `yaml:"model"`
	Timeout  int               `yaml:"timeout"`
	Extra    map[string]string `yaml:"extra"`
}

// Client represents an LLM client that supports multiple providers.
type Client struct {
	config     *Config
	httpClient *http.Client
}

// NewClient creates a new LLM client.
func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, coreerrors.ErrInvalidArgument
	}

	if config.Timeout <= 0 {
		config.Timeout = 60
	}

	return &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}, nil
}

// Generate sends a text generation request to the LLM.
// Args:
// ctx - operation context.
// prompt - the prompt text.
// Returns generated text or error.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	// Validate prompt input
	if prompt == "" {
		return "", coreerrors.ErrInvalidArgument
	}

	// Check if prompt is too long (max 8192 characters)
	const maxPromptLength = 8192
	if len(prompt) > maxPromptLength {
		return "", fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
	}

	// Check if prompt contains only whitespace
	trimmed := []byte(prompt)
	trimmed = bytes.TrimSpace(trimmed)
	if len(trimmed) == 0 {
		return "", coreerrors.ErrInvalidArgument
	}

	switch ProviderType(c.config.Provider) {
	case ProviderOpenRouter:
		return c.generateOpenRouter(ctx, prompt)
	case ProviderOllama:
		return c.generateOllama(ctx, prompt)
	default:
		return "", fmt.Errorf("unsupported provider: %s", c.config.Provider)
	}
}

// generateOpenRouter generates text using OpenRouter API.
func (c *Client) generateOpenRouter(ctx context.Context, prompt string) (string, error) {
	if c.config.APIKey == "" {
		return "", fmt.Errorf("API key is required for OpenRouter")
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7,
		"max_tokens":  4096, // Increased for code generation
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	// Privacy: Omit referer to avoid exposing repository details.
	req.Header.Set("X-Title", "GoAgent")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return response.Choices[0].Message.Content, nil
}

// generateOllama generates text using Ollama API.
func (c *Client) generateOllama(ctx context.Context, prompt string) (string, error) {
	requestBody := map[string]interface{}{
		"model":  c.config.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.7,
			"num_predict": 4096, // Increased for code generation
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal request")
	}

	baseURL := c.config.BaseURL
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/generate", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("unexpected status code: %d, body: %s", resp.StatusCode, string(body)),
		}
	}

	var response struct {
		Response string `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	return response.Response, nil
}

// IsEnabled checks if the LLM client is properly configured.
func (c *Client) IsEnabled() bool {
	if c == nil || c.config == nil {
		return false
	}

	switch ProviderType(c.config.Provider) {
	case ProviderOpenRouter:
		return c.config.APIKey != ""
	case ProviderOllama:
		return true // Ollama doesn't require API key
	default:
		return false
	}
}

// GetProvider returns the current provider type.
func (c *Client) GetProvider() string {
	if c.config != nil {
		return c.config.Provider
	}
	return ""
}

// GetModel returns the current model name.
func (c *Client) GetModel() string {
	if c.config != nil {
		return c.config.Model
	}
	return ""
}

// NewClientFromEnv creates an LLM client from environment variables.
func NewClientFromEnv() (*Client, error) {
	config := &Config{
		Provider: os.Getenv("LLM_PROVIDER"),
		APIKey:   os.Getenv("LLM_API_KEY"),
		BaseURL:  os.Getenv("LLM_BASE_URL"),
		Model:    os.Getenv("LLM_MODEL"),
	}

	// Set defaults
	if config.Provider == "" {
		config.Provider = "ollama"
	}
	if config.BaseURL == "" {
		if config.Provider == "openrouter" {
			config.BaseURL = DefaultOpenRouterBaseURL
		} else {
			config.BaseURL = DefaultOllamaBaseURL
		}
	}
	if config.Model == "" {
		if config.Provider == "ollama" {
			config.Model = DefaultOllamaModel
		} else {
			config.Model = DefaultOpenRouterModel
		}
	}

	return NewClient(config)
}
